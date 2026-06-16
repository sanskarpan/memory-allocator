# Benchmarks — Memory Allocator Simulator

**Date:** 2026-06-16
**Environment:** macOS arm64, Apple M3 Pro
**Method:** targeted Go benchmarks with `-benchmem -benchtime=100ms`

## Measured Results

### Targeted allocation/deallocation benchmarks

| Benchmark | Result |
|---|---|
| `BenchmarkFirstFitAllocation` | `493.3 ns/op`, `368 B/op`, `2 allocs/op` |
| `BenchmarkFirstFitDeallocation` | `3029 ns/op`, `160 B/op`, `1 allocs/op` |
| `BenchmarkBestFitAllocation` | `501.5 ns/op`, `368 B/op`, `2 allocs/op` |
| `BenchmarkWorstFitAllocation` | `508.3 ns/op`, `368 B/op`, `2 allocs/op` |
| `BenchmarkBuddyAllocation` | `757.8 ns/op`, `409 B/op`, `3 allocs/op` |
| `BenchmarkBuddyDeallocation` | `978.7 ns/op`, `168 B/op`, `2 allocs/op` |

### Allocator comparison benchmark

| Benchmark | Result |
|---|---|
| `BenchmarkAllocatorComparison/FirstFit` | `637.1 ns/op`, `368 B/op`, `2 allocs/op` |
| `BenchmarkAllocatorComparison/BestFit` | `441.2 ns/op`, `368 B/op`, `2 allocs/op` |
| `BenchmarkAllocatorComparison/WorstFit` | `508.6 ns/op`, `368 B/op`, `2 allocs/op` |
| `BenchmarkAllocatorComparison/Buddy` | `513.7 ns/op`, `409 B/op`, `3 allocs/op` |

## Interpretation

- The fixes in this audit were correctness and lifecycle fixes, not algorithmic rewrites, so no material performance regression was expected.
- Fit-family allocators remain in the same rough performance band.
- Buddy remains slower on direct allocation paths than fit-family allocators in the targeted benchmark.
- No benchmark result indicates a regression severe enough to block release for this project’s educational/simulation use case.

## Caveats

- These are short targeted runs, not long statistical performance studies.
- Slab, segregated, pool, and arena were not re-benchmarked in this pass because the prompt-driven audit focused on confirmed correctness defects and targeted regression evidence.
