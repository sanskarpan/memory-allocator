# Test Results — Memory Allocator Simulator

**Date:** 2026-06-09
**Environment:** macOS arm64, Apple M3 Pro, Go 1.26.1

---

## 1. Unit & Integration Tests (with race detector)

```
ok  github.com/sanskar/memory-allocator/internal/allocator   5.198s
ok  github.com/sanskar/memory-allocator/internal/pool        1.862s
ok  github.com/sanskar/memory-allocator/internal/simulator   3.140s
ok  github.com/sanskar/memory-allocator/web                  4.061s
```

**Result:** ALL PASS (3 consecutive runs, race detector enabled)

### Test Breakdown by Package

| Package | Test Files | Test Functions | Status |
|---|---|---|---|
| `internal/allocator` | 5 | ~55 | PASS |
| `internal/pool` | 1 | ~10 | PASS |
| `internal/simulator` | 2 | ~11 | PASS |
| `web` | 3 | ~16 | PASS |

### Test Categories Covered

- **Unit tests:** Basic alloc/dealloc for all 8 allocators
- **Regression tests:** Chain coalesce, discontiguous coalesce, buddy merge, list sorting
- **Race tests:** Concurrent alloc/dealloc with `-race` flag
- **Fuzz tests:** Invariant checking across all allocators
- **E2E tests:** Full WebSocket flow (init → alloc → dealloc → coalesce → reset → health)
- **Stress tests:** 20 rapid re-inits, 4 parallel servers, 500 sequential allocs
- **Benchmark tests:** Performance comparison across all allocators

---

## 2. Fuzz Testing

```
fuzz: elapsed: 31s, execs: 2606 (103/sec), new interesting: 1 (total: 188)
PASS
```

**Duration:** 30 seconds
**Iterations:** 2,606
**Workers:** 11
**Invariant violations:** 0
**New interesting inputs:** 1 (added to seed corpus)

### Fuzz Invariants Checked

For every allocator, after every operation:
1. Sum of allocated sizes ≥ 0
2. All block addresses are unique
3. No block appears in both free and allocated state simultaneously
4. Free block count + allocated block count = total block count
5. Total free bytes + total allocated bytes ≤ total memory size

---

## 3. Benchmarks

### Allocator Comparison (Apple M3 Pro, 200ms per benchmark)

| Allocator | ns/op | B/op | allocs/op | Relative Speed |
|---|---|---|---|---|
| FirstFit | 1,231 | 368 | 2 | 1.0× (baseline) |
| BestFit | 3,637 | 368 | 2 | 0.34× |
| WorstFit | 4,207 | 368 | 2 | 0.29× |
| Buddy | 9,281 | 409 | 3 | 0.13× |

**Note:** Slab and Segregated benchmarks were not captured in this run (benchmark names didn't match the filter). Previous audit recorded Slab at ~42 ns/op and Segregated at ~73 ns/op.

---

## 4. Static Analysis

| Check | Result |
|---|---|
| `go vet ./...` | PASS (clean) |
| `gofmt -l .` | PASS (no unformatted files) |
| `go mod verify` | PASS (all modules verified) |
| `go build ./...` | PASS (clean build) |

---

## 5. Runtime Validation

| Check | Result |
|---|---|
| Server startup | PASS (health endpoint returns JSON) |
| Graceful shutdown (SIGTERM) | PASS (exit code 0) |
| Port selection | PASS (picks available port) |
| WebSocket upgrade | PASS |
| All 8 allocator types via WebSocket | PASS |

---

## 6. Coverage

| Package | Coverage (approximate) |
|---|---|
| `internal/allocator` | High (55+ unit tests + fuzz + race) |
| `internal/pool` | High (10 unit tests + concurrent) |
| `internal/simulator` | Medium (11 tests, all allocator types) |
| `web` | High (16 tests including E2E, stress, parallel) |
| `internal/memory` | Not directly tested (tested through allocators) |
| `internal/metrics` | Not directly tested (tested through allocators) |

---

## 7. Regression Matrix

| Scenario | Test | Status |
|---|---|---|
| FirstFit basic flow | allocator_test.go | PASS |
| BestFit basic flow | allocator_test.go | PASS |
| WorstFit basic flow | allocator_test.go | PASS |
| Buddy alloc/dealloc/merge | buddy_test.go | PASS |
| Slab alloc/dealloc/exhaust | slab_test.go | PASS |
| Segregated alloc/dealloc/merge | segregated_test.go | PASS |
| Pool alloc/dealloc/exhaust | pool_test.go | PASS |
| Arena alloc/no-dealloc/reset | pool_test.go | PASS |
| Chain coalesce | allocator_race_test.go | PASS |
| Discontiguous coalesce | allocator_race_test.go | PASS |
| Buddy higher-level merge | allocator_race_test.go | PASS |
| List sorted after merge | allocator_race_test.go | PASS |
| Concurrent FirstFit | allocator_race_test.go | PASS |
| Concurrent Buddy | allocator_race_test.go | PASS |
| Simulator lifecycle | simulator_test.go | PASS |
| SetSpeed affects tick | simulator_set_speed_test.go | PASS |
| Done channel lifecycle | simulator_set_speed_test.go | PASS |
| No broadcast after stop | simulator_set_speed_test.go | PASS |
| Init stops previous sim | server_extra_test.go | PASS |
| Reinit while running | server_extra_test.go | PASS |
| Stress reinit (20 iterations) | server_extra_test.go | PASS |
| Parallel servers | server_extra_test.go | PASS |
| E2E full flow | e2e_test.go | PASS |
| Fuzz all allocators | allocator_fuzz_test.go | PASS |
