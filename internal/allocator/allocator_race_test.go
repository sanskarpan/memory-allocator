package allocator

import (
	"sync"
	"testing"
)

// TestFirstFit_CoalesceMergesChainOfFreeBlocks exercises the bug where the
// previous Coalesce() implementation mutated block *clones* from
// GetBlocks(), so the second merge looked up the wrong address. With the
// real-list implementation, three free neighbours must collapse to one.
func TestFirstFit_CoalesceMergesChainOfFreeBlocks(t *testing.T) {
	a := NewFirstFitAllocator(1024)
	addrs := make([]uintptr, 0, 5)
	for i := 0; i < 5; i++ {
		b, err := a.Allocate(100, "x")
		if err != nil {
			t.Fatalf("alloc %d: %v", i, err)
		}
		addrs = append(addrs, b.Address)
	}
	// Free all five — auto-coalesce will merge each with its immediate
	// neighbour, leaving at most one free block of the full size.
	for _, addr := range addrs {
		if err := a.Deallocate(addr); err != nil {
			t.Fatalf("dealloc: %v", err)
		}
	}
	// Explicit Coalesce should report 0 merges (auto-coalesce already did
	// the work) but must not break state.
	merged := a.Coalesce()
	if merged != 0 {
		t.Logf("Coalesce reported %d extra merges (auto-coalesce may have been partial)", merged)
	}

	// The full capacity should be available now.
	b, err := a.Allocate(1024, "all")
	if err != nil {
		t.Fatalf("expected full 1024 alloc after chain coalesce, got %v", err)
	}
	if b == nil || b.Size != 1024 {
		t.Errorf("expected size 1024, got %d", b.Size)
	}
}

// TestFirstFit_CoalesceMergesDiscontiguousFrees verifies that Coalesce()
// can merge free blocks whose free neighbours are not consecutive in the
// auto-coalesce order (e.g. A-free-B-free-C, then A and C are freed
// non-adjacently).
func TestFirstFit_CoalesceMergesDiscontiguousFrees(t *testing.T) {
	a := NewFirstFitAllocator(1024)
	b1, _ := a.Allocate(100, "x")
	b2, _ := a.Allocate(100, "x")
	b3, _ := a.Allocate(100, "x")
	b4, _ := a.Allocate(100, "x")

	// Free b1 and b3, leaving b2 and b4 allocated. With auto-coalesce,
	// b1 and b3 each merge with nothing. Now free b2 and b4 — auto-
	// coalesce will merge each with the free neighbour on one side, but
	// may not produce a single contiguous region. Explicit Coalesce
	// should finish the chain.
	a.Deallocate(b1.Address)
	a.Deallocate(b3.Address)
	a.Deallocate(b2.Address)
	a.Deallocate(b4.Address)
	a.Coalesce()

	// Should be able to allocate the full 1024.
	b, err := a.Allocate(1024, "all")
	if err != nil {
		t.Fatalf("expected full alloc after Coalesce, got %v", err)
	}
	if b == nil || b.Size != 1024 {
		t.Errorf("expected 1024, got %d", b.Size)
	}
}

// TestBuddy_MergeAtHigherLevel exercises the case where two buddies at
// level L merge into a level L+1 block, whose buddy at L+1 is also free,
// bubbling all the way up to the top level. The previous implementation
// failed this case by inserting the new merged block at the END of the
// list and creating a new block object instead of reusing one of the
// merged buddies, breaking list order.
func TestBuddy_MergeAtHigherLevel(t *testing.T) {
	a := NewBuddyAllocator(1024)
	// Allocate 16 64-byte blocks (fills the entire 1024-byte heap).
	addrs := make([]uintptr, 0, 16)
	for i := 0; i < 16; i++ {
		b, err := a.Allocate(64, "x")
		if err != nil {
			t.Fatalf("alloc %d: %v", i, err)
		}
		addrs = append(addrs, b.Address)
	}
	// Free all 16. Each pair of buddies should merge to 128, then each
	// pair of 128s to 256, etc., until we have a single 1024 free block.
	for _, addr := range addrs {
		if err := a.Deallocate(addr); err != nil {
			t.Fatalf("dealloc: %v", err)
		}
	}
	// After all frees the entire heap should be allocatable as one block.
	b, err := a.Allocate(1024, "all")
	if err != nil {
		t.Fatalf("expected full 1024 alloc after buddy merge, got %v", err)
	}
	if b == nil || b.Size < 1024 {
		t.Errorf("expected size >= 1024, got %d", b.Size)
	}
}

// TestBuddy_ListSortedAfterMerge verifies the linked list remains sorted
// by address after a series of merge operations. The previous
// implementation's merge inserted the merged block at the tail of the
// list, breaking the order invariant relied on by the UI.
func TestBuddy_ListSortedAfterMerge(t *testing.T) {
	a := NewBuddyAllocator(1024)
	addrs := make([]uintptr, 0, 8)
	for i := 0; i < 8; i++ {
		b, err := a.Allocate(128, "x")
		if err != nil {
			t.Fatalf("alloc %d: %v", i, err)
		}
		addrs = append(addrs, b.Address)
	}
	// Free in a non-monotonic order to stress the merge path.
	order := []int{3, 1, 5, 0, 6, 2, 4, 7}
	for _, i := range order {
		if err := a.Deallocate(addrs[i]); err != nil {
			t.Fatalf("dealloc %d: %v", i, err)
		}
	}
	blocks := a.GetAllBlocks()
	for i := 1; i < len(blocks); i++ {
		if blocks[i].Address < blocks[i-1].Address {
			t.Errorf("list not sorted: block[%d].Address=0x%x < block[%d].Address=0x%x",
				i, blocks[i].Address, i-1, blocks[i-1].Address)
		}
	}
}

// TestFirstFit_ConcurrentAllocDealloc exercises the race condition that
// existed when firstBlock() returned a live pointer under a brief lock.
// The new implementation walks the list via HeadLocked() while the
// allocator's mu is held. With -race, this test must not flag any
// concurrent map/struct access.
func TestFirstFit_ConcurrentAllocDealloc(t *testing.T) {
	a := NewFirstFitAllocator(1 << 20) // 1 MiB
	var wg sync.WaitGroup
	const workers = 8
	const iters = 200
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			held := make([]uintptr, 0, 32)
			for i := 0; i < iters; i++ {
				b, err := a.Allocate(64, "c")
				if err == nil && b != nil {
					held = append(held, b.Address)
				}
				if len(held) > 8 {
					_ = a.Deallocate(held[0])
					held = held[1:]
				}
			}
			for _, addr := range held {
				_ = a.Deallocate(addr)
			}
		}()
	}
	wg.Wait()
}

// TestBuddy_ConcurrentAllocDealloc is the buddy-system equivalent of the
// above, exercising the buddy list-walking path under contention.
func TestBuddy_ConcurrentAllocDealloc(t *testing.T) {
	a := NewBuddyAllocator(1 << 20)
	var wg sync.WaitGroup
	const workers = 8
	const iters = 200
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			held := make([]uintptr, 0, 32)
			for i := 0; i < iters; i++ {
				b, err := a.Allocate(64, "c")
				if err == nil && b != nil {
					held = append(held, b.Address)
				}
				if len(held) > 8 {
					_ = a.Deallocate(held[0])
					held = held[1:]
				}
			}
			for _, addr := range held {
				_ = a.Deallocate(addr)
			}
		}()
	}
	wg.Wait()
}
