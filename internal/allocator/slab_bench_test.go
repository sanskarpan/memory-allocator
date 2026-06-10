package allocator

import (
	"testing"
)

// BenchmarkSlabAllocation measures the slab allocator's hot path:
// pick class, pop from free list.
func BenchmarkSlabAllocation(b *testing.B) {
	a := NewSlabAllocator(1 << 20)
	// Pre-warm: take 100 objects so the free list isn't the
	// shortest path. (We don't need them back; the benchmark
	// will keep allocating from a finite pool. The point is to
	// measure per-op cost, not exhaustion.)
	for i := 0; i < 100; i++ {
		blk, _ := a.Allocate(8, "warm")
		_ = blk
	}
	live := make([]uintptr, 0, b.N+100)
	for i := 0; i < 100; i++ {
		blk, _ := a.Allocate(8, "warm2")
		live = append(live, blk.Address)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blk, err := a.Allocate(8, "bench")
		if err == nil {
			live = append(live, blk.Address)
		}
	}
}

// BenchmarkSlabDeallocation measures the slab allocator's free path.
func BenchmarkSlabDeallocation(b *testing.B) {
	a := NewSlabAllocator(1 << 20)
	// Pre-allocate b.N + 100 objects.
	live := make([]uintptr, 0, b.N+100)
	for i := 0; i < b.N+100; i++ {
		blk, err := a.Allocate(8, "warm")
		if err == nil {
			live = append(live, blk.Address)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = a.Deallocate(live[i])
	}
}

// BenchmarkSlabVaryingSize exercises the class-selection path.
func BenchmarkSlabVaryingSize(b *testing.B) {
	a := NewSlabAllocator(1 << 20)
	live := make([]uintptr, 0, b.N+100)
	sizes := []int{8, 50, 200, 1000, 5000, 10000}
	for i := 0; i < b.N+100; i++ {
		s := sizes[i%len(sizes)]
		blk, err := a.Allocate(s, "bench")
		if err == nil {
			live = append(live, blk.Address)
		}
	}
}

// BenchmarkSegregatedAllocation measures the segregated-fit hot path.
func BenchmarkSegregatedAllocation(b *testing.B) {
	a := NewSegregatedFitAllocator(1 << 20)
	live := make([]uintptr, 0, b.N+100)
	for i := 0; i < 100; i++ {
		blk, _ := a.Allocate(8, "warm")
		live = append(live, blk.Address)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		blk, err := a.Allocate(8, "bench")
		if err == nil {
			live = append(live, blk.Address)
		}
	}
}

// BenchmarkSegregatedDeallocation measures the segregated-fit free
// path, including the buddy-style merge up the class chain.
func BenchmarkSegregatedDeallocation(b *testing.B) {
	a := NewSegregatedFitAllocator(1 << 20)
	live := make([]uintptr, 0, b.N+100)
	for i := 0; i < b.N+100; i++ {
		blk, err := a.Allocate(50, "warm")
		if err == nil {
			live = append(live, blk.Address)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = a.Deallocate(live[i])
	}
}

// BenchmarkSegregatedVaryingSize exercises the class-selection path.
func BenchmarkSegregatedVaryingSize(b *testing.B) {
	a := NewSegregatedFitAllocator(1 << 20)
	live := make([]uintptr, 0, b.N+100)
	sizes := []int{8, 50, 200, 1000, 5000, 10000}
	for i := 0; i < b.N+100; i++ {
		s := sizes[i%len(sizes)]
		blk, err := a.Allocate(s, "bench")
		if err == nil {
			live = append(live, blk.Address)
		}
	}
}

// BenchmarkAllocatorComparisonTable is a side-by-side allocation-cost
// table for all six allocators, useful for the audit report.
func BenchmarkAllocatorComparisonTable(b *testing.B) {
	makers := map[string]func() Allocator{
		"FirstFit":   func() Allocator { return NewFirstFitAllocator(1 << 20) },
		"BestFit":    func() Allocator { return NewBestFitAllocator(1 << 20) },
		"WorstFit":   func() Allocator { return NewWorstFitAllocator(1 << 20) },
		"Buddy":      func() Allocator { return NewBuddyAllocator(1 << 20) },
		"Slab":       func() Allocator { return NewSlabAllocator(1 << 20) },
		"Segregated": func() Allocator { return NewSegregatedFitAllocator(1 << 20) },
	}
	for name, mk := range makers {
		b.Run(name, func(b *testing.B) {
			a := mk()
			live := make([]uintptr, 0, b.N+100)
			for i := 0; i < 100; i++ {
				blk, _ := a.Allocate(64, "warm")
				if blk != nil {
					live = append(live, blk.Address)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				blk, err := a.Allocate(64, "bench")
				if err == nil {
					live = append(live, blk.Address)
				}
			}
		})
	}
}
