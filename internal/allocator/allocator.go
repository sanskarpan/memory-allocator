package allocator

import (
	"errors"
	"fmt"
	"sync"

	"github.com/sanskar/memory-allocator/internal/memory"
	"github.com/sanskar/memory-allocator/internal/metrics"
)

var (
	ErrOutOfMemory      = errors.New("out of memory")
	ErrInvalidSize      = errors.New("invalid size")
	ErrBlockNotFound    = errors.New("block not found")
	ErrInvalidAddress   = errors.New("invalid address")
	ErrAlreadyFreed     = errors.New("block already freed")
	ErrInvalidAlignment = errors.New("invalid alignment")
	ErrUnsupported      = errors.New("operation not supported by this allocator")
)

// Allocator defines the interface for memory allocators
type Allocator interface {
	// Allocate allocates a block of memory
	Allocate(size int, owner string) (*memory.Block, error)

	// Deallocate frees a previously allocated block
	Deallocate(address uintptr) error

	// GetBlock retrieves a block by address
	GetBlock(address uintptr) (*memory.Block, error)

	// GetAllBlocks returns all blocks
	GetAllBlocks() []*memory.Block

	// GetFreeBlocks returns all free blocks
	GetFreeBlocks() []*memory.Block

	// GetAllocatedBlocks returns all allocated blocks
	GetAllocatedBlocks() []*memory.Block

	// GetMetrics returns allocation metrics
	GetMetrics() metrics.MetricsSnapshot

	// CalculateFragmentation calculates external fragmentation
	CalculateFragmentation() float64

	// Coalesce merges adjacent free blocks
	Coalesce() int

	// Reset resets the allocator to initial state
	Reset()

	// Name returns the allocator name
	Name() string

	// TotalSize returns total memory size
	TotalSize() int
}

// BaseAllocator provides common functionality for all link-list-based
// allocators (first/best/worst fit). It is *not* safe to embed into
// allocators that maintain their own free structure (e.g. buddy) because
// the Coalesce/splitBlock operations would interfere with the buddy tree.
//
// All exported methods acquire ba.mu as appropriate. Subclasses that
// implement custom logic must call into the BaseAllocator while holding
// ba.mu, or coordinate their own locking.
type BaseAllocator struct {
	name      string
	totalSize int
	baseAddr  uintptr
	blocks    *memory.BlockList
	blockMap  map[uintptr]*memory.Block
	nextID    int
	metrics   *metrics.AllocationMetrics
	mu        sync.RWMutex
}

// NewBaseAllocator creates a new base allocator
func NewBaseAllocator(name string, size int) *BaseAllocator {
	baseAddr := uintptr(0x1000) // Start at 0x1000 for simulation

	blocks := memory.NewBlockList()
	initialBlock := memory.NewBlock(0, baseAddr, size)
	blocks.Add(initialBlock)

	return &BaseAllocator{
		name:      name,
		totalSize: size,
		baseAddr:  baseAddr,
		blocks:    blocks,
		blockMap:  make(map[uintptr]*memory.Block),
		nextID:    1,
		metrics:   metrics.NewAllocationMetrics(),
	}
}

// GetMetrics returns current metrics
func (ba *BaseAllocator) GetMetrics() metrics.MetricsSnapshot {
	return ba.metrics.GetSnapshot()
}

// GetBlock retrieves a block by address. The block is cloned so callers may
// not mutate allocator state.
func (ba *BaseAllocator) GetBlock(address uintptr) (*memory.Block, error) {
	ba.mu.RLock()
	defer ba.mu.RUnlock()

	block, ok := ba.blockMap[address]
	if !ok {
		return nil, ErrBlockNotFound
	}
	return block.Clone(), nil
}

// GetAllBlocks returns all blocks in their linked-list order.
func (ba *BaseAllocator) GetAllBlocks() []*memory.Block {
	ba.mu.RLock()
	defer ba.mu.RUnlock()
	return ba.blocks.GetBlocks()
}

// GetFreeBlocks returns all free blocks
func (ba *BaseAllocator) GetFreeBlocks() []*memory.Block {
	ba.mu.RLock()
	defer ba.mu.RUnlock()
	all := ba.blocks.GetBlocks()
	out := make([]*memory.Block, 0, len(all))
	for _, b := range all {
		if b.IsFree() {
			out = append(out, b)
		}
	}
	return out
}

// GetAllocatedBlocks returns all allocated blocks
func (ba *BaseAllocator) GetAllocatedBlocks() []*memory.Block {
	ba.mu.RLock()
	defer ba.mu.RUnlock()
	all := ba.blocks.GetBlocks()
	out := make([]*memory.Block, 0, len(all))
	for _, b := range all {
		if b.IsAllocated() {
			out = append(out, b)
		}
	}
	return out
}

// CalculateFragmentation returns (1 - largest_free / total_free) * 100. Returns
// 0 if there are no free blocks or total free space is 0.
//
// The allocator's RLock is held while reading blocks AND while updating the
// metrics snapshot, so the fragmentation value is always consistent with the
// block list the caller observes. Lock ordering: ba.mu → metrics.mu (never
// reversed), so there is no deadlock risk.
func (ba *BaseAllocator) CalculateFragmentation() float64 {
	ba.mu.RLock()
	defer ba.mu.RUnlock()

	all := ba.blocks.GetBlocks()
	var maxFreeSize, totalFreeSize int
	for _, b := range all {
		if b.IsFree() {
			totalFreeSize += b.Size
			if b.Size > maxFreeSize {
				maxFreeSize = b.Size
			}
		}
	}

	var frag float64
	if totalFreeSize == 0 {
		frag = 0
	} else {
		frag = (1.0 - float64(maxFreeSize)/float64(totalFreeSize)) * 100.0
	}
	ba.metrics.UpdateFragmentation(frag)
	return frag
}

// Coalesce merges adjacent free blocks in the linked list and returns the
// number of merges performed. Caller must NOT hold ba.mu.
//
// The previous implementation iterated over GetBlocks() clones and
// mutated them, which silently lost the size changes back to the real
// blocks. The current implementation walks the real linked list (the
// allocator's source of truth) and merges consecutive free blocks by
// extending `current` and removing the next free block from the list.
// Because each iteration's "current" is the survivor, an arbitrary
// chain of free neighbours reduces to a single block.
func (ba *BaseAllocator) Coalesce() int {
	ba.mu.Lock()
	defer ba.mu.Unlock()

	merged := 0
	current := ba.blocks.HeadLocked()
	for current != nil {
		if !current.IsFree() {
			current = current.Next()
			continue
		}
		// Merge the next free block (in linked-list order) into current.
		// Repeat until current's next neighbour is allocated or there
		// is no next neighbour.
		for {
			nb := current.Next()
			if nb == nil || !nb.IsFree() {
				break
			}
			current.Size += nb.Size
			ba.blocks.Remove(nb)
			delete(ba.blockMap, nb.Address)
			merged++
		}
		current = current.Next()
	}
	return merged
}

// Reset returns the allocator to its initial state.
func (ba *BaseAllocator) Reset() {
	ba.mu.Lock()
	defer ba.mu.Unlock()

	ba.blocks = memory.NewBlockList()
	initialBlock := memory.NewBlock(0, ba.baseAddr, ba.totalSize)
	ba.blocks.Add(initialBlock)

	ba.blockMap = make(map[uintptr]*memory.Block)
	ba.metrics.Reset()
	ba.nextID = 1
}

// Name returns the allocator name
func (ba *BaseAllocator) Name() string {
	return ba.name
}

// TotalSize returns total memory size
func (ba *BaseAllocator) TotalSize() int {
	return ba.totalSize
}

// splitBlock reduces block to exactly `size` bytes and inserts a new free
// block for the remainder. Caller must hold ba.mu (write).
// Returns the (possibly resized) allocated block.
func (ba *BaseAllocator) splitBlock(block *memory.Block, size int) *memory.Block {
	if block.Size == size {
		return block
	}

	remainingSize := block.Size - size
	remainingAddr := block.Address + uintptr(size)
	remainingBlock := memory.NewBlock(ba.nextID, remainingAddr, remainingSize)
	ba.nextID++

	block.Size = size
	ba.blocks.InsertAfter(block, remainingBlock)
	return block
}

// findFreeBlockByPolicy returns the first free block satisfying size using
// the given comparison (used by first/best/worst fit). Caller must hold ba.mu.
func (ba *BaseAllocator) findFreeBlockByPolicy(size int, better func(a, b *memory.Block) bool) *memory.Block {
	var chosen *memory.Block
	current := ba.blocks.HeadLocked()
	for current != nil {
		if current.IsFree() && current.Size >= size {
			if chosen == nil || better(current, chosen) {
				chosen = current
			}
		}
		current = current.Next()
	}
	return chosen
}

// Helper to validate size
func validateSize(size int) error {
	if size <= 0 {
		return ErrInvalidSize
	}
	return nil
}

// String returns a string representation of the allocator
func (ba *BaseAllocator) String() string {
	return fmt.Sprintf("%s [Total: %d bytes, Allocated: %d bytes]",
		ba.name, ba.totalSize, ba.metrics.GetSnapshot().CurrentBytesUsed)
}
