package allocator

import (
	"time"

	"github.com/sanskar/memory-allocator/internal/memory"
)

// FirstFitAllocator implements First-Fit allocation strategy
type FirstFitAllocator struct {
	*BaseAllocator
}

// NewFirstFitAllocator creates a new First-Fit allocator
func NewFirstFitAllocator(size int) *FirstFitAllocator {
	return &FirstFitAllocator{
		BaseAllocator: NewBaseAllocator("First-Fit", size),
	}
}

// Allocate allocates memory using First-Fit strategy
func (a *FirstFitAllocator) Allocate(size int, owner string) (*memory.Block, error) {
	start := time.Now()
	if err := validateSize(size); err != nil {
		a.metrics.RecordFailedAllocation()
		return nil, err
	}

	a.mu.Lock()
	current := a.blocks.HeadLocked()
	for current != nil {
		if current.IsFree() && current.Size >= size {
			block := a.splitBlock(current, size)
			block.Allocate(owner)
			a.blockMap[block.Address] = block
			clone := block.Clone()
			a.mu.Unlock()
			a.metrics.RecordAllocation(size, time.Since(start))
			return clone, nil
		}
		current = current.Next()
	}
	a.mu.Unlock()
	a.metrics.RecordFailedAllocation()
	return nil, ErrOutOfMemory
}

// Deallocate frees a previously allocated block and coalesces with neighbours
func (a *FirstFitAllocator) Deallocate(address uintptr) error {
	start := time.Now()
	a.mu.Lock()
	block, ok := a.blockMap[address]
	if !ok {
		a.mu.Unlock()
		return ErrBlockNotFound
	}
	if block.IsFree() {
		a.mu.Unlock()
		return ErrAlreadyFreed
	}

	size := block.Size
	block.Free()
	delete(a.blockMap, address)
	a.coalesceAtLocked(block)
	a.mu.Unlock()

	a.metrics.RecordDeallocation(size, time.Since(start))
	return nil
}

// coalesceAtLocked merges block with any adjacent free blocks already in the
// list. Caller must hold a.mu.
func (a *FirstFitAllocator) coalesceAtLocked(block *memory.Block) {
	// Merge with next
	if next, ok := a.blockMap[block.EndAddress()]; ok && next.IsFree() {
		block.Size += next.Size
		a.blocks.Remove(next)
		delete(a.blockMap, next.Address)
	}
	// Merge with previous
	if block.Previous() != nil && block.Previous().IsFree() {
		prev := block.Previous()
		prev.Size += block.Size
		a.blocks.Remove(block)
		delete(a.blockMap, block.Address)
	}
}

// BestFitAllocator implements Best-Fit allocation strategy
type BestFitAllocator struct {
	*BaseAllocator
}

// NewBestFitAllocator creates a new Best-Fit allocator
func NewBestFitAllocator(size int) *BestFitAllocator {
	return &BestFitAllocator{
		BaseAllocator: NewBaseAllocator("Best-Fit", size),
	}
}

// Allocate allocates memory using Best-Fit strategy
func (a *BestFitAllocator) Allocate(size int, owner string) (*memory.Block, error) {
	start := time.Now()
	if err := validateSize(size); err != nil {
		a.metrics.RecordFailedAllocation()
		return nil, err
	}

	a.mu.Lock()
	better := func(candidate, current *memory.Block) bool {
		return candidate.Size < current.Size
	}
	best := a.findFreeBlockByPolicy(size, better)
	if best == nil {
		a.mu.Unlock()
		a.metrics.RecordFailedAllocation()
		return nil, ErrOutOfMemory
	}
	block := a.splitBlock(best, size)
	block.Allocate(owner)
	a.blockMap[block.Address] = block
	clone := block.Clone()
	a.mu.Unlock()
	a.metrics.RecordAllocation(size, time.Since(start))
	return clone, nil
}

// Deallocate frees a previously allocated block
func (a *BestFitAllocator) Deallocate(address uintptr) error {
	start := time.Now()
	a.mu.Lock()
	block, ok := a.blockMap[address]
	if !ok {
		a.mu.Unlock()
		return ErrBlockNotFound
	}
	if block.IsFree() {
		a.mu.Unlock()
		return ErrAlreadyFreed
	}
	size := block.Size
	block.Free()
	delete(a.blockMap, address)
	a.coalesceAtLocked(block)
	a.mu.Unlock()
	a.metrics.RecordDeallocation(size, time.Since(start))
	return nil
}

func (a *BestFitAllocator) coalesceAtLocked(block *memory.Block) {
	if next, ok := a.blockMap[block.EndAddress()]; ok && next.IsFree() {
		block.Size += next.Size
		a.blocks.Remove(next)
		delete(a.blockMap, next.Address)
	}
	if block.Previous() != nil && block.Previous().IsFree() {
		prev := block.Previous()
		prev.Size += block.Size
		a.blocks.Remove(block)
		delete(a.blockMap, block.Address)
	}
}

// WorstFitAllocator implements Worst-Fit allocation strategy
type WorstFitAllocator struct {
	*BaseAllocator
}

// NewWorstFitAllocator creates a new Worst-Fit allocator
func NewWorstFitAllocator(size int) *WorstFitAllocator {
	return &WorstFitAllocator{
		BaseAllocator: NewBaseAllocator("Worst-Fit", size),
	}
}

// Allocate allocates memory using Worst-Fit strategy
func (a *WorstFitAllocator) Allocate(size int, owner string) (*memory.Block, error) {
	start := time.Now()
	if err := validateSize(size); err != nil {
		a.metrics.RecordFailedAllocation()
		return nil, err
	}

	a.mu.Lock()
	better := func(candidate, current *memory.Block) bool {
		return candidate.Size > current.Size
	}
	worst := a.findFreeBlockByPolicy(size, better)
	if worst == nil {
		a.mu.Unlock()
		a.metrics.RecordFailedAllocation()
		return nil, ErrOutOfMemory
	}
	block := a.splitBlock(worst, size)
	block.Allocate(owner)
	a.blockMap[block.Address] = block
	clone := block.Clone()
	a.mu.Unlock()
	a.metrics.RecordAllocation(size, time.Since(start))
	return clone, nil
}

// Deallocate frees a previously allocated block
func (a *WorstFitAllocator) Deallocate(address uintptr) error {
	start := time.Now()
	a.mu.Lock()
	block, ok := a.blockMap[address]
	if !ok {
		a.mu.Unlock()
		return ErrBlockNotFound
	}
	if block.IsFree() {
		a.mu.Unlock()
		return ErrAlreadyFreed
	}
	size := block.Size
	block.Free()
	delete(a.blockMap, address)
	a.coalesceAtLocked(block)
	a.mu.Unlock()
	a.metrics.RecordDeallocation(size, time.Since(start))
	return nil
}

func (a *WorstFitAllocator) coalesceAtLocked(block *memory.Block) {
	if next, ok := a.blockMap[block.EndAddress()]; ok && next.IsFree() {
		block.Size += next.Size
		a.blocks.Remove(next)
		delete(a.blockMap, next.Address)
	}
	if block.Previous() != nil && block.Previous().IsFree() {
		prev := block.Previous()
		prev.Size += block.Size
		a.blocks.Remove(block)
		delete(a.blockMap, block.Address)
	}
}
