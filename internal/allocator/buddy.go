package allocator

import (
	"time"

	"github.com/sanskar/memory-allocator/internal/memory"
	"github.com/sanskar/memory-allocator/internal/metrics"
)

// BuddyAllocator implements the binary buddy system.
//
// Invariants:
//   - Memory is laid out as a power-of-two range starting at ba.baseAddr.
//   - A block at level l has size = minSize << l, where minSize is the
//     smallest allocatable size (level 0).
//   - The doubly-linked list in ba.blocks is the single source of truth
//     for visualization. It contains all currently-allocated blocks plus
//     one free block per level that has any free buddies.
//   - freeLists[level] is the set of addresses of free blocks at that level
//     (sourced from the linked list).
//
// The buddy allocator does NOT use BaseAllocator.Coalesce / splitBlock.
// It manages its own free-lists and updates ba.blocks directly while
// holding ba.mu.
type BuddyAllocator struct {
	*BaseAllocator
	minSize   int
	maxLevel  int
	freeLists map[int][]uintptr // level -> list of free addresses
}

// NewBuddyAllocator creates a new Buddy System allocator
func NewBuddyAllocator(size int) *BuddyAllocator {
	if size <= 0 {
		size = 64
	}
	totalSize := nextPowerOf2(size)
	const minSize = 64
	maxLevel := 0
	for (minSize << uint(maxLevel)) < totalSize {
		maxLevel++
	}
	// Trim down so minSize<<maxLevel is the smallest power of two >= totalSize
	for maxLevel > 0 && (minSize<<uint(maxLevel-1)) >= totalSize {
		maxLevel--
	}

	base := NewBaseAllocator("Buddy System", totalSize)
	// The base allocator already created an initial free block. Use it.
	ba := &BuddyAllocator{
		BaseAllocator: base,
		minSize:       minSize,
		maxLevel:      maxLevel,
		freeLists:     make(map[int][]uintptr),
	}
	for i := 0; i <= maxLevel; i++ {
		ba.freeLists[i] = make([]uintptr, 0)
	}
	// Mark the existing initial block as the top-level free block. This is
	// the only place where it's safe to call Head() without an external
	// allocator lock: nothing else holds a reference to `ba` yet.
	head := base.blocks.Head()
	if head != nil {
		head.Level = maxLevel
		ba.freeLists[maxLevel] = append(ba.freeLists[maxLevel], head.Address)
	}
	return ba
}

// blockSizeFor returns the byte size for a given level.
func (a *BuddyAllocator) blockSizeFor(level int) int {
	return a.minSize << uint(level)
}

// levelFor returns the buddy level for a requested size, or an error.
func (a *BuddyAllocator) levelFor(size int) (int, error) {
	if size <= 0 {
		return -1, ErrInvalidSize
	}
	if size <= a.minSize {
		return 0, nil
	}
	if size&(size-1) != 0 {
		size = nextPowerOf2(size)
	}
	level := 0
	for (a.minSize << uint(level)) < size {
		level++
	}
	if level > a.maxLevel {
		return -1, ErrOutOfMemory
	}
	return level, nil
}

// Allocate allocates memory using Buddy System
func (a *BuddyAllocator) Allocate(size int, owner string) (*memory.Block, error) {
	start := time.Now()
	level, err := a.levelFor(size)
	if err != nil {
		a.metrics.RecordFailedAllocation()
		return nil, err
	}
	allocSize := a.blockSizeFor(level)

	a.mu.Lock()
	blk, ok := a.findAndPrepareBlockLocked(level)
	if !ok {
		a.mu.Unlock()
		a.metrics.RecordFailedAllocation()
		return nil, ErrOutOfMemory
	}
	blk.Allocate(owner)
	blk.Level = level
	blk.Size = allocSize
	a.blockMap[blk.Address] = blk
	clone := blk.Clone()
	a.mu.Unlock()

	a.metrics.RecordAllocation(allocSize, time.Since(start))
	return clone, nil
}

// findAndPrepareBlockLocked finds a free block at the requested level,
// splits larger blocks down to that level, and returns the block ready to be
// marked allocated. The returned block remains a member of the linked list
// (its state is still Free until the caller marks it allocated).
// Caller must hold a.mu.
func (a *BuddyAllocator) findAndPrepareBlockLocked(level int) (*memory.Block, bool) {
	if level > a.maxLevel {
		return nil, false
	}
	// Try exact level first
	if addr, ok := a.popFreeAddrLocked(level); ok {
		if blk, ok := a.findBlockInListLocked(addr); ok {
			return blk, true
		}
		return nil, false
	}
	// Try splitting from a higher level
	for i := level + 1; i <= a.maxLevel; i++ {
		addr, ok := a.popFreeAddrLocked(i)
		if !ok {
			continue
		}
		blk, ok := a.findBlockInListLocked(addr)
		if !ok {
			// Free list out of sync with linked list. This should not
			// happen during normal operation; bail out gracefully.
			return nil, false
		}
		// Resize blk to its actual level, then split down to the target.
		blk.Size = a.blockSizeFor(i)
		blk.Level = i
		a.splitDownLocked(blk, i, level)
		return blk, true
	}
	return nil, false
}

// splitDownLocked splits a free block at startLevel down to targetLevel,
// inserting the new free buddies in the linked list and free lists.
// `blk` is the lower-addressed half after the split, with its level/size
// set to targetLevel. Caller must hold a.mu.
func (a *BuddyAllocator) splitDownLocked(blk *memory.Block, startLevel, targetLevel int) {
	for curLevel := startLevel; curLevel > targetLevel; curLevel-- {
		newLevel := curLevel - 1
		newSize := a.blockSizeFor(newLevel)
		buddyAddr := blk.Address + uintptr(newSize)
		buddy := memory.NewBlock(a.nextID, buddyAddr, newSize)
		a.nextID++
		buddy.Level = newLevel
		buddy.BuddyAddress = blk.Address
		blk.Level = newLevel
		blk.Size = newSize
		blk.BuddyAddress = buddyAddr
		// Insert buddy as free in the linked list (right after blk)
		a.blocks.InsertAfter(blk, buddy)
		a.freeLists[newLevel] = append(a.freeLists[newLevel], buddyAddr)
	}
}

// popFreeAddrLocked removes the first free address at the given level from
// the free list. Caller must hold a.mu.
func (a *BuddyAllocator) popFreeAddrLocked(level int) (uintptr, bool) {
	list := a.freeLists[level]
	if len(list) == 0 {
		return 0, false
	}
	addr := list[0]
	a.freeLists[level] = list[1:]
	return addr, true
}

// findBlockInListLocked walks the linked list for the block at the given
// address. Caller must hold a.mu.
func (a *BuddyAllocator) findBlockInListLocked(addr uintptr) (*memory.Block, bool) {
	cur := a.blocks.HeadLocked()
	for cur != nil {
		if cur.Address == addr {
			return cur, true
		}
		cur = cur.Next()
	}
	return nil, false
}

// Deallocate frees a block and merges with its buddy if possible
func (a *BuddyAllocator) Deallocate(address uintptr) error {
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
	level := block.Level
	delete(a.blockMap, address)
	a.mergeBuddiesLocked(block, level)
	a.mu.Unlock()

	a.metrics.RecordDeallocation(size, time.Since(start))
	return nil
}

// mergeBuddiesLocked tries to merge `block` with its buddy at the given level,
// recursively bubbling up. Caller must hold a.mu.
//
// Invariant preserved: the linked list remains sorted by address. The
// merged block occupies the position previously held by whichever of
// (block, buddy) had the lower address.
func (a *BuddyAllocator) mergeBuddiesLocked(block *memory.Block, level int) {
	if level >= a.maxLevel {
		a.insertFreeLocked(block, level)
		return
	}

	buddyAddr := a.calculateBuddyAddress(block.Address, level)
	if !a.removeAddrFromFreeListLocked(buddyAddr, level) {
		// Buddy is not free — keep `block` as a free block at this level.
		a.insertFreeLocked(block, level)
		return
	}

	buddy, ok := a.findBlockInListLocked(buddyAddr)
	if !ok {
		// Free list and linked list are out of sync. Bail out by keeping
		// `block` as a free block at this level.
		a.insertFreeLocked(block, level)
		return
	}

	// Decide which physical block will become the merged block. It must
	// be the one with the lower address so the list stays sorted.
	var merged *memory.Block
	if block.Address < buddyAddr {
		merged = block
		a.blocks.Remove(buddy)
	} else {
		merged = buddy
		a.blocks.Remove(block)
	}
	merged.Level = level + 1
	merged.Size = a.blockSizeFor(level + 1)
	merged.BuddyAddress = 0
	merged.State = memory.StateFree
	merged.Owner = ""

	a.mergeBuddiesLocked(merged, level+1)
}

// insertFreeLocked places the block back into the free list and updates its
// state. Caller must hold a.mu.
func (a *BuddyAllocator) insertFreeLocked(block *memory.Block, level int) {
	block.Level = level
	block.Size = a.blockSizeFor(level)
	block.Free()
	a.freeLists[level] = append(a.freeLists[level], block.Address)
}

func (a *BuddyAllocator) removeAddrFromFreeListLocked(addr uintptr, level int) bool {
	list := a.freeLists[level]
	for i, candidate := range list {
		if candidate == addr {
			a.freeLists[level] = append(list[:i], list[i+1:]...)
			return true
		}
	}
	return false
}

// calculateBuddyAddress returns the address of the block's buddy at the given
// level. Buddies differ by XOR of their block size.
func (a *BuddyAllocator) calculateBuddyAddress(addr uintptr, level int) uintptr {
	blockSize := uintptr(a.blockSizeFor(level))
	offset := addr - a.BaseAllocator.baseAddr
	return a.BaseAllocator.baseAddr + (offset ^ blockSize)
}

// Reset resets the buddy allocator. Caller must NOT hold a.mu.
func (a *BuddyAllocator) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resetLocked()
}

// resetLocked performs the reset work assuming a.mu is already held. Since
// BaseAllocator.Reset tries to acquire a.mu internally, we cannot call it
// from BuddyAllocator.Reset. Instead, we reset the embedded state directly.
func (a *BuddyAllocator) resetLocked() {
	// Reset base allocator state without re-locking.
	a.blocks = memory.NewBlockList()
	initial := memory.NewBlock(0, a.baseAddr, a.totalSize)
	a.blocks.Add(initial)
	a.blockMap = make(map[uintptr]*memory.Block)
	a.metrics.Reset()
	a.nextID = 1

	// Reset buddy free lists.
	a.freeLists = make(map[int][]uintptr)
	for i := 0; i <= a.maxLevel; i++ {
		a.freeLists[i] = make([]uintptr, 0)
	}
	head := a.blocks.HeadLocked()
	if head != nil {
		head.Level = a.maxLevel
		a.freeLists[a.maxLevel] = append(a.freeLists[a.maxLevel], head.Address)
	}
}

// GetAllBlocks returns all blocks in the linked list. Caller-visible.
func (a *BuddyAllocator) GetAllBlocks() []*memory.Block {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.blocks.GetBlocks()
}

// GetFreeBlocks returns free blocks
func (a *BuddyAllocator) GetFreeBlocks() []*memory.Block {
	all := a.GetAllBlocks()
	out := make([]*memory.Block, 0, len(all))
	for _, b := range all {
		if b.IsFree() {
			out = append(out, b)
		}
	}
	return out
}

// GetAllocatedBlocks returns allocated blocks
func (a *BuddyAllocator) GetAllocatedBlocks() []*memory.Block {
	all := a.GetAllBlocks()
	out := make([]*memory.Block, 0, len(all))
	for _, b := range all {
		if b.IsAllocated() {
			out = append(out, b)
		}
	}
	return out
}

// CalculateFragmentation: percent of free memory that is NOT in the largest
// free block. Returns 0 if no free memory.
func (a *BuddyAllocator) CalculateFragmentation() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	a.calculateFragmentationLocked()
	return a.metrics.GetSnapshot().Fragmentation
}

func (a *BuddyAllocator) calculateFragmentationLocked() {
	all := a.blocks.GetBlocks()
	var total, largest int
	for _, b := range all {
		if b.IsFree() {
			total += b.Size
			if b.Size > largest {
				largest = b.Size
			}
		}
	}
	if total == 0 {
		a.metrics.UpdateFragmentation(0)
		return
	}
	frag := (1.0 - float64(largest)/float64(total)) * 100.0
	a.metrics.UpdateFragmentation(frag)
}

// Coalesce is essentially a no-op for the buddy allocator; merges happen
// automatically on deallocation. Recalculate fragmentation for current
// metrics.
func (a *BuddyAllocator) Coalesce() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calculateFragmentationLocked()
	return 0
}

// nextPowerOf2 returns the next power of 2 >= n (with n>=1). For n<=0 it
// returns 1.
func nextPowerOf2(n int) int {
	if n <= 0 {
		return 1
	}
	if n&(n-1) == 0 {
		return n
	}
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// GetFreeListSummary returns counts of free blocks per level, sorted by
// ascending level (smallest blocks first).
func (a *BuddyAllocator) GetFreeListSummary() map[int]int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	summary := make(map[int]int)
	for level, addrs := range a.freeLists {
		summary[level] = len(addrs)
	}
	return summary
}

// Ensure interface compliance (compile-time check).
var _ Allocator = (*BuddyAllocator)(nil)
var _ Allocator = (*FirstFitAllocator)(nil)
var _ Allocator = (*BestFitAllocator)(nil)
var _ Allocator = (*WorstFitAllocator)(nil)
var _ = metrics.AllocationMetrics{}
