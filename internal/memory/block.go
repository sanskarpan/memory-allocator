package memory

import (
	"fmt"
	"sync"
	"time"
)

// BlockState represents the current state of a memory block
type BlockState int

const (
	StateFree BlockState = iota
	StateAllocated
	StateReserved
)

func (s BlockState) String() string {
	switch s {
	case StateFree:
		return "Free"
	case StateAllocated:
		return "Allocated"
	case StateReserved:
		return "Reserved"
	default:
		return "Unknown"
	}
}

// Block represents a memory block
type Block struct {
	ID           int        `json:"id"`
	Address      uintptr    `json:"address"`
	Size         int        `json:"size"`
	State        BlockState `json:"state"`
	AllocatedAt  time.Time  `json:"allocatedAt"`
	FreedAt      time.Time  `json:"freedAt"`
	Owner        string     `json:"owner"`
	Alignment    int        `json:"alignment"`
	BuddyAddress uintptr    `json:"buddyAddress"`
	Level        int        `json:"level"`
	Color        string     `json:"color"`
	AccessCount  int        `json:"accessCount"`

	// Internal linked-list pointers. Unexported so they aren't serialized.
	previous *Block
	next     *Block
}

// NewBlock creates a new memory block
func NewBlock(id int, address uintptr, size int) *Block {
	return &Block{
		ID:        id,
		Address:   address,
		Size:      size,
		State:     StateFree,
		Alignment: 1,
		Color:     generateColor(id),
	}
}

// Clone creates a deep copy of the block
func (b *Block) Clone() *Block {
	return &Block{
		ID:           b.ID,
		Address:      b.Address,
		Size:         b.Size,
		State:        b.State,
		AllocatedAt:  b.AllocatedAt,
		FreedAt:      b.FreedAt,
		Owner:        b.Owner,
		Alignment:    b.Alignment,
		BuddyAddress: b.BuddyAddress,
		Level:        b.Level,
		Color:        b.Color,
		AccessCount:  b.AccessCount,
	}
}

// Allocate marks the block as allocated
func (b *Block) Allocate(owner string) {
	b.State = StateAllocated
	b.Owner = owner
	b.AllocatedAt = time.Now()
	b.AccessCount = 0
}

// Free marks the block as free
func (b *Block) Free() {
	b.State = StateFree
	b.FreedAt = time.Now()
}

// Access increments the access counter
func (b *Block) Access() {
	b.AccessCount++
}

// IsFree checks if the block is free
func (b *Block) IsFree() bool {
	return b.State == StateFree
}

// IsAllocated checks if the block is allocated
func (b *Block) IsAllocated() bool {
	return b.State == StateAllocated
}

// EndAddress returns the ending address of the block
func (b *Block) EndAddress() uintptr {
	return b.Address + uintptr(b.Size)
}

// Next returns the next block in the linked list, or nil. Useful for
// allocators that hold the list mutex externally and need to walk the
// list without copying it.
func (b *Block) Next() *Block { return b.next }

// Previous returns the previous block in the linked list, or nil.
func (b *Block) Previous() *Block { return b.previous }

// CanMerge checks if this block can merge with another
func (b *Block) CanMerge(other *Block) bool {
	if b == nil || other == nil {
		return false
	}

	if !b.IsFree() || !other.IsFree() {
		return false
	}

	return b.EndAddress() == other.Address || other.EndAddress() == b.Address
}

// String returns a string representation
func (b *Block) String() string {
	return fmt.Sprintf("Block[ID=%d Addr=0x%x Size=%d State=%s Owner=%s]",
		b.ID, b.Address, b.Size, b.State, b.Owner)
}

// BlockList is a doubly-linked list of blocks.
//
// The list is intended to be used by a single owner (the allocator that
// created it) that already holds its own mutex when accessing the list. As
// such, the list does NOT lock its internal `head`/`tail` pointers — that
// is the caller's responsibility. The exported `Add`, `Remove`, and
// `InsertAfter` methods are convenience wrappers that still serialise
// against the rare case of an external caller, but allocators should use
// the `*Locked` variants while holding the allocator's own mutex to avoid
// a double-lock and the resulting lock-ordering complexity.
//
// All exported methods that return block pointers return *clones* unless
// documented otherwise, so the caller cannot accidentally mutate the
// allocator's internal state.
type BlockList struct {
	mu    sync.Mutex
	head  *Block
	tail  *Block
	count int
}

// NewBlockList creates a new block list
func NewBlockList() *BlockList {
	return &BlockList{}
}

// Lock acquires the list mutex. Use when iterating and mutating concurrently
// with other list operations. Must be paired with Unlock.
func (bl *BlockList) Lock()   { bl.mu.Lock() }
func (bl *BlockList) Unlock() { bl.mu.Unlock() }

// Add appends a block to the end of the list.
func (bl *BlockList) Add(block *Block) {
	bl.mu.Lock()
	defer bl.mu.Unlock()
	bl.addLocked(block)
}

func (bl *BlockList) addLocked(block *Block) {
	block.previous = bl.tail
	block.next = nil
	if bl.tail != nil {
		bl.tail.next = block
	} else {
		bl.head = block
	}
	bl.tail = block
	bl.count++
}

// InsertAfter inserts newBlock immediately after existing. existing must be in
// the list.
func (bl *BlockList) InsertAfter(existing, newBlock *Block) {
	bl.mu.Lock()
	defer bl.mu.Unlock()
	bl.insertAfterLocked(existing, newBlock)
}

func (bl *BlockList) insertAfterLocked(existing, newBlock *Block) {
	newBlock.previous = existing
	newBlock.next = existing.next
	if existing.next != nil {
		existing.next.previous = newBlock
	} else {
		bl.tail = newBlock
	}
	existing.next = newBlock
	bl.count++
}

// Remove removes a block from the list. The caller is responsible for
// ensuring the block is currently a member of the list.
func (bl *BlockList) Remove(block *Block) {
	bl.mu.Lock()
	defer bl.mu.Unlock()
	bl.removeLocked(block)
}

func (bl *BlockList) removeLocked(block *Block) {
	if block.previous != nil {
		block.previous.next = block.next
	} else {
		bl.head = block.next
	}
	if block.next != nil {
		block.next.previous = block.previous
	} else {
		bl.tail = block.previous
	}
	block.previous = nil
	block.next = nil
	bl.count--
}

// Head returns a snapshot of the head block, or nil if empty.
//
// The returned pointer is the live head block; the caller must hold the
// BlockList mutex (or an external allocator mutex that serialises list
// access) for as long as it dereferences the pointer. Allocators should
// use HeadLocked instead, which is the documented fast path.
func (bl *BlockList) Head() *Block {
	bl.mu.Lock()
	defer bl.mu.Unlock()
	return bl.head
}

// HeadLocked returns the raw head pointer. Caller MUST serialise access to
// the list (typically by holding the owning allocator's write lock) for
// as long as the returned pointer is dereferenced.
func (bl *BlockList) HeadLocked() *Block {
	return bl.head
}

// GetBlocks returns a snapshot of all blocks in order. Each block in the
// returned slice is a clone and may be safely held by the caller.
func (bl *BlockList) GetBlocks() []*Block {
	bl.mu.Lock()
	defer bl.mu.Unlock()
	blocks := make([]*Block, 0, bl.count)
	current := bl.head
	for current != nil {
		blocks = append(blocks, current.Clone())
		current = current.next
	}
	return blocks
}

// Count returns the number of blocks in the list.
func (bl *BlockList) Count() int {
	bl.mu.Lock()
	defer bl.mu.Unlock()
	return bl.count
}

// generateColor generates a unique color for visualization
func generateColor(id int) string {
	colors := []string{
		"#FF6B6B", "#4ECDC4", "#45B7D1", "#FFA07A", "#98D8C8",
		"#F7DC6F", "#BB8FCE", "#85C1E2", "#F8B739", "#52B788",
		"#E63946", "#A8DADC", "#F77F00", "#06FFA5", "#FF9E00",
		"#9B59B6", "#3498DB", "#E74C3C", "#2ECC71", "#F39C12",
	}
	if id < 0 {
		id = -id
	}
	return colors[id%len(colors)]
}

// StateReserved is reserved for future use. Allocators that wish to mark
// in-flight allocations (e.g. during a split) can transition through
// StateReserved to make ownership clearer in traces.
var _ = StateReserved // keep const available; not currently used
