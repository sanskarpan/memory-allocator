package pool

import (
	"testing"
)

func TestPoolAllocator(t *testing.T) {
	pool := NewPoolAllocator(256, 10)

	// Test basic allocation
	block, err := pool.Allocate(256, "test1")
	if err != nil {
		t.Fatalf("Failed to allocate: %v", err)
	}
	if block == nil || block.Size != 256 {
		t.Errorf("Expected block of size 256, got %v", block)
	}

	// Test multiple allocations
	blocks := make([]uintptr, 5)
	for i := 0; i < 5; i++ {
		b, err := pool.Allocate(256, "test")
		if err != nil {
			t.Fatalf("Failed to allocate block %d: %v", i, err)
		}
		blocks[i] = b.Address
	}

	// Test pool exhaustion
	for i := 0; i < 4; i++ {
		pool.Allocate(256, "exhaust")
	}
	_, err = pool.Allocate(256, "toMany")
	if err != ErrPoolExhausted {
		t.Errorf("Expected ErrPoolExhausted, got %v", err)
	}

	// Test deallocation
	err = pool.Deallocate(blocks[0])
	if err != nil {
		t.Fatalf("Failed to deallocate: %v", err)
	}

	// Should be able to allocate again
	block, err = pool.Allocate(256, "reuse")
	if err != nil {
		t.Errorf("Should be able to allocate after deallocation: %v", err)
	}
	if block == nil {
		t.Error("Expected successful reallocation")
	}
}

func TestPoolStats(t *testing.T) {
	pool := NewPoolAllocator(128, 20)

	// Allocate some blocks
	for i := 0; i < 10; i++ {
		pool.Allocate(128, "test")
	}

	// Get stats
	stats := pool.GetPoolStats()

	if stats.BlockSize != 128 {
		t.Errorf("Expected block size 128, got %d", stats.BlockSize)
	}

	if stats.TotalBlocks != 20 {
		t.Errorf("Expected 20 total blocks, got %d", stats.TotalBlocks)
	}

	if stats.UsedBlocks != 10 {
		t.Errorf("Expected 10 used blocks, got %d", stats.UsedBlocks)
	}

	if stats.FreeBlocks != 10 {
		t.Errorf("Expected 10 free blocks, got %d", stats.FreeBlocks)
	}

	expectedUtilization := 50.0
	if stats.Utilization != expectedUtilization {
		t.Errorf("Expected utilization %.1f%%, got %.1f%%", expectedUtilization, stats.Utilization)
	}
}

func TestPoolFragmentation(t *testing.T) {
	pool := NewPoolAllocator(256, 10)

	// Pool allocator should always have 0% fragmentation
	frag := pool.CalculateFragmentation()
	if frag != 0.0 {
		t.Errorf("Expected 0%% fragmentation for pool, got %.2f%%", frag)
	}

	// Allocate and deallocate
	b1, _ := pool.Allocate(256, "test1")
	pool.Allocate(256, "test2")
	pool.Deallocate(b1.Address)

	// Still should be 0% fragmentation
	frag = pool.CalculateFragmentation()
	if frag != 0.0 {
		t.Errorf("Pool should have no fragmentation, got %.2f%%", frag)
	}
}

func TestPoolReset(t *testing.T) {
	pool := NewPoolAllocator(256, 10)

	// Allocate all blocks
	for i := 0; i < 10; i++ {
		pool.Allocate(256, "test")
	}

	// Verify pool is exhausted
	_, err := pool.Allocate(256, "extra")
	if err != ErrPoolExhausted {
		t.Error("Expected pool to be exhausted")
	}

	// Reset
	pool.Reset()

	// Should be able to allocate again
	for i := 0; i < 10; i++ {
		_, err := pool.Allocate(256, "test")
		if err != nil {
			t.Errorf("Failed to allocate after reset: %v", err)
		}
	}

	stats := pool.GetPoolStats()
	if stats.FreeBlocks != 0 {
		t.Errorf("Expected 0 free blocks after reset and reallocation, got %d", stats.FreeBlocks)
	}
}

func TestPoolInvalidOperations(t *testing.T) {
	pool := NewPoolAllocator(256, 10)

	// Test deallocation of invalid address
	err := pool.Deallocate(0xDEADBEEF)
	if err != ErrInvalidBlock {
		t.Errorf("Expected ErrInvalidBlock, got %v", err)
	}

	// Test double deallocation
	block, _ := pool.Allocate(256, "test")
	pool.Deallocate(block.Address)
	err = pool.Deallocate(block.Address)
	if err != ErrInvalidBlock {
		t.Errorf("Expected error on double deallocation, got %v", err)
	}
}

func TestArenaAllocator(t *testing.T) {
	arena := NewArenaAllocator(2048)

	// Test basic allocation
	block1, err := arena.Allocate(256, "test1")
	if err != nil {
		t.Fatalf("Failed to allocate: %v", err)
	}
	if block1 == nil || block1.Size != 256 {
		t.Errorf("Expected block of size 256, got %v", block1)
	}

	// Test multiple allocations (bump pointer)
	block2, err := arena.Allocate(512, "test2")
	if err != nil {
		t.Fatalf("Failed to allocate second block: %v", err)
	}

	// Addresses should be sequential
	if block2.Address != block1.Address+uintptr(block1.Size) {
		t.Error("Arena allocations should be sequential")
	}

	// Test arena exhaustion
	_, err = arena.Allocate(2000, "toolarge")
	if err != ErrArenaFull {
		t.Errorf("Expected ErrArenaFull, got %v", err)
	}

	// Test that individual deallocation is not supported
	err = arena.Deallocate(block1.Address)
	if err == nil {
		t.Error("Arena should not support individual deallocation")
	}
}

func TestArenaStats(t *testing.T) {
	arena := NewArenaAllocator(4096)

	// Allocate some memory
	arena.Allocate(1024, "test1")
	arena.Allocate(512, "test2")

	// Get stats
	stats := arena.GetArenaStats()

	if stats.TotalSize != 4096 {
		t.Errorf("Expected total size 4096, got %d", stats.TotalSize)
	}

	expectedUsed := 1024 + 512
	if stats.UsedSize != expectedUsed {
		t.Errorf("Expected used size %d, got %d", expectedUsed, stats.UsedSize)
	}

	expectedFree := 4096 - expectedUsed
	if stats.FreeSize != expectedFree {
		t.Errorf("Expected free size %d, got %d", expectedFree, stats.FreeSize)
	}

	if stats.AllocationCount != 2 {
		t.Errorf("Expected 2 allocations, got %d", stats.AllocationCount)
	}
}

func TestArenaReset(t *testing.T) {
	arena := NewArenaAllocator(2048)

	// Allocate memory
	arena.Allocate(512, "test1")
	arena.Allocate(256, "test2")

	stats := arena.GetArenaStats()
	if stats.UsedSize == 0 {
		t.Error("Expected some used memory before reset")
	}

	// Reset
	arena.Reset()

	// All memory should be free
	stats = arena.GetArenaStats()
	if stats.UsedSize != 0 {
		t.Errorf("Expected 0 used size after reset, got %d", stats.UsedSize)
	}

	if stats.FreeSize != 2048 {
		t.Errorf("Expected full free size after reset, got %d", stats.FreeSize)
	}

	if stats.AllocationCount != 0 {
		t.Errorf("Expected 0 allocations after reset, got %d", stats.AllocationCount)
	}

	// Should be able to allocate full size
	block, err := arena.Allocate(2048, "full")
	if err != nil {
		t.Errorf("Should be able to allocate full size after reset: %v", err)
	}
	if block == nil || block.Size != 2048 {
		t.Error("Expected full size allocation after reset")
	}
}

func TestArenaCanAllocate(t *testing.T) {
	arena := NewArenaAllocator(1024)

	// Should be able to allocate
	if !arena.CanAllocate(512) {
		t.Error("Should be able to allocate 512 bytes")
	}

	// Allocate some memory
	arena.Allocate(800, "test")

	// Should be able to allocate remaining
	if !arena.CanAllocate(224) {
		t.Error("Should be able to allocate 224 bytes")
	}

	// Should not be able to allocate more than remaining
	if arena.CanAllocate(300) {
		t.Error("Should not be able to allocate 300 bytes")
	}
}

func TestArenaFragmentation(t *testing.T) {
	arena := NewArenaAllocator(2048)

	// Arena should have no fragmentation
	frag := arena.CalculateFragmentation()
	if frag != 0.0 {
		t.Errorf("Arena should have 0%% fragmentation, got %.2f%%", frag)
	}

	// Allocate and check again
	arena.Allocate(512, "test")
	frag = arena.CalculateFragmentation()
	if frag != 0.0 {
		t.Errorf("Arena should maintain 0%% fragmentation, got %.2f%%", frag)
	}
}

func TestPoolConcurrent(t *testing.T) {
	pool := NewPoolAllocator(128, 100)

	// Test concurrent allocations
	done := make(chan bool)
	errors := make(chan error, 50)

	for i := 0; i < 50; i++ {
		go func() {
			_, err := pool.Allocate(128, "concurrent")
			if err != nil && err != ErrPoolExhausted {
				errors <- err
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 50; i++ {
		<-done
	}

	// Check for unexpected errors
	close(errors)
	for err := range errors {
		t.Errorf("Unexpected error: %v", err)
	}

	// Verify stats
	stats := pool.GetPoolStats()
	if stats.UsedBlocks == 0 {
		t.Error("Expected some used blocks")
	}
}

func TestArenaSequential(t *testing.T) {
	arena := NewArenaAllocator(4096)

	// Allocate several blocks
	var prevAddr uintptr
	var prevSize int

	for i := 0; i < 10; i++ {
		size := 100 + i*50
		block, err := arena.Allocate(size, "test")
		if err != nil {
			t.Fatalf("Failed to allocate block %d: %v", i, err)
		}

		// Verify sequential allocation
		if i > 0 {
			expectedAddr := prevAddr + uintptr(prevSize)
			if block.Address != expectedAddr {
				t.Errorf("Block %d not sequential: expected 0x%x, got 0x%x",
					i, expectedAddr, block.Address)
			}
		}

		prevAddr = block.Address
		prevSize = block.Size
	}

	// Verify final stats
	stats := arena.GetArenaStats()
	if stats.AllocationCount != 10 {
		t.Errorf("Expected 10 allocations, got %d", stats.AllocationCount)
	}
}
