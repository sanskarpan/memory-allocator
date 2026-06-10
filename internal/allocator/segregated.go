package allocator

import (
	"sort"
	"sync"
	"time"

	"github.com/sanskar/memory-allocator/internal/memory"
	"github.com/sanskar/memory-allocator/internal/metrics"
)

// DefaultSegregatedClasses is the default set of size classes. They
// are powers of two doubling from 16 to 4096, giving 9 classes. The
// total memory region is rounded up to the largest class so the
// highest class has at least one whole slot.
var DefaultSegregatedClasses = []int{16, 32, 64, 128, 256, 512, 1024, 2048, 4096}

// SegregatedFitAllocator implements a segregated free-list allocator:
// one free list per size class. Allocation picks the smallest class
// that fits the request, and falls back to splitting a larger free
// block from a higher class. Free returns the block to its class's
// free list, and (if the buddy at the same class is also free)
// merges them and promotes the result to the next class.
//
// The key difference from BuddyAllocator:
//   - Class sizes are an explicit slice, not derived from
//     minSize << level. The two allocators are isomorphic for
//     power-of-two classes, but segregated-fit supports
//     non-power-of-two classes (e.g. the FreeBSD malloc "small" bins
//     use {16, 32, 48, 64, 80, ...}).
//   - The free list per class stores block pointers (not just
//     addresses), so the UI can render each free slot as its own
//     visualization cell.
//
// Thread safety: a single sync.Mutex serialises all operations, like
// the existing fit-family allocators. The mutex is held during
// allocation/deallocation; metrics writes happen after unlock.
type SegregatedFitAllocator struct {
	name      string
	totalSize int
	baseAddr  uintptr
	classes   []int
	freeLists map[int][]*memory.Block // classSize -> free blocks
	blockMap  map[uintptr]*memory.Block
	blocks    *memory.BlockList
	metrics   *metrics.AllocationMetrics
	nextID    int
	mu        sync.Mutex
}

// NewSegregatedFitAllocator creates a new segregated-fit allocator
// with the default size classes. The total memory region is rounded
// up to a multiple of the largest class so every class has at least
// one whole slot.
func NewSegregatedFitAllocator(size int) *SegregatedFitAllocator {
	return NewSegregatedFitAllocatorWithClasses(size, DefaultSegregatedClasses)
}

// NewSegregatedFitAllocatorWithClasses creates a segregated-fit
// allocator with custom class sizes. The classes are sorted
// ascending. The largest class is the unit of the totalSize
// rounding.
func NewSegregatedFitAllocatorWithClasses(size int, classes []int) *SegregatedFitAllocator {
	if size <= 0 {
		size = 4096
	}
	cs := make([]int, len(classes))
	copy(cs, classes)
	sort.Ints(cs)
	for i := 1; i < len(cs); i++ {
		if cs[i] <= cs[i-1] {
			panic("segregated-fit: classes must be strictly ascending")
		}
	}
	maxClass := cs[len(cs)-1]
	total := ((size + maxClass - 1) / maxClass) * maxClass
	if total == 0 {
		total = maxClass
	}
	base := uintptr(0x5000)
	s := &SegregatedFitAllocator{
		name:      "Segregated Fit",
		totalSize: total,
		baseAddr:  base,
		classes:   cs,
		freeLists: make(map[int][]*memory.Block, len(cs)),
		blockMap:  make(map[uintptr]*memory.Block, total/16),
		blocks:    memory.NewBlockList(),
		metrics:   metrics.NewAllocationMetrics(),
		nextID:    1,
	}
	// Seed the free lists. Walk the region class-by-class from the
	// largest down, so each large slot is "split" into smaller
	// sub-slots only if needed. The simplest strategy: give the
	// largest class the whole region, then split its slots into the
	// next class's free list when allocations arrive. (This is
	// effectively buddy, but with explicit class sizes.)
	cursor := base
	id := 0
	// Lay out one big free block of the largest class; splitDown
	// will handle the rest on demand.
	initial := memory.NewBlock(id, cursor, total)
	id++
	s.blocks.Add(initial)
	s.freeLists[maxClass] = append(s.freeLists[maxClass], initial)
	s.nextID = id
	return s
}

// classFor returns the smallest class that fits size.
func (a *SegregatedFitAllocator) classFor(size int) (int, bool) {
	for _, c := range a.classes {
		if c >= size {
			return c, true
		}
	}
	return 0, false
}

// Allocate picks the smallest class that fits the request and pops a
// free block. If the chosen class is empty, it looks up the chain of
// larger classes and splits the first available block. Returns
// ErrOutOfMemory when no split is possible.
func (a *SegregatedFitAllocator) Allocate(size int, owner string) (*memory.Block, error) {
	start := time.Now()
	if size <= 0 {
		a.metrics.RecordFailedAllocation()
		return nil, ErrInvalidSize
	}
	class, ok := a.classFor(size)
	if !ok {
		a.metrics.RecordFailedAllocation()
		return nil, ErrOutOfMemory
	}
	a.mu.Lock()
	blk, ok := a.findAndPrepareLocked(class)
	if !ok {
		a.mu.Unlock()
		a.metrics.RecordFailedAllocation()
		return nil, ErrOutOfMemory
	}
	blk.Allocate(owner)
	blk.Size = class
	a.blockMap[blk.Address] = blk
	clone := blk.Clone()
	a.mu.Unlock()
	a.metrics.RecordAllocation(class, time.Since(start))
	return clone, nil
}

// findAndPrepareLocked finds a free block in `class` (or splits from
// a larger class), sets its state to Free (the caller will mark it
// Allocated), and returns it.
func (a *SegregatedFitAllocator) findAndPrepareLocked(class int) (*memory.Block, bool) {
	if list := a.freeLists[class]; len(list) > 0 {
		blk := list[len(list)-1]
		a.freeLists[class] = list[:len(list)-1]
		return blk, true
	}
	// Walk up the class chain.
	for i := 0; i < len(a.classes); i++ {
		if a.classes[i] <= class {
			continue
		}
		upperClass := a.classes[i]
		if list := a.freeLists[upperClass]; len(list) > 0 {
			upper := list[len(list)-1]
			a.freeLists[upperClass] = list[:len(list)-1]
			// Split `upper` (size=upperClass) into two blocks of
			// `a.classes[i-1]` (the next-smaller class). The
			// lower-addressed half is returned; the upper half
			// goes into the next-smaller class's free list.
			lowerSize := a.classes[i-1]
			upper.Size = lowerSize
			lower := upper
			buddyAddr := upper.Address + uintptr(lowerSize)
			buddy := memory.NewBlock(a.nextID, buddyAddr, lowerSize)
			a.nextID++
			a.blocks.InsertAfter(lower, buddy)
			a.freeLists[lowerSize] = append(a.freeLists[lowerSize], buddy)
			// Recurse to the next-smaller class to keep splitting
			// until we reach `class`.
			if lowerSize == class {
				return lower, true
			}
			// `lower` is now size lowerSize but we want `class`.
			// Recursively split `lower` further by inserting it
			// into the free list and recursing.
			a.freeLists[lowerSize] = append(a.freeLists[lowerSize], lower)
			return a.findAndPrepareLocked(class)
		}
	}
	return nil, false
}

// Deallocate returns a block to its class's free list. The class is
// inferred from the block's size. If the block's buddy (block XOR
// size) is also free in the same class, they are merged and the
// result is promoted to the next class.
func (a *SegregatedFitAllocator) Deallocate(address uintptr) error {
	start := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()

	block, ok := a.blockMap[address]
	if !ok {
		return ErrBlockNotFound
	}
	if block.IsFree() {
		return ErrAlreadyFreed
	}
	size := block.Size
	// Try buddy merge up the class chain.
	a.tryMergeUpLocked(block, size)
	a.metrics.RecordDeallocation(size, time.Since(start))
	return nil
}

// tryMergeUpLocked tries to merge `block` with its buddy at the
// given class. If the buddy is also free, merge and recurse to the
// next class up. If no merge, just push the block to its class's
// free list.
func (a *SegregatedFitAllocator) tryMergeUpLocked(block *memory.Block, class int) {
	idx := a.classIndex(class)
	if idx < 0 {
		// Unknown class — just put on the matching class.
		block.Free()
		a.freeLists[class] = append(a.freeLists[class], block)
		return
	}
	// Look for the buddy in the same class's free list.
	buddyAddr := block.Address ^ uintptr(class)
	// We need to be careful: XOR is the buddy formula only when the
	// region is aligned to class boundaries. For our layout
	// (largest class slot at base, split downward), blocks at the
	// same class are guaranteed to be aligned.
	list := a.freeLists[class]
	buddyIdx := -1
	for i, b := range list {
		if b.Address == buddyAddr {
			buddyIdx = i
			break
		}
	}
	if buddyIdx < 0 || idx == len(a.classes)-1 {
		// No free buddy, or already at top class — just put it back.
		block.Free()
		a.freeLists[class] = append(a.freeLists[class], block)
		return
	}
	// Merge: keep the lower-addressed block.
	buddy := list[buddyIdx]
	a.freeLists[class] = append(list[:buddyIdx], list[buddyIdx+1:]...)
	var merged *memory.Block
	if block.Address < buddy.Address {
		merged = block
		a.blocks.Remove(buddy)
	} else {
		merged = buddy
		a.blocks.Remove(block)
	}
	nextClass := a.classes[idx+1]
	merged.Size = nextClass
	merged.Free()
	a.tryMergeUpLocked(merged, nextClass)
}

func (a *SegregatedFitAllocator) classIndex(class int) int {
	for i, c := range a.classes {
		if c == class {
			return i
		}
	}
	return -1
}

// GetBlock returns a clone of the allocated block at address.
func (a *SegregatedFitAllocator) GetBlock(address uintptr) (*memory.Block, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if b, ok := a.blockMap[address]; ok {
		if b.IsFree() {
			return nil, ErrAlreadyFreed
		}
		return b.Clone(), nil
	}
	return nil, ErrBlockNotFound
}

// GetAllBlocks returns clones of all blocks in the linked list, both
// free and allocated. Sorted by address.
func (a *SegregatedFitAllocator) GetAllBlocks() []*memory.Block {
	a.mu.Lock()
	defer a.mu.Unlock()
	all := a.blocks.GetBlocks()
	out := make([]*memory.Block, len(all))
	for i, b := range all {
		out[i] = b.Clone()
	}
	return out
}

// GetFreeBlocks returns clones of all free blocks.
func (a *SegregatedFitAllocator) GetFreeBlocks() []*memory.Block {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]*memory.Block, 0)
	for _, list := range a.freeLists {
		for _, b := range list {
			out = append(out, b.Clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// GetAllocatedBlocks returns clones of allocated blocks.
func (a *SegregatedFitAllocator) GetAllocatedBlocks() []*memory.Block {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]*memory.Block, 0, len(a.blockMap))
	for _, b := range a.blockMap {
		if b.IsAllocated() {
			out = append(out, b.Clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// GetMetrics returns the metrics snapshot.
func (a *SegregatedFitAllocator) GetMetrics() metrics.MetricsSnapshot {
	return a.metrics.GetSnapshot()
}

// CalculateFragmentation: percent of free memory that is NOT in the
// largest free block. Returns 0 if no free memory.
func (a *SegregatedFitAllocator) CalculateFragmentation() float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	var total, largest int
	for _, list := range a.freeLists {
		for _, b := range list {
			total += b.Size
			if b.Size > largest {
				largest = b.Size
			}
		}
	}
	if total == 0 {
		a.metrics.UpdateFragmentation(0)
		return 0
	}
	frag := (1.0 - float64(largest)/float64(total)) * 100.0
	a.metrics.UpdateFragmentation(frag)
	return frag
}

// Coalesce is a no-op for segregated-fit; merges happen
// automatically on deallocation. Recalculates fragmentation for
// the metrics snapshot.
func (a *SegregatedFitAllocator) Coalesce() int {
	a.CalculateFragmentation()
	return 0
}

// Reset returns the allocator to its initial state.
func (a *SegregatedFitAllocator) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.blocks = memory.NewBlockList()
	a.blockMap = make(map[uintptr]*memory.Block, a.totalSize/16)
	a.metrics.Reset()
	a.nextID = 1
	a.freeLists = make(map[int][]*memory.Block, len(a.classes))
	for _, c := range a.classes {
		a.freeLists[c] = nil
	}
	maxClass := a.classes[len(a.classes)-1]
	initial := memory.NewBlock(0, a.baseAddr, a.totalSize)
	a.blocks.Add(initial)
	a.freeLists[maxClass] = append(a.freeLists[maxClass], initial)
}

// Name returns the allocator's display name.
func (a *SegregatedFitAllocator) Name() string { return a.name }

// TotalSize returns the total memory region size in bytes.
func (a *SegregatedFitAllocator) TotalSize() int { return a.totalSize }

// GetClassSummary returns counts of free blocks per class.
func (a *SegregatedFitAllocator) GetClassSummary() map[int]int {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[int]int, len(a.classes))
	for _, c := range a.classes {
		out[c] = len(a.freeLists[c])
	}
	return out
}

// Compile-time interface compliance
var _ Allocator = (*SegregatedFitAllocator)(nil)
var _ = metrics.AllocationMetrics{}
