package allocator

import (
	"sync"
	"testing"
)

func TestSegregated_BasicAllocate(t *testing.T) {
	a := NewSegregatedFitAllocator(4096)
	b, err := a.Allocate(50, "user1")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if b.Size < 50 {
		t.Errorf("Size: got %d, want >= 50", b.Size)
	}
}

func TestSegregated_PicksSmallestFittingClass(t *testing.T) {
	a := NewSegregatedFitAllocator(4096)
	b1, _ := a.Allocate(8, "a")
	if b1.Size != 16 {
		t.Errorf("8-byte request should pick 16-byte class, got %d", b1.Size)
	}
	b2, _ := a.Allocate(17, "b")
	if b2.Size != 32 {
		t.Errorf("17-byte request should pick 32-byte class, got %d", b2.Size)
	}
}

func TestSegregated_Deallocate(t *testing.T) {
	a := NewSegregatedFitAllocator(4096)
	b, _ := a.Allocate(50, "x")
	if err := a.Deallocate(b.Address); err != nil {
		t.Fatalf("Deallocate: %v", err)
	}
}

func TestSegregated_DeallocateUnknown(t *testing.T) {
	a := NewSegregatedFitAllocator(4096)
	if err := a.Deallocate(0xDEADBEEF); err != ErrBlockNotFound {
		t.Errorf("expected ErrBlockNotFound, got %v", err)
	}
}

func TestSegregated_DoubleFree(t *testing.T) {
	a := NewSegregatedFitAllocator(4096)
	b, _ := a.Allocate(50, "x")
	if err := a.Deallocate(b.Address); err != nil {
		t.Fatalf("first dealloc: %v", err)
	}
	if err := a.Deallocate(b.Address); err != ErrAlreadyFreed {
		t.Errorf("expected ErrAlreadyFreed, got %v", err)
	}
}

func TestSegregated_InvalidSize(t *testing.T) {
	a := NewSegregatedFitAllocator(4096)
	if _, err := a.Allocate(0, "x"); err != ErrInvalidSize {
		t.Errorf("expected ErrInvalidSize, got %v", err)
	}
}

func TestSegregated_ExceedsLargestClass(t *testing.T) {
	a := NewSegregatedFitAllocator(4096)
	// Default max class is 4096 — request larger.
	if _, err := a.Allocate(8192, "toolarge"); err != ErrOutOfMemory {
		t.Errorf("expected ErrOutOfMemory, got %v", err)
	}
}

func TestSegregated_AllocateAfterExhaustion(t *testing.T) {
	a := NewSegregatedFitAllocator(4096)
	// Allocate a 4096-byte block — exhausts the top class.
	b1, err := a.Allocate(4096, "a")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if b1.Size != 4096 {
		t.Errorf("Size: got %d, want 4096", b1.Size)
	}
	// Another 4096-byte block should fail (no more top-class slots).
	if _, err := a.Allocate(4096, "b"); err != ErrOutOfMemory {
		t.Errorf("expected OOM, got %v", err)
	}
}

func TestSegregated_SplitAndMerge(t *testing.T) {
	a := NewSegregatedFitAllocator(4096)
	// Allocate two 32-byte objects. They should come from splitting
	// the 4096-byte top block.
	b1, _ := a.Allocate(20, "a")
	b2, _ := a.Allocate(20, "b")
	if b1.Size != 32 {
		t.Errorf("b1.Size: got %d, want 32", b1.Size)
	}
	if b2.Size != 32 {
		t.Errorf("b2.Size: got %d, want 32", b2.Size)
	}
	if b1.Address == b2.Address {
		t.Errorf("expected different addresses, both at %x", b1.Address)
	}
	// Free both. Buddy merge should bring them back together.
	if err := a.Deallocate(b1.Address); err != nil {
		t.Fatalf("dealloc b1: %v", err)
	}
	if err := a.Deallocate(b2.Address); err != nil {
		t.Fatalf("dealloc b2: %v", err)
	}
	// After merge, the top class should have a free block.
	summary := a.GetClassSummary()
	if summary[4096] == 0 {
		t.Errorf("expected merged block at top class 4096, got summary %v", summary)
	}
	// Allocate a 4096-byte block — should succeed.
	b3, err := a.Allocate(4000, "c")
	if err != nil {
		t.Fatalf("alloc 4000: %v", err)
	}
	if b3.Size != 4096 {
		t.Errorf("b3.Size: got %d, want 4096", b3.Size)
	}
}

func TestSegregated_Reset(t *testing.T) {
	a := NewSegregatedFitAllocator(4096)
	b, _ := a.Allocate(50, "x")
	a.Deallocate(b.Address)
	a.Reset()
	// Should be re-allocatable.
	if _, err := a.Allocate(50, "y"); err != nil {
		t.Errorf("alloc after reset: %v", err)
	}
}

func TestSegregated_GetBlocks(t *testing.T) {
	a := NewSegregatedFitAllocator(4096)
	b, _ := a.Allocate(50, "x")
	got, err := a.GetBlock(b.Address)
	if err != nil {
		t.Fatalf("GetBlock: %v", err)
	}
	if got.Address != b.Address {
		t.Errorf("address mismatch")
	}
	all := a.GetAllBlocks()
	if len(all) == 0 {
		t.Error("GetAllBlocks empty")
	}
	alloc := a.GetAllocatedBlocks()
	if len(alloc) != 1 {
		t.Errorf("GetAllocatedBlocks: got %d, want 1", len(alloc))
	}

	if err := a.Deallocate(b.Address); err != nil {
		t.Fatalf("Deallocate: %v", err)
	}
	alloc = a.GetAllocatedBlocks()
	if len(alloc) != 0 {
		t.Errorf("GetAllocatedBlocks after free: got %d, want 0", len(alloc))
	}
	if _, err := a.GetBlock(b.Address); err != ErrAlreadyFreed {
		t.Errorf("GetBlock on freed address: got %v, want ErrAlreadyFreed", err)
	}
}

func TestSegregated_GetMetrics(t *testing.T) {
	a := NewSegregatedFitAllocator(4096)
	b, _ := a.Allocate(50, "x")
	_ = b
	m := a.GetMetrics()
	if m.TotalAllocations != 1 {
		t.Errorf("TotalAllocations: got %d, want 1", m.TotalAllocations)
	}
}

func TestSegregated_Fragmentation(t *testing.T) {
	a := NewSegregatedFitAllocator(4096)
	// Allocate two small blocks from the top block. This creates
	// fragmentation (many free smaller blocks).
	_, _ = a.Allocate(20, "a")
	_, _ = a.Allocate(20, "b")
	frag := a.CalculateFragmentation()
	if frag < 0 || frag > 100 {
		t.Errorf("frag out of range: %f", frag)
	}
}

func TestSegregated_CoalesceIsNoop(t *testing.T) {
	a := NewSegregatedFitAllocator(4096)
	if got := a.Coalesce(); got != 0 {
		t.Errorf("Coalesce: got %d, want 0", got)
	}
}

func TestSegregated_Name(t *testing.T) {
	a := NewSegregatedFitAllocator(4096)
	if a.Name() != "Segregated Fit" {
		t.Errorf("Name: got %q, want %q", a.Name(), "Segregated Fit")
	}
}

func TestSegregated_TotalSize(t *testing.T) {
	a := NewSegregatedFitAllocator(4096)
	// TotalSize is rounded up to the largest class.
	if got := a.TotalSize(); got < 4096 {
		t.Errorf("TotalSize: got %d, want >= 4096", got)
	}
}

func TestSegregated_ConcurrentAllocDealloc(t *testing.T) {
	a := NewSegregatedFitAllocator(1 << 16)
	const goroutines = 8
	const opsPerG = 200
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var live []uintptr
			for i := 0; i < opsPerG; i++ {
				b, err := a.Allocate(8, "c")
				if err == nil {
					live = append(live, b.Address)
				}
				if len(live) > 0 && i%3 == 0 {
					addr := live[0]
					live = live[1:]
					if err := a.Deallocate(addr); err != nil {
						t.Errorf("dealloc: %v", err)
					}
				}
			}
		}()
	}
	wg.Wait()
}

func TestSegregated_CustomClasses(t *testing.T) {
	a := NewSegregatedFitAllocatorWithClasses(1024, []int{8, 16, 32, 64})
	b, err := a.Allocate(7, "a")
	if err != nil {
		t.Fatalf("alloc: %v", err)
	}
	if b.Size != 8 {
		t.Errorf("class: got %d, want 8", b.Size)
	}
}

func TestSegregated_UsesFullCapacityAcrossTopLevelSlots(t *testing.T) {
	a := NewSegregatedFitAllocator(8192)
	count := 0
	for {
		b, err := a.Allocate(64, "bulk")
		if err == ErrOutOfMemory {
			break
		}
		if err != nil {
			t.Fatalf("allocate: %v", err)
		}
		if b.Size != 64 {
			t.Fatalf("got block size %d, want 64", b.Size)
		}
		count++
	}
	if count != 128 {
		t.Fatalf("expected to allocate 128 64-byte blocks from 8192 bytes, got %d", count)
	}
}

func TestSegregated_PanicOnNonAscending(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on non-ascending classes")
		}
	}()
	NewSegregatedFitAllocatorWithClasses(1024, []int{16, 32, 16})
}

func TestSegregated_PanicOnNonDoublingClasses(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on non-doubling classes")
		}
	}()
	NewSegregatedFitAllocatorWithClasses(1024, []int{16, 32, 96})
}
