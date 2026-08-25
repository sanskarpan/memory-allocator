# Fixes — Memory Allocator Simulator

**Date:** 2026-06-16
**Audit scope:** Production stabilization audit

## Summary

| Related Issue | Files Changed | Result |
|---|---|---|
| `FIT-001` | `internal/allocator/fit.go`, `internal/allocator/allocator_test.go` | Right-neighbor auto-coalescing corrected and regression-covered |
| `SEG-001`, `SEG-002` | `internal/allocator/segregated.go`, `internal/allocator/segregated_test.go` | Full capacity restored; buddy math corrected; class assumptions made explicit |
| `WS-001` | `web/server.go`, `web/server_extra_test.go` | WebSocket connection shutdown made idempotent |
| `CFG-001` | `web/server.go`, `web/server_test.go` | Port parsing and unlimited-connection mode now match documented behavior |

## Detailed Fixes

### `FIT-001` — Fit-family auto-coalescing

- Files changed:
  - `internal/allocator/fit.go`
  - `internal/allocator/allocator_test.go`
- Rationale:
  - adjacency is represented by the linked list, not `blockMap`
  - merging with the right neighbor must use `block.Next()` under the allocator lock
- Before:
  - deallocation only reliably merged with the previous free block
- After:
  - deallocation merges with the next free block via linked-list adjacency, then with the previous free block if present
- Validation:
  - `TestFirstFitAutoCoalescesWithRightNeighbor`
  - full unit and race suite

### `SEG-001` / `SEG-002` — Segregated allocator correctness

- Files changed:
  - `internal/allocator/segregated.go`
  - `internal/allocator/segregated_test.go`
- Rationale:
  - the implementation is buddy-like and requires consistent slot seeding and base-relative buddy calculations
- Before:
  - only one top-level block was seeded for the entire region
  - buddy computation XORed absolute addresses
- After:
  - top-class slots are seeded across the full region
  - buddy computation is base-relative
  - merged-away blocks are removed from `blockMap`
  - non-doubling class definitions are rejected explicitly
- Validation:
  - `TestSegregated_UsesFullCapacityAcrossTopLevelSlots`
  - `TestSegregated_PanicOnNonDoublingClasses`
  - existing segregated allocator suite

### `WS-001` — Idempotent client shutdown

- Files changed:
  - `web/server.go`
  - `web/server_extra_test.go`
- Rationale:
  - connection teardown must be safe regardless of whether it originates from slow-client removal, normal disconnect, or server shutdown
- Before:
  - multiple code paths directly closed `c.done`
- After:
  - `clientConn` owns a `sync.Once`-guarded `close()` helper
  - all shutdown/removal paths call the helper
- Validation:
  - `TestServer_ShutdownClosesClientsWithoutDoubleClosePanic`
  - web package tests and race suite

### `CFG-001` — Runtime config contract

- Files changed:
  - `web/server.go`
  - `web/server_test.go`
- Rationale:
  - environment parsing must match documented operator expectations
- Before:
  - colon-prefixed ports were mangled
  - unlimited per-IP connections were impossible
- After:
  - `MEMALLOC_PORT` accepts both `9090` and `:9090`
  - `MEMALLOC_MAX_CONN_PER_IP=0` now means unlimited
  - constructor only normalizes negative limits, not zero
- Validation:
  - `TestServer_ConfigFromEnvAllowsColonPortAndUnlimitedConnections`
  - `TestServer_UnlimitedConnectionsAllowed`
