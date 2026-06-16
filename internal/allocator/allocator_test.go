package allocator

import (
	"testing"
)

func TestFirstFitAllocator(t *testing.T) {
	alloc := NewFirstFitAllocator(1024)

	// Test basic allocation
	block1, err := alloc.Allocate(256, "test1")
	if err != nil {
		t.Fatalf("Failed to allocate: %v", err)
	}
	if block1 == nil || block1.Size != 256 {
		t.Errorf("Expected block of size 256, got %v", block1)
	}

	// Test second allocation
	block2, err := alloc.Allocate(128, "test2")
	if err != nil {
		t.Fatalf("Failed to allocate second block: %v", err)
	}
	if block2 == nil || block2.Size != 128 {
		t.Errorf("Expected block of size 128, got %v", block2)
	}

	// Test deallocation
	err = alloc.Deallocate(block1.Address)
	if err != nil {
		t.Fatalf("Failed to deallocate: %v", err)
	}

	// Test reallocation in freed space
	block3, err := alloc.Allocate(100, "test3")
	if err != nil {
		t.Fatalf("Failed to reallocate: %v", err)
	}
	if block3 == nil {
		t.Error("Expected successful reallocation")
	}

	// Test out of memory
	_, err = alloc.Allocate(2000, "toolarge")
	if err != ErrOutOfMemory {
		t.Errorf("Expected ErrOutOfMemory, got %v", err)
	}
}

func TestBestFitAllocator(t *testing.T) {
	alloc := NewBestFitAllocator(1024)

	// Allocate and deallocate to create fragmentation
	b1, _ := alloc.Allocate(100, "test1")
	b2, _ := alloc.Allocate(200, "test2")
	b3, _ := alloc.Allocate(150, "test3")

	alloc.Deallocate(b2.Address)

	// Best fit should use the 200-byte hole for 180-byte allocation
	b4, err := alloc.Allocate(180, "test4")
	if err != nil {
		t.Fatalf("Failed to allocate with best fit: %v", err)
	}

	// Verify it's using the right space
	if b4.Address < b1.Address || b4.Address > b3.Address {
		t.Error("Best fit did not select optimal block")
	}

	// Test metrics
	metrics := alloc.GetMetrics()
	if metrics.TotalAllocations != 4 {
		t.Errorf("Expected 4 allocations, got %d", metrics.TotalAllocations)
	}
}

func TestWorstFitAllocator(t *testing.T) {
	alloc := NewWorstFitAllocator(2048)

	// Test worst fit selects largest block
	b1, _ := alloc.Allocate(256, "test1")
	b2, _ := alloc.Allocate(512, "test2")
	alloc.Deallocate(b1.Address)
	alloc.Deallocate(b2.Address)

	// Should select the larger freed block (512 bytes)
	b3, err := alloc.Allocate(100, "test3")
	if err != nil {
		t.Fatalf("Failed to allocate with worst fit: %v", err)
	}
	if b3 == nil {
		t.Error("Expected successful allocation")
	}

	// Verify all blocks
	blocks := alloc.GetAllBlocks()
	if len(blocks) == 0 {
		t.Error("Expected blocks to be present")
	}
}

func TestBuddyAllocator(t *testing.T) {
	alloc := NewBuddyAllocator(1024)

	// Test power-of-2 allocation
	block1, err := alloc.Allocate(100, "test1")
	if err != nil {
		t.Fatalf("Failed to allocate: %v", err)
	}

	// Buddy system rounds up to power of 2
	if block1.Size < 100 {
		t.Errorf("Block size should be at least 100, got %d", block1.Size)
	}

	// Test multiple allocations
	block2, err := alloc.Allocate(200, "test2")
	if err != nil {
		t.Fatalf("Failed to allocate second block: %v", err)
	}

	// Test deallocation and merging
	err = alloc.Deallocate(block1.Address)
	if err != nil {
		t.Fatalf("Failed to deallocate: %v", err)
	}

	err = alloc.Deallocate(block2.Address)
	if err != nil {
		t.Fatalf("Failed to deallocate second block: %v", err)
	}

	// After deallocating buddies, should be able to allocate larger block
	block3, err := alloc.Allocate(400, "test3")
	if err != nil {
		t.Fatalf("Failed to allocate after merging: %v", err)
	}
	if block3 == nil {
		t.Error("Expected successful allocation after buddy merge")
	}
}

func TestCoalescing(t *testing.T) {
	alloc := NewFirstFitAllocator(1024)

	// Allocate several blocks
	b1, _ := alloc.Allocate(100, "test1")
	b2, _ := alloc.Allocate(100, "test2")
	b3, _ := alloc.Allocate(100, "test3")
	b4, _ := alloc.Allocate(100, "test4")

	// Deallocate non-adjacent blocks first (to prevent auto-coalescing)
	alloc.Deallocate(b2.Address)
	alloc.Deallocate(b4.Address)

	// Now deallocate adjacent blocks
	alloc.Deallocate(b1.Address)
	alloc.Deallocate(b3.Address)

	// At this point, auto-coalescing may have merged some blocks
	// Verify we can allocate a larger block
	b5, err := alloc.Allocate(180, "test5")
	if err != nil {
		t.Errorf("Should be able to allocate after coalescing: %v", err)
	}
	if b5 == nil {
		t.Error("Expected successful allocation")
	}

	// Test explicit coalesce call (may be no-op if already coalesced)
	freeBlocksBefore := len(alloc.GetFreeBlocks())
	merged := alloc.Coalesce()

	// Either blocks were merged, or they were already coalesced
	if merged > 0 {
		freeBlocksAfter := len(alloc.GetFreeBlocks())
		if freeBlocksAfter >= freeBlocksBefore {
			t.Error("Coalescing should reduce or maintain number of free blocks")
		}
	}

	// Clean up
	alloc.Deallocate(b5.Address)
}

func TestFirstFitAutoCoalescesWithRightNeighbor(t *testing.T) {
	alloc := NewFirstFitAllocator(512)

	b1, err := alloc.Allocate(128, "left")
	if err != nil {
		t.Fatalf("alloc left: %v", err)
	}
	b2, err := alloc.Allocate(128, "right")
	if err != nil {
		t.Fatalf("alloc right: %v", err)
	}

	if err := alloc.Deallocate(b2.Address); err != nil {
		t.Fatalf("free right: %v", err)
	}
	if err := alloc.Deallocate(b1.Address); err != nil {
		t.Fatalf("free left: %v", err)
	}

	merged, err := alloc.Allocate(256, "merged")
	if err != nil {
		t.Fatalf("alloc merged: %v", err)
	}
	if merged.Address != b1.Address {
		t.Fatalf("expected merged block at 0x%x, got 0x%x", b1.Address, merged.Address)
	}
}

func TestFragmentation(t *testing.T) {
	alloc := NewFirstFitAllocator(2048)

	// Create fragmented memory
	blocks := make([]uintptr, 10)
	for i := 0; i < 10; i++ {
		b, _ := alloc.Allocate(100, "test")
		blocks[i] = b.Address
	}

	// Deallocate every other block
	for i := 0; i < 10; i += 2 {
		alloc.Deallocate(blocks[i])
	}

	// Calculate fragmentation
	frag := alloc.CalculateFragmentation()

	// Should have some fragmentation
	if frag == 0 {
		t.Error("Expected non-zero fragmentation")
	}

	// Clean up
	for i := 1; i < 10; i += 2 {
		alloc.Deallocate(blocks[i])
	}
}

func TestAllocatorReset(t *testing.T) {
	alloc := NewFirstFitAllocator(1024)

	// Allocate some blocks
	alloc.Allocate(100, "test1")
	alloc.Allocate(200, "test2")

	// Get metrics
	metrics := alloc.GetMetrics()
	if metrics.TotalAllocations != 2 {
		t.Errorf("Expected 2 allocations, got %d", metrics.TotalAllocations)
	}

	// Reset
	alloc.Reset()

	// Metrics should be reset
	metrics = alloc.GetMetrics()
	if metrics.TotalAllocations != 0 {
		t.Errorf("Expected 0 allocations after reset, got %d", metrics.TotalAllocations)
	}

	// Should be able to allocate full size
	block, err := alloc.Allocate(1024, "testfull")
	if err != nil {
		t.Errorf("Should be able to allocate full size after reset: %v", err)
	}
	if block == nil || block.Size != 1024 {
		t.Error("Expected full size allocation after reset")
	}
}

func TestInvalidOperations(t *testing.T) {
	alloc := NewFirstFitAllocator(1024)

	// Test invalid size
	_, err := alloc.Allocate(0, "test")
	if err != ErrInvalidSize {
		t.Errorf("Expected ErrInvalidSize, got %v", err)
	}

	_, err = alloc.Allocate(-100, "test")
	if err != ErrInvalidSize {
		t.Errorf("Expected ErrInvalidSize for negative size, got %v", err)
	}

	// Test deallocate invalid address
	err = alloc.Deallocate(0xDEADBEEF)
	if err != ErrBlockNotFound {
		t.Errorf("Expected ErrBlockNotFound, got %v", err)
	}

	// Test double free
	block, _ := alloc.Allocate(100, "test")
	alloc.Deallocate(block.Address)
	err = alloc.Deallocate(block.Address)
	if err != ErrBlockNotFound {
		t.Errorf("Expected error on double free, got %v", err)
	}
}

func TestConcurrentAllocations(t *testing.T) {
	alloc := NewFirstFitAllocator(8192)

	// Test concurrent allocations
	done := make(chan bool)
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		go func(id int) {
			_, err := alloc.Allocate(100, "concurrent")
			if err != nil {
				errors <- err
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Check for errors
	close(errors)
	for err := range errors {
		if err != nil && err != ErrOutOfMemory {
			t.Errorf("Unexpected error in concurrent allocation: %v", err)
		}
	}

	// Verify metrics
	metrics := alloc.GetMetrics()
	if metrics.TotalAllocations == 0 {
		t.Error("Expected some successful allocations")
	}
}

func TestGetBlocks(t *testing.T) {
	alloc := NewFirstFitAllocator(1024)

	// Allocate some blocks
	b1, _ := alloc.Allocate(100, "test1")
	b2, _ := alloc.Allocate(200, "test2")

	// Get all blocks
	allBlocks := alloc.GetAllBlocks()
	if len(allBlocks) < 2 {
		t.Errorf("Expected at least 2 blocks, got %d", len(allBlocks))
	}

	// Get allocated blocks
	allocatedBlocks := alloc.GetAllocatedBlocks()
	if len(allocatedBlocks) != 2 {
		t.Errorf("Expected 2 allocated blocks, got %d", len(allocatedBlocks))
	}

	// Deallocate one
	alloc.Deallocate(b1.Address)

	// Get free blocks
	freeBlocks := alloc.GetFreeBlocks()
	if len(freeBlocks) == 0 {
		t.Error("Expected at least one free block")
	}

	// Allocated blocks should be reduced
	allocatedBlocks = alloc.GetAllocatedBlocks()
	if len(allocatedBlocks) != 1 {
		t.Errorf("Expected 1 allocated block after deallocation, got %d", len(allocatedBlocks))
	}

	// Clean up
	alloc.Deallocate(b2.Address)
}
