package allocator

import (
	"testing"
)

func TestSlab_BasicAllocate(t *testing.T) {
	s := NewSlabAllocator(16384)
	b, err := s.Allocate(50, "user1")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if b.Size < 50 {
		t.Errorf("Size: got %d, want >= 50", b.Size)
	}
	if b.IsFree() {
		t.Error("expected allocated block")
	}
}

func TestSlab_PicksSmallestFittingClass(t *testing.T) {
	s := NewSlabAllocator(16384)
	b1, _ := s.Allocate(8, "a")
	if b1.Size != 16 {
		t.Errorf("8-byte request should pick 16-byte class, got %d", b1.Size)
	}
	b2, _ := s.Allocate(17, "b")
	if b2.Size != 32 {
		t.Errorf("17-byte request should pick 32-byte class, got %d", b2.Size)
	}
	b3, _ := s.Allocate(100, "c")
	if b3.Size != 128 {
		t.Errorf("100-byte request should pick 128-byte class, got %d", b3.Size)
	}
}

func TestSlab_Deallocate(t *testing.T) {
	s := NewSlabAllocator(16384)
	b, _ := s.Allocate(50, "x")
	if err := s.Deallocate(b.Address); err != nil {
		t.Fatalf("Deallocate: %v", err)
	}
	// Should be re-allocatable.
	b2, err := s.Allocate(50, "y")
	if err != nil {
		t.Fatalf("Re-allocate: %v", err)
	}
	if b2.Address != b.Address {
		t.Errorf("expected to reuse the freed slot, got addr %x, want %x", b2.Address, b.Address)
	}
}

func TestSlab_DeallocateUnknownAddress(t *testing.T) {
	s := NewSlabAllocator(16384)
	if err := s.Deallocate(0xDEADBEEF); err != ErrBlockNotFound {
		t.Errorf("expected ErrBlockNotFound, got %v", err)
	}
}

func TestSlab_DoubleFree(t *testing.T) {
	s := NewSlabAllocator(16384)
	b, _ := s.Allocate(50, "x")
	if err := s.Deallocate(b.Address); err != nil {
		t.Fatalf("first dealloc: %v", err)
	}
	if err := s.Deallocate(b.Address); err != ErrAlreadyFreed {
		t.Errorf("expected ErrAlreadyFreed, got %v", err)
	}
}

func TestSlab_ExceedsLargestClass(t *testing.T) {
	s := NewSlabAllocator(16384)
	_, err := s.Allocate(8192, "toolarge")
	if err != ErrSlabSizeTooBig {
		t.Errorf("expected ErrSlabSizeTooBig, got %v", err)
	}
}

func TestSlab_InvalidSize(t *testing.T) {
	s := NewSlabAllocator(16384)
	if _, err := s.Allocate(0, "x"); err != ErrSlabInvalidSize {
		t.Errorf("expected ErrSlabInvalidSize, got %v", err)
	}
	if _, err := s.Allocate(-1, "x"); err != ErrSlabInvalidSize {
		t.Errorf("expected ErrSlabInvalidSize, got %v", err)
	}
}

func TestSlab_ExhaustsClass(t *testing.T) {
	// Use a tiny region so we can quickly exhaust one class.
	s := NewSlabAllocator(128)
	// 128 bytes / 8 classes = 16 bytes per class. 16-byte class
	// gets 1 object, then is exhausted for size > 0.
	if _, err := s.Allocate(1, "a"); err != nil {
		t.Fatalf("first alloc: %v", err)
	}
	// Second 1-byte alloc should pick the same 16-byte class and fail.
	if _, err := s.Allocate(1, "b"); err != ErrSlabExhausted {
		t.Errorf("expected ErrSlabExhausted, got %v", err)
	}
}

func TestSlab_Reset(t *testing.T) {
	s := NewSlabAllocator(16384)
	pre := s.GetSlabStats().TotalCap
	// Allocate and free.
	b, _ := s.Allocate(50, "x")
	s.Deallocate(b.Address)
	s.Reset()
	post := s.GetSlabStats()
	if pre != post.TotalCap {
		t.Errorf("capacity changed after reset: %d -> %d", pre, post.TotalCap)
	}
	if post.TotalUse != 0 {
		t.Errorf("TotalUse should be 0 after reset, got %d", post.TotalUse)
	}
	// Should be re-allocatable.
	if _, err := s.Allocate(50, "y"); err != nil {
		t.Errorf("alloc after reset: %v", err)
	}
}

func TestSlab_GetAllocatedBlocks(t *testing.T) {
	s := NewSlabAllocator(16384)
	b1, _ := s.Allocate(10, "a")
	b2, _ := s.Allocate(100, "b")
	all := s.GetAllocatedBlocks()
	if len(all) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(all))
	}
	seen := map[uintptr]bool{b1.Address: true, b2.Address: true}
	for _, b := range all {
		if !seen[b.Address] {
			t.Errorf("unexpected block at %x", b.Address)
		}
	}
}

func TestSlab_GetFreeBlocks(t *testing.T) {
	s := NewSlabAllocator(16384)
	// Allocate 1 object — most slots stay free.
	b, _ := s.Allocate(10, "a")
	_ = b
	free := s.GetFreeBlocks()
	if len(free) == 0 {
		t.Error("expected free blocks")
	}
}

func TestSlab_GetBlock(t *testing.T) {
	s := NewSlabAllocator(16384)
	b, _ := s.Allocate(50, "x")
	got, err := s.GetBlock(b.Address)
	if err != nil {
		t.Fatalf("GetBlock: %v", err)
	}
	if got.Address != b.Address {
		t.Errorf("address mismatch: %x vs %x", got.Address, b.Address)
	}
	if _, err := s.GetBlock(0xBAD); err != ErrBlockNotFound {
		t.Errorf("expected ErrBlockNotFound, got %v", err)
	}
}

func TestSlab_GetMetrics(t *testing.T) {
	s := NewSlabAllocator(16384)
	b, _ := s.Allocate(50, "x")
	_ = b
	m := s.GetMetrics()
	if m.TotalAllocations != 1 {
		t.Errorf("TotalAllocations: got %d, want 1", m.TotalAllocations)
	}
}

func TestSlab_CoalesceIsNoop(t *testing.T) {
	s := NewSlabAllocator(16384)
	if got := s.Coalesce(); got != 0 {
		t.Errorf("Coalesce: got %d, want 0", got)
	}
}

func TestSlab_Name(t *testing.T) {
	s := NewSlabAllocator(16384)
	if s.Name() != "Slab Allocator" {
		t.Errorf("Name: got %q, want %q", s.Name(), "Slab Allocator")
	}
}

func TestSlab_TotalSize(t *testing.T) {
	s := NewSlabAllocator(16384)
	if s.TotalSize() != 16384 {
		t.Errorf("TotalSize: got %d, want 16384", s.TotalSize())
	}
}

func TestSlab_ConcurrentAllocDealloc(t *testing.T) {
	s := NewSlabAllocator(1 << 16)
	const goroutines = 8
	const opsPerG = 200
	done := make(chan struct{}, goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer func() { done <- struct{}{} }()
			var live []uintptr
			for i := 0; i < opsPerG; i++ {
				b, err := s.Allocate(8, "c")
				if err == nil {
					live = append(live, b.Address)
				}
				if len(live) > 0 && i%3 == 0 {
					addr := live[0]
					live = live[1:]
					if err := s.Deallocate(addr); err != nil {
						t.Errorf("dealloc: %v", err)
					}
				}
			}
		}()
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}
	// All addresses in `live` should still be valid; not checked
	// here because the goroutines are local. Just ensure we exit
	// cleanly.
}

func TestSlab_CustomClasses(t *testing.T) {
	s := NewSlabAllocatorWithClasses(1024, []int{8, 16, 32})
	b, err := s.Allocate(7, "a")
	if err != nil {
		t.Fatalf("alloc: %v", err)
	}
	if b.Size != 8 {
		t.Errorf("class: got %d, want 8", b.Size)
	}
}

func TestSlab_PanicOnInvalidTotalSize(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on totalSize=0")
		}
	}()
	NewSlabAllocator(0)
}

func TestSlab_PanicOnInvalidClassSize(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on class size 0")
		}
	}()
	NewSlabAllocatorWithClasses(1024, []int{8, 0, 32})
}
