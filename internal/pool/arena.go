package pool

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/sanskar/memory-allocator/internal/allocator"
	"github.com/sanskar/memory-allocator/internal/memory"
	"github.com/sanskar/memory-allocator/internal/metrics"
)

var (
	ErrArenaFull     = errors.New("arena full")
	ErrInvalidSize   = errors.New("invalid size")
	ErrArenaDealloc  = errors.New("arena allocator does not support individual deallocation")
	ErrBlockNotFound = errors.New("block not found")
)

// ArenaAllocator implements arena/region-based allocation. All memory is
// released together via Reset. Individual Deallocate returns an error.
type ArenaAllocator struct {
	name        string
	totalSize   int
	baseAddr    uintptr
	currentAddr uintptr
	blocks      []*memory.Block
	blockMap    map[uintptr]*memory.Block
	metrics     *metrics.AllocationMetrics
	nextID      int
	mu          sync.RWMutex
}

// NewArenaAllocator creates a new arena allocator
func NewArenaAllocator(size int) *ArenaAllocator {
	if size <= 0 {
		panic("arena allocator requires size > 0")
	}
	baseAddr := uintptr(0x3000)
	return &ArenaAllocator{
		name:        "Arena Allocator",
		totalSize:   size,
		baseAddr:    baseAddr,
		currentAddr: baseAddr,
		blocks:      make([]*memory.Block, 0),
		blockMap:    make(map[uintptr]*memory.Block),
		metrics:     metrics.NewAllocationMetrics(),
	}
}

// Allocate allocates memory using bump-pointer allocation
func (a *ArenaAllocator) Allocate(size int, owner string) (*memory.Block, error) {
	start := time.Now()
	var allocErr error
	defer func() {
		if allocErr != nil {
			a.metrics.RecordFailedAllocation()
		} else {
			a.metrics.RecordAllocation(size, time.Since(start))
		}
	}()

	if size <= 0 {
		allocErr = ErrInvalidSize
		return nil, ErrInvalidSize
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	endAddr := a.currentAddr + uintptr(size)
	maxAddr := a.baseAddr + uintptr(a.totalSize)
	if endAddr > maxAddr {
		allocErr = ErrArenaFull
		return nil, ErrArenaFull
	}

	block := memory.NewBlock(a.nextID, a.currentAddr, size)
	a.nextID++
	block.Allocate(owner)
	a.blocks = append(a.blocks, block)
	a.blockMap[block.Address] = block
	a.currentAddr = endAddr
	return block.Clone(), nil
}

// Deallocate is not supported; returns ErrArenaDealloc.
func (a *ArenaAllocator) Deallocate(address uintptr) error {
	return ErrArenaDealloc
}

// GetBlock retrieves a block by address
func (a *ArenaAllocator) GetBlock(address uintptr) (*memory.Block, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	block, ok := a.blockMap[address]
	if !ok {
		return nil, ErrBlockNotFound
	}
	return block.Clone(), nil
}

// GetAllBlocks returns all allocated blocks sorted by address
func (a *ArenaAllocator) GetAllBlocks() []*memory.Block {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*memory.Block, len(a.blocks))
	for i, b := range a.blocks {
		out[i] = b.Clone()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// GetFreeBlocks returns the remaining free region as a single block (if any)
func (a *ArenaAllocator) GetFreeBlocks() []*memory.Block {
	a.mu.RLock()
	defer a.mu.RUnlock()
	used := int(a.currentAddr - a.baseAddr)
	free := a.totalSize - used
	if free <= 0 {
		return []*memory.Block{}
	}
	return []*memory.Block{memory.NewBlock(-1, a.currentAddr, free)}
}

// GetAllocatedBlocks returns all allocated blocks
func (a *ArenaAllocator) GetAllocatedBlocks() []*memory.Block {
	return a.GetAllBlocks()
}

// GetMetrics returns allocation metrics
func (a *ArenaAllocator) GetMetrics() metrics.MetricsSnapshot {
	return a.metrics.GetSnapshot()
}

// CalculateFragmentation returns 0 (linear allocation has no fragmentation)
func (a *ArenaAllocator) CalculateFragmentation() float64 { return 0.0 }

// Coalesce returns 0 (no coalescing needed)
func (a *ArenaAllocator) Coalesce() int { return 0 }

// Reset frees all memory at once. All metrics are cleared; no individual
// deallocation events are recorded because the entire arena is released in
// bulk.
func (a *ArenaAllocator) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.blocks = make([]*memory.Block, 0)
	a.blockMap = make(map[uintptr]*memory.Block)
	a.currentAddr = a.baseAddr
	a.nextID = 0
	a.metrics.Reset()
}

// Name returns the allocator name
func (a *ArenaAllocator) Name() string { return a.name }

// TotalSize returns total arena size
func (a *ArenaAllocator) TotalSize() int { return a.totalSize }

// GetArenaStats returns arena-specific statistics
func (a *ArenaAllocator) GetArenaStats() ArenaStats {
	a.mu.RLock()
	defer a.mu.RUnlock()
	used := int(a.currentAddr - a.baseAddr)
	free := a.totalSize - used
	util := 0.0
	if a.totalSize > 0 {
		util = float64(used) / float64(a.totalSize) * 100
	}
	return ArenaStats{
		TotalSize:       a.totalSize,
		UsedSize:        used,
		FreeSize:        free,
		Utilization:     util,
		AllocationCount: len(a.blocks),
		CurrentOffset:   used,
	}
}

// CanAllocate checks if size can be allocated
func (a *ArenaAllocator) CanAllocate(size int) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	endAddr := a.currentAddr + uintptr(size)
	return endAddr <= a.baseAddr+uintptr(a.totalSize)
}

// ArenaStats represents arena allocator statistics
type ArenaStats struct {
	TotalSize       int     `json:"totalSize"`
	UsedSize        int     `json:"usedSize"`
	FreeSize        int     `json:"freeSize"`
	Utilization     float64 `json:"utilization"`
	AllocationCount int     `json:"allocationCount"`
	CurrentOffset   int     `json:"currentOffset"`
}

// Compile-time interface compliance
var _ allocator.Allocator = (*ArenaAllocator)(nil)
