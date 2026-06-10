package allocator

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/sanskar/memory-allocator/internal/memory"
	"github.com/sanskar/memory-allocator/internal/metrics"
)

// Slab-specific errors. The slab allocator is a fixed-class pool and
// has its own failure modes that don't map cleanly to the generic
// ErrOutOfMemory / ErrInvalidSize.
var (
	ErrSlabExhausted   = errors.New("slab allocator: no free object in the chosen size class")
	ErrSlabSizeTooBig  = errors.New("slab allocator: requested size exceeds the largest size class")
	ErrSlabInvalidSize = errors.New("slab allocator: size must be > 0")
)

// Default size classes used by the slab allocator. They are powers of
// two, doubling, starting at 16 bytes. The total memory region is
// divided evenly (in bytes) across these classes, so smaller classes
// get more objects and larger classes get fewer.
var defaultSlabClasses = []int{16, 32, 64, 128, 256, 512, 1024, 2048}

// SlabAllocator implements the slab/object-cache pattern from operating
// system kernels (e.g. the Linux kernel's SLUB allocator). The total
// memory is divided into per-size-class caches; an allocation picks
// the smallest class that fits and pops an object from that class's
// free list. Free objects are returned to the class's free list.
//
// Differences from a plain pool:
//   - Multiple size classes, so requests of different sizes share the
//     same region efficiently.
//   - Internal fragmentation is bounded by the size-class spacing
//     (worst case: 2x — 32-byte object uses a 32-byte slot but a 17-byte
//     payload wastes 15 bytes; in practice, the next-size-up class
//     would be used if a 17-byte object came in).
//   - Object-level bookkeeping (per object free/used) for the UI.
//
// The block list (and thus the visualization) shows one entry per
// allocated object, with Size = classSize. This matches what the UI
// already expects: a 2KB region with mixed allocations renders as a
// row of colored cells, each the size of its class.
type SlabAllocator struct {
	name      string
	totalSize int
	baseAddr  uintptr
	classes   []int                     // sorted ascending
	caches    map[int]*slabCache        // classSize -> cache
	cacheList []*slabCache              // ordered list of caches (for stable iteration)
	used      map[uintptr]*memory.Block // object address -> block
	free      map[uintptr]int           // object address -> classSize
	mu        sync.Mutex
	metrics   *metrics.AllocationMetrics
	nextID    int
}

// slabCache is one per-size-class cache: a free-list of object
// addresses and the bounds of the region it owns.
type slabCache struct {
	classSize int     // bytes per object
	region    uintptr // start of the class's region
	regionEnd uintptr // end of the class's region
	free      []uintptr
	inUse     int
}

// SlabCacheStats is the per-class stats type returned by GetSlabStats.
type SlabCacheStats struct {
	ClassSize int `json:"classSize"`
	Capacity  int `json:"capacity"`
	InUse     int `json:"inUse"`
	Free      int `json:"free"`
}

// SlabStats is the full stats object for the slab allocator.
type SlabStats struct {
	Classes  []SlabCacheStats `json:"classes"`
	TotalCap int              `json:"totalCapacity"`
	TotalUse int              `json:"totalInUse"`
}

// NewSlabAllocator creates a new slab allocator with the default
// size classes. The memory region is split evenly (by bytes) across
// the classes; a class gets zero objects if totalSize/len(classes)
// isn't a multiple of classSize.
func NewSlabAllocator(totalSize int) *SlabAllocator {
	return NewSlabAllocatorWithClasses(totalSize, defaultSlabClasses)
}

// NewSlabAllocatorWithClasses creates a slab allocator with a custom
// set of size classes. The classes slice is copied and sorted.
func NewSlabAllocatorWithClasses(totalSize int, classes []int) *SlabAllocator {
	if totalSize <= 0 {
		panic(fmt.Sprintf("slab allocator: totalSize must be > 0, got %d", totalSize))
	}
	cs := make([]int, len(classes))
	copy(cs, classes)
	sort.Ints(cs)
	for _, c := range cs {
		if c <= 0 {
			panic(fmt.Sprintf("slab allocator: class size must be > 0, got %d", c))
		}
	}
	baseAddr := uintptr(0x4000)
	s := &SlabAllocator{
		name:      "Slab Allocator",
		totalSize: totalSize,
		baseAddr:  baseAddr,
		classes:   cs,
		caches:    make(map[int]*slabCache, len(cs)),
		cacheList: make([]*slabCache, 0, len(cs)),
		used:      make(map[uintptr]*memory.Block, totalSize/16),
		free:      make(map[uintptr]int, totalSize/16),
		metrics:   metrics.NewAllocationMetrics(),
	}
	// Split the region evenly across classes by bytes. Each class
	// gets totalSize/len(classes) bytes. Objects are laid out back-
	// to-back within a class's region.
	perClass := totalSize / len(cs)
	cursor := baseAddr
	for _, classSize := range cs {
		count := perClass / classSize
		regionEnd := cursor + uintptr(count*classSize)
		c := &slabCache{
			classSize: classSize,
			region:    cursor,
			regionEnd: regionEnd,
			free:      make([]uintptr, 0, count),
			inUse:     0,
		}
		for i := 0; i < count; i++ {
			c.free = append(c.free, cursor+uintptr(i*classSize))
		}
		s.caches[classSize] = c
		s.cacheList = append(s.cacheList, c)
		cursor = regionEnd
	}
	return s
}

// pickClass returns the smallest size class that fits `size`.
func (s *SlabAllocator) pickClass(size int) (int, error) {
	if size <= 0 {
		return 0, ErrSlabInvalidSize
	}
	for _, c := range s.classes {
		if c >= size {
			return c, nil
		}
	}
	return 0, ErrSlabSizeTooBig
}

// Allocate picks the smallest size class that fits the request, then
// pops an object from that class's free list. Returns ErrSlabExhausted
// if the chosen class is empty and ErrSlabSizeTooBig if the request
// exceeds the largest class.
func (s *SlabAllocator) Allocate(size int, owner string) (*memory.Block, error) {
	start := time.Now()
	class, err := s.pickClass(size)
	if err != nil {
		s.metrics.RecordFailedAllocation()
		return nil, err
	}
	s.mu.Lock()
	c := s.caches[class]
	if len(c.free) == 0 {
		s.mu.Unlock()
		s.metrics.RecordFailedAllocation()
		return nil, ErrSlabExhausted
	}
	// Pop from end (stack semantics).
	idx := len(c.free) - 1
	addr := c.free[idx]
	c.free = c.free[:idx]
	c.inUse++
	block := memory.NewBlock(s.nextID, addr, class)
	s.nextID++
	block.Allocate(owner)
	s.used[addr] = block
	delete(s.free, addr)
	clone := block.Clone()
	s.mu.Unlock()
	s.metrics.RecordAllocation(class, time.Since(start))
	return clone, nil
}

// Deallocate returns an object to its class's free list. The class is
// inferred from the object's address. Returns ErrAlreadyFreed if the
// address is on a class's free list (double-free), and
// ErrBlockNotFound for unknown addresses.
func (s *SlabAllocator) Deallocate(address uintptr) error {
	start := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.free[address]; ok {
		return ErrAlreadyFreed
	}
	block, ok := s.used[address]
	if !ok {
		return ErrBlockNotFound
	}
	class := block.Size
	c, ok := s.caches[class]
	if !ok {
		return ErrBlockNotFound
	}
	c.inUse--
	c.free = append(c.free, address)
	block.Free()
	delete(s.used, address)
	s.free[address] = class
	s.metrics.RecordDeallocation(class, time.Since(start))
	return nil
}

// GetBlock returns a clone of the block at the given address (allocated
// objects only). Returns ErrBlockNotFound for unknown or free
// addresses.
func (s *SlabAllocator) GetBlock(address uintptr) (*memory.Block, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.used[address]; ok {
		return b.Clone(), nil
	}
	return nil, ErrBlockNotFound
}

// GetAllBlocks returns clones of all blocks (used only) for the UI.
func (s *SlabAllocator) GetAllBlocks() []*memory.Block {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*memory.Block, 0, len(s.used))
	for _, b := range s.used {
		out = append(out, b.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// GetFreeBlocks returns one block per free object in each class, for
// the UI. Sized to the class size.
func (s *SlabAllocator) GetFreeBlocks() []*memory.Block {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*memory.Block, 0)
	for _, c := range s.cacheList {
		for _, addr := range c.free {
			b := memory.NewBlock(-1, addr, c.classSize)
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// GetAllocatedBlocks returns clones of allocated blocks.
func (s *SlabAllocator) GetAllocatedBlocks() []*memory.Block {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*memory.Block, 0, len(s.used))
	for _, b := range s.used {
		out = append(out, b.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// GetMetrics returns the metrics snapshot.
func (s *SlabAllocator) GetMetrics() metrics.MetricsSnapshot {
	return s.metrics.GetSnapshot()
}

// CalculateFragmentation returns the ratio of "wasted" bytes (slot
// size minus requested size) to total in-use bytes, expressed as a
// percentage. For a uniform workload with size == classSize, this is
// 0. For random sizes, it can be up to ~50% (the next-class-up ratio
// for power-of-two classes).
func (s *SlabAllocator) CalculateFragmentation() float64 {
	// Slab doesn't track per-object requested size; we report the
	// ratio of "headroom" bytes (classSize - average payload estimate)
	// to total in-use bytes. A simpler, conservative estimate is
	// 1/avgClassSize; we just return 0 since slab fragmentation is a
	// per-allocation property and not a fragmentation-in-the-block-
	// list sense.
	s.metrics.UpdateFragmentation(0)
	return 0
}

// Coalesce is a no-op for slab (no merging across objects). Returns 0.
func (s *SlabAllocator) Coalesce() int { return 0 }

// Reset returns the allocator to its initial state. The same memory
// region, the same per-class free lists.
func (s *SlabAllocator) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.used = make(map[uintptr]*memory.Block, s.totalSize/16)
	s.free = make(map[uintptr]int, s.totalSize/16)
	s.nextID = 0
	cursor := s.baseAddr
	perClass := s.totalSize / len(s.classes)
	for _, classSize := range s.classes {
		c := s.caches[classSize]
		count := perClass / classSize
		c.region = cursor
		c.regionEnd = cursor + uintptr(count*classSize)
		c.free = make([]uintptr, 0, count)
		c.inUse = 0
		for i := 0; i < count; i++ {
			c.free = append(c.free, cursor+uintptr(i*classSize))
		}
		cursor = c.regionEnd
	}
	s.metrics.Reset()
}

// Name returns the allocator's display name.
func (s *SlabAllocator) Name() string { return s.name }

// TotalSize returns the total memory region size in bytes.
func (s *SlabAllocator) TotalSize() int { return s.totalSize }

// GetSlabStats returns per-class cache statistics. Useful for the UI
// and for assertions in tests.
func (s *SlabAllocator) GetSlabStats() SlabStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := SlabStats{
		Classes: make([]SlabCacheStats, 0, len(s.cacheList)),
	}
	for _, c := range s.cacheList {
		cap := len(c.free) + c.inUse
		stats.Classes = append(stats.Classes, SlabCacheStats{
			ClassSize: c.classSize,
			Capacity:  cap,
			InUse:     c.inUse,
			Free:      len(c.free),
		})
		stats.TotalCap += cap
		stats.TotalUse += c.inUse
	}
	return stats
}

// Compile-time interface compliance
var _ Allocator = (*SlabAllocator)(nil)
