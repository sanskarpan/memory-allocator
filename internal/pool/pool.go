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
	ErrPoolExhausted = errors.New("pool exhausted")
	ErrInvalidBlock  = errors.New("invalid block")
	ErrPoolSize      = errors.New("invalid size for pool allocator")
)

// PoolAllocator implements fixed-size block allocation (Object Pool pattern).
// All blocks have the same Size, and Allocate always returns a block of
// exactly blockSize bytes regardless of the requested size.
type PoolAllocator struct {
	name       string
	blockSize  int
	blockCount int
	baseAddr   uintptr
	freeBlocks []*memory.Block // stack of free blocks
	usedBlocks map[uintptr]*memory.Block
	metrics    *metrics.AllocationMetrics
	nextID     int
	mu         sync.RWMutex
}

// NewPoolAllocator creates a new pool allocator
func NewPoolAllocator(blockSize, blockCount int) *PoolAllocator {
	if blockSize <= 0 || blockCount <= 0 {
		panic("pool allocator requires blockSize > 0 and blockCount > 0")
	}
	baseAddr := uintptr(0x2000)
	pool := &PoolAllocator{
		name:       "Pool Allocator",
		blockSize:  blockSize,
		blockCount: blockCount,
		baseAddr:   baseAddr,
		freeBlocks: make([]*memory.Block, 0, blockCount),
		usedBlocks: make(map[uintptr]*memory.Block, blockCount),
		metrics:    metrics.NewAllocationMetrics(),
	}
	for i := 0; i < blockCount; i++ {
		addr := baseAddr + uintptr(i*blockSize)
		block := memory.NewBlock(pool.nextID, addr, blockSize)
		pool.nextID++
		pool.freeBlocks = append(pool.freeBlocks, block)
	}
	return pool
}

// Allocate allocates a fixed-size block from the pool. The requested size is
// honored only as long as it fits the block; an error is returned for sizes
// larger than the configured block size.
func (p *PoolAllocator) Allocate(size int, owner string) (*memory.Block, error) {
	start := time.Now()
	var allocErr error
	defer func() {
		if allocErr != nil {
			p.metrics.RecordFailedAllocation()
		} else {
			p.metrics.RecordAllocation(p.blockSize, time.Since(start))
		}
	}()

	if size <= 0 {
		allocErr = ErrPoolSize
		return nil, ErrPoolSize
	}
	if size > p.blockSize {
		allocErr = ErrPoolSize
		return nil, ErrPoolSize
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.freeBlocks) == 0 {
		allocErr = ErrPoolExhausted
		return nil, ErrPoolExhausted
	}

	// Pop from end (stack)
	idx := len(p.freeBlocks) - 1
	block := p.freeBlocks[idx]
	p.freeBlocks = p.freeBlocks[:idx]
	block.Allocate(owner)
	p.usedBlocks[block.Address] = block
	return block.Clone(), nil
}

// Deallocate returns a block to the pool
func (p *PoolAllocator) Deallocate(address uintptr) error {
	start := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()

	block, ok := p.usedBlocks[address]
	if !ok {
		return ErrInvalidBlock
	}

	size := block.Size
	block.Free()
	delete(p.usedBlocks, address)
	p.freeBlocks = append(p.freeBlocks, block)

	p.metrics.RecordDeallocation(size, time.Since(start))
	return nil
}

// GetBlock retrieves a block by address (allocated only)
func (p *PoolAllocator) GetBlock(address uintptr) (*memory.Block, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	block, ok := p.usedBlocks[address]
	if !ok {
		return nil, ErrInvalidBlock
	}
	return block.Clone(), nil
}

// GetAllBlocks returns all blocks (free + used), sorted by address
func (p *PoolAllocator) GetAllBlocks() []*memory.Block {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := make([]*memory.Block, 0, p.blockCount)
	// Used blocks
	for _, b := range p.usedBlocks {
		out = append(out, b.Clone())
	}
	// Free blocks
	for _, b := range p.freeBlocks {
		out = append(out, b.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// GetFreeBlocks returns all free blocks sorted by address
func (p *PoolAllocator) GetFreeBlocks() []*memory.Block {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := make([]*memory.Block, len(p.freeBlocks))
	for i, b := range p.freeBlocks {
		out[i] = b.Clone()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// GetAllocatedBlocks returns all allocated blocks sorted by address
func (p *PoolAllocator) GetAllocatedBlocks() []*memory.Block {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := make([]*memory.Block, 0, len(p.usedBlocks))
	for _, b := range p.usedBlocks {
		out = append(out, b.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// GetMetrics returns allocation metrics
func (p *PoolAllocator) GetMetrics() metrics.MetricsSnapshot {
	return p.metrics.GetSnapshot()
}

// CalculateFragmentation returns 0 for pool (no fragmentation)
func (p *PoolAllocator) CalculateFragmentation() float64 {
	return 0.0
}

// Coalesce returns 0 for pool (no coalescing needed)
func (p *PoolAllocator) Coalesce() int { return 0 }

// Reset resets the pool to initial state
func (p *PoolAllocator) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.usedBlocks = make(map[uintptr]*memory.Block, p.blockCount)
	p.freeBlocks = make([]*memory.Block, 0, p.blockCount)
	p.nextID = 0
	for i := 0; i < p.blockCount; i++ {
		addr := p.baseAddr + uintptr(i*p.blockSize)
		block := memory.NewBlock(p.nextID, addr, p.blockSize)
		p.nextID++
		p.freeBlocks = append(p.freeBlocks, block)
	}
	p.metrics.Reset()
}

// Name returns the allocator name
func (p *PoolAllocator) Name() string { return p.name }

// TotalSize returns total pool size
func (p *PoolAllocator) TotalSize() int { return p.blockSize * p.blockCount }

// GetPoolStats returns pool-specific statistics
func (p *PoolAllocator) GetPoolStats() PoolStats {
	p.mu.RLock()
	defer p.mu.RUnlock()
	util := 0.0
	if p.blockCount > 0 {
		util = float64(len(p.usedBlocks)) / float64(p.blockCount) * 100
	}
	return PoolStats{
		BlockSize:   p.blockSize,
		TotalBlocks: p.blockCount,
		FreeBlocks:  len(p.freeBlocks),
		UsedBlocks:  len(p.usedBlocks),
		Utilization: util,
	}
}

// PoolStats represents pool allocator statistics
type PoolStats struct {
	BlockSize   int     `json:"blockSize"`
	TotalBlocks int     `json:"totalBlocks"`
	FreeBlocks  int     `json:"freeBlocks"`
	UsedBlocks  int     `json:"usedBlocks"`
	Utilization float64 `json:"utilization"`
}

// Compile-time interface compliance
var _ allocator.Allocator = (*PoolAllocator)(nil)
