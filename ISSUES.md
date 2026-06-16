# Issues — Memory Allocator Simulator

**Date:** 2026-06-16
**Audit scope:** Production stabilization audit

## Issue Registry

| ID | Severity | Title | Status |
|---|---|---|---|
| `FIT-001` | Medium | Fit-family allocators fail to auto-coalesce with right neighbor | Fixed |
| `SEG-001` | Medium | Segregated allocator loses capacity for memory larger than one top-class slot | Fixed |
| `SEG-002` | Low | Segregated buddy-address math is not base-relative | Fixed |
| `WS-001` | Medium | WebSocket client shutdown can double-close channels | Fixed |
| `CFG-001` | Low | Config parsing breaks colon-prefixed ports and unlimited connection mode | Fixed |

## Detailed Issues

### `FIT-001`

- Severity: Medium
- Affected components: `internal/allocator/fit.go`
- Symptoms: freed blocks could remain artificially fragmented until a later explicit coalesce or favorable allocation pattern.
- Root cause: right-neighbor merge logic looked up `blockMap[block.EndAddress()]`, but `blockMap` only contains allocated blocks. Free neighbors are tracked by linked-list adjacency, not by that map.
- Impact: lower effective free-space consolidation and incorrect allocator behavior under common free-order patterns.
- Reproduction:
  1. Allocate two adjacent blocks.
  2. Free the right block.
  3. Free the left block.
  4. Attempt to allocate the combined size.
- Validation evidence: new regression `TestFirstFitAutoCoalescesWithRightNeighbor`, plus `go test ./...` and `go test -race -count=1 ./...`.

### `SEG-001`

- Severity: Medium
- Affected components: `internal/allocator/segregated.go`
- Symptoms: a large segregated allocator could exhaust after using only a fraction of total configured memory.
- Root cause: initialization seeded a single top-level free block spanning the whole rounded region instead of one top-class block per top-class slot.
- Impact: silent underutilization of memory and incorrect allocator capacity.
- Reproduction:
  1. Create a segregated allocator with `8192` bytes.
  2. Repeatedly allocate `64`-byte blocks.
  3. Observe exhaustion far earlier than the expected `128` allocations.
- Validation evidence: new regression `TestSegregated_UsesFullCapacityAcrossTopLevelSlots`.

### `SEG-002`

- Severity: Low
- Affected components: `internal/allocator/segregated.go`
- Symptoms: merge behavior depends on address/base coincidence rather than correct buddy math.
- Root cause: buddy address used `block.Address ^ uintptr(class)` instead of base-relative XOR.
- Impact: latent merge-address correctness bug and fragile implementation assumptions.
- Reproduction: inspect merge logic for non-zero base address; compare with buddy allocator’s correct base-relative formula.
- Validation evidence: included in the same segregated allocator regression pass after the fix.

### `WS-001`

- Severity: Medium
- Affected components: `web/server.go`
- Symptoms: shutdown/removal paths could panic under overlapping cleanup because multiple code paths directly closed the same `done` channel.
- Root cause: channel lifecycle was not idempotent.
- Impact: server panic risk during shutdown or slow-client removal races.
- Reproduction:
  1. Connect a client.
  2. Trigger server shutdown while connection cleanup is in progress.
  3. Observe double-close risk in pre-fix code path.
- Validation evidence: new regression `TestServer_ShutdownClosesClientsWithoutDoubleClosePanic`.

### `CFG-001`

- Severity: Low
- Affected components: `web/server.go`
- Symptoms:
  - `MEMALLOC_PORT=:9090` became `::9090`
  - `MEMALLOC_MAX_CONN_PER_IP=0` did not behave as unlimited
- Root cause: config parser always prefixed `:` to port values and constructor logic rewrote non-positive connection limits to the default.
- Impact: documented runtime contract did not match behavior.
- Reproduction:
  1. Set `MEMALLOC_PORT=:9090`.
  2. Set `MEMALLOC_MAX_CONN_PER_IP=0`.
  3. Read parsed config.
- Validation evidence: new regressions `TestServer_ConfigFromEnvAllowsColonPortAndUnlimitedConnections` and `TestServer_UnlimitedConnectionsAllowed`.
