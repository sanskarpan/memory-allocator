# Benchmarks — Memory Allocator Simulator

**Date:** 2026-06-09
**Environment:** macOS arm64, Apple M3 Pro, Go 1.26.1
**Method:** `go test -bench=... -benchmem -benchtime=200ms -run=^$`

---

## Allocator Comparison

```
BenchmarkAllocatorComparison/FirstFit-11         	  229414	      1231 ns/op	     368 B/op	       2 allocs/op
BenchmarkAllocatorComparison/BestFit-11          	  140994	      3637 ns/op	     368 B/op	       2 allocs/op
BenchmarkAllocatorComparison/WorstFit-11         	  102294	      4207 ns/op	     368 B/op	       2 allocs/op
BenchmarkAllocatorComparison/Buddy-11            	   60682	      9281 ns/op	     409 B/op	       3 allocs/op
```

## Summary Table

| Allocator | ns/op | B/op | allocs/op | Operations/sec | Speed vs FirstFit |
|---|---|---|---|---|---|
| **FirstFit** | 1,231 | 368 | 2 | ~812K | 1.00× (baseline) |
| **BestFit** | 3,637 | 368 | 2 | ~275K | 0.34× |
| **WorstFit** | 4,207 | 368 | 2 | ~238K | 0.29× |
| **Buddy** | 9,281 | 409 | 3 | ~108K | 0.13× |
| **Slab** | ~42* | 0* | 0* | ~23.8M | 29.3× |
| **Segregated** | ~73* | 0* | 0* | ~13.7M | 16.9× |
| **Pool** | ~30* | 0* | 0* | ~33.3M | 41.0× |
| **Arena** | ~15* | 0* | 0* | ~66.7M | 82.1× |

*Slab, Segregated, Pool, and Arena benchmark numbers from previous audit (2026-06-04). Not re-measured in this audit run due to benchmark filter mismatch.

## Interpretation

### Fit-family allocators (FirstFit, BestFit, WorstFit)
- All three allocate 2 objects per operation (the block clone + the metrics snapshot)
- FirstFit is fastest because it stops at the first suitable block
- BestFit and WorstFit scan the entire free list to find the optimal block
- All use O(n) linked-list traversal

### Buddy System
- 7.5× slower than FirstFit due to recursive merge logic and level calculations
- Allocates 3 objects per operation (block + clone + metrics)
- XOR-based buddy address calculation is fast but the merge/split overhead dominates

### Slab, Segregated, Pool, Arena (O(1) allocators)
- Dramatically faster because allocation is a stack pop or bump-pointer increment
- Zero heap allocations per operation (pre-allocated free lists)
- 17-82× faster than FirstFit

## Performance Characteristics

| Allocator | Alloc Complexity | Dealloc Complexity | Coalesce | Best For |
|---|---|---|---|---|
| FirstFit | O(n) | O(1)* | Manual | Simple workloads |
| BestFit | O(n) | O(1)* | Manual | Minimal waste |
| WorstFit | O(n) | O(1)* | Manual | Large-block preservation |
| Buddy | O(log n) | O(log n) | Automatic | Power-of-2 workloads |
| Slab | O(1) | O(1) | None | Fixed-size objects |
| Segregated | O(1) avg | O(1) avg | Automatic | General-purpose |
| Pool | O(1) | O(1) | None | Object pools |
| Arena | O(1) | N/A | None | Request-scoped alloc |

*O(1) amortized with coalesce-at-dealloc

## Recommendations

1. **Use Slab** for workloads with uniform object sizes (e.g., network packets, database rows)
2. **Use Segregated** as a general-purpose allocator replacement (similar to jemalloc/tcmalloc)
3. **Use Buddy** when power-of-2 alignment is needed (e.g., page allocation)
4. **Use Pool** for fixed-size object pools (e.g., connection pools, worker pools)
5. **Use Arena** for request-scoped allocations (e.g., per-request parsing, compilation)
6. **Use FirstFit** for simplicity when performance is not critical
7. **Avoid BestFit/WorstFit** in production — they're slower than FirstFit with no practical benefit for most workloads
