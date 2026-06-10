package allocator

import (
	"fmt"
	"testing"
)

// Benchmark First Fit allocator
func BenchmarkFirstFitAllocation(b *testing.B) {
	alloc := NewFirstFitAllocator(1048576) // 1MB

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alloc.Allocate(256, "bench")
		if i%100 == 99 {
			alloc.Reset() // Reset periodically to avoid exhaustion
		}
	}
}

func BenchmarkFirstFitDeallocation(b *testing.B) {
	alloc := NewFirstFitAllocator(1048576)

	// Pre-allocate blocks
	addresses := make([]uintptr, 1000)
	for i := 0; i < 1000; i++ {
		block, _ := alloc.Allocate(256, "bench")
		addresses[i] = block.Address
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := i % 1000
		alloc.Deallocate(addresses[idx])

		// Reallocate to keep testing
		block, _ := alloc.Allocate(256, "bench")
		addresses[idx] = block.Address
	}
}

// Benchmark Best Fit allocator
func BenchmarkBestFitAllocation(b *testing.B) {
	alloc := NewBestFitAllocator(1048576)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alloc.Allocate(256, "bench")
		if i%100 == 99 {
			alloc.Reset()
		}
	}
}

// Benchmark Worst Fit allocator
func BenchmarkWorstFitAllocation(b *testing.B) {
	alloc := NewWorstFitAllocator(1048576)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alloc.Allocate(256, "bench")
		if i%100 == 99 {
			alloc.Reset()
		}
	}
}

// Benchmark Buddy System allocator
func BenchmarkBuddyAllocation(b *testing.B) {
	alloc := NewBuddyAllocator(1048576)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alloc.Allocate(256, "bench")
		if i%100 == 99 {
			alloc.Reset()
		}
	}
}

func BenchmarkBuddyDeallocation(b *testing.B) {
	alloc := NewBuddyAllocator(1048576)

	// Pre-allocate blocks
	addresses := make([]uintptr, 500)
	for i := 0; i < 500; i++ {
		block, _ := alloc.Allocate(256, "bench")
		addresses[i] = block.Address
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := i % 500
		alloc.Deallocate(addresses[idx])

		// Reallocate
		block, _ := alloc.Allocate(256, "bench")
		addresses[idx] = block.Address
	}
}

// Benchmark allocation with varying sizes
func BenchmarkFirstFitVaryingSize(b *testing.B) {
	alloc := NewFirstFitAllocator(2097152) // 2MB
	sizes := []int{64, 128, 256, 512, 1024, 2048}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		size := sizes[i%len(sizes)]
		alloc.Allocate(size, "bench")
		if i%50 == 49 {
			alloc.Reset()
		}
	}
}

// Benchmark fragmentation calculation
func BenchmarkFragmentationCalculation(b *testing.B) {
	alloc := NewFirstFitAllocator(1048576)

	// Create fragmented memory
	addresses := make([]uintptr, 100)
	for i := 0; i < 100; i++ {
		block, _ := alloc.Allocate(1024, "bench")
		addresses[i] = block.Address
	}

	// Deallocate every other block
	for i := 0; i < 100; i += 2 {
		alloc.Deallocate(addresses[i])
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alloc.CalculateFragmentation()
	}
}

// Benchmark coalescing
func BenchmarkCoalescing(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		alloc := NewFirstFitAllocator(1048576)

		// Create fragmented memory
		addresses := make([]uintptr, 100)
		for j := 0; j < 100; j++ {
			block, _ := alloc.Allocate(1024, "bench")
			addresses[j] = block.Address
		}

		// Deallocate all blocks
		for j := 0; j < 100; j++ {
			alloc.Deallocate(addresses[j])
		}

		b.StartTimer()
		alloc.Coalesce()
	}
}

// Comparison benchmark for all allocators
func BenchmarkAllocatorComparison(b *testing.B) {
	allocators := []struct {
		name  string
		alloc Allocator
	}{
		{"FirstFit", NewFirstFitAllocator(1048576)},
		{"BestFit", NewBestFitAllocator(1048576)},
		{"WorstFit", NewWorstFitAllocator(1048576)},
		{"Buddy", NewBuddyAllocator(1048576)},
	}

	for _, a := range allocators {
		b.Run(a.name, func(b *testing.B) {
			alloc := a.alloc
			sizes := []int{64, 128, 256, 512}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				size := sizes[i%len(sizes)]
				alloc.Allocate(size, "bench")
				if i%100 == 99 {
					alloc.Reset()
				}
			}
		})
	}
}

// Benchmark memory access patterns
func BenchmarkRandomAllocationDeallocation(b *testing.B) {
	alloc := NewFirstFitAllocator(2097152)
	addresses := make([]uintptr, 0, 500)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if len(addresses) < 500 && i%3 != 0 {
			// Allocate
			block, err := alloc.Allocate(256, "bench")
			if err == nil {
				addresses = append(addresses, block.Address)
			}
		} else if len(addresses) > 0 {
			// Deallocate
			idx := i % len(addresses)
			alloc.Deallocate(addresses[idx])
			addresses = append(addresses[:idx], addresses[idx+1:]...)
		}
	}
}

// Benchmark GetAllBlocks performance
func BenchmarkGetAllBlocks(b *testing.B) {
	alloc := NewFirstFitAllocator(1048576)

	// Allocate some blocks
	for i := 0; i < 100; i++ {
		alloc.Allocate(1024, fmt.Sprintf("bench%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = alloc.GetAllBlocks()
	}
}

// Benchmark metrics collection
func BenchmarkMetricsCollection(b *testing.B) {
	alloc := NewFirstFitAllocator(1048576)

	// Perform some allocations
	for i := 0; i < 50; i++ {
		alloc.Allocate(256, "bench")
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = alloc.GetMetrics()
	}
}
