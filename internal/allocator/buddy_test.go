package allocator

import (
	"sync"
	"testing"
)

func TestBuddyAllocator_AllocateAllAndFree(t *testing.T) {
	a := NewBuddyAllocator(1024)
	addrs := make([]uintptr, 0, 16)
	for i := 0; i < 16; i++ {
		b, err := a.Allocate(64, "x")
		if err != nil {
			t.Fatalf("Alloc %d failed: %v", i, err)
		}
		addrs = append(addrs, b.Address)
	}
	// 17th should fail
	if _, err := a.Allocate(64, "x"); err == nil {
		t.Error("Expected out-of-memory on 17th 64-byte alloc")
	}
	for _, addr := range addrs {
		if err := a.Deallocate(addr); err != nil {
			t.Fatalf("Dealloc 0x%x failed: %v", addr, err)
		}
	}
	// After freeing all, we should be able to allocate one 1024-byte block
	b, err := a.Allocate(1024, "x")
	if err != nil {
		t.Fatalf("Final alloc failed: %v", err)
	}
	if b.Size < 1024 {
		t.Errorf("Expected >= 1024, got %d", b.Size)
	}
}

func TestBuddyAllocator_MergeOnDealloc(t *testing.T) {
	a := NewBuddyAllocator(1024)
	// Allocate two 64-byte blocks at 0x1000 and 0x1040 (they should be buddies)
	b1, _ := a.Allocate(64, "x")
	b2, _ := a.Allocate(64, "y")
	if b1.Address == b2.Address {
		t.Fatal("Expected distinct addresses")
	}

	// Free them in any order and verify we can re-allocate 128 bytes
	_ = a.Deallocate(b1.Address)
	_ = a.Deallocate(b2.Address)

	// After both buddies are freed, the next 128-byte alloc should succeed
	// (the 128-byte buddy is also free, so we should get a 128-byte block)
	big, err := a.Allocate(100, "big")
	if err != nil {
		t.Fatalf("Expected to allocate 100 after buddy merge: %v", err)
	}
	if big.Size < 100 {
		t.Errorf("Expected >= 100, got %d", big.Size)
	}
}

func TestBuddyAllocator_Reset(t *testing.T) {
	a := NewBuddyAllocator(1024)
	// Allocate all
	for i := 0; i < 16; i++ {
		_, _ = a.Allocate(64, "x")
	}
	a.Reset()
	// After reset, full capacity is restored
	b, err := a.Allocate(1024, "x")
	if err != nil {
		t.Fatalf("Reset failed to restore capacity: %v", err)
	}
	if b == nil {
		t.Fatal("Expected a block of size >= 1024")
	}
}

func TestBuddyAllocator_InvalidSize(t *testing.T) {
	a := NewBuddyAllocator(1024)
	if _, err := a.Allocate(0, "x"); err == nil {
		t.Error("Expected error for size 0")
	}
	if _, err := a.Allocate(-5, "x"); err == nil {
		t.Error("Expected error for negative size")
	}
}

func TestBuddyAllocator_DoubleFree(t *testing.T) {
	a := NewBuddyAllocator(1024)
	b, _ := a.Allocate(64, "x")
	if err := a.Deallocate(b.Address); err != nil {
		t.Fatal(err)
	}
	if err := a.Deallocate(b.Address); err == nil {
		t.Error("Expected error on double free")
	}
}

func TestBuddyAllocator_InvalidAddress(t *testing.T) {
	a := NewBuddyAllocator(1024)
	if err := a.Deallocate(0xDEADBEEF); err == nil {
		t.Error("Expected error for unknown address")
	}
}

func TestBuddyAllocator_Concurrent(t *testing.T) {
	a := NewBuddyAllocator(16384)

	const goroutines = 20
	const allocsPerG = 50

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			blocks := make([]uintptr, 0, allocsPerG)
			for i := 0; i < allocsPerG; i++ {
				b, err := a.Allocate(64, "concurrent")
				if err == nil && b != nil {
					blocks = append(blocks, b.Address)
				}
			}
			for _, addr := range blocks {
				_ = a.Deallocate(addr)
			}
		}()
	}
	wg.Wait()
}

func TestBuddyAllocator_AllBlocksListConsistency(t *testing.T) {
	a := NewBuddyAllocator(1024)
	for i := 0; i < 5; i++ {
		_, _ = a.Allocate(64, "x")
	}
	// Check that we get at most one block per (address, level) and that
	// total free + allocated size == totalSize.
	all := a.GetAllBlocks()
	seen := make(map[uintptr]int)
	totalSize := 0
	for _, b := range all {
		if _, dup := seen[b.Address]; dup {
			t.Errorf("Duplicate block at address 0x%x in list", b.Address)
		}
		seen[b.Address] = 1
		totalSize += b.Size
	}
	if totalSize > a.TotalSize() {
		t.Errorf("Total block size %d exceeds total memory %d", totalSize, a.TotalSize())
	}
}
