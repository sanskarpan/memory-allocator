# Audit Log — Memory Allocator Simulator

**Date:** 2026-06-16
**Scope:** Production stabilization, QA, security, performance, and reliability audit
**Repository:** `sanskarpan/memory-allocator`

## Phase 1 — Repository Discovery

- Application shape: single Go HTTP/WebSocket server with a static frontend.
- Backend entrypoint: `cmd/server/main.go`
- Backend transport: `web/server.go`
- Frontend: `web/static/index.html`, `web/static/app.js`, `web/static/style.css`
- Domain modules:
  - `internal/allocator`: first-fit, best-fit, worst-fit, buddy, slab, segregated-fit
  - `internal/pool`: pool and arena allocators
  - `internal/simulator`: orchestration, auto-simulation, event/leak tracking
  - `internal/memory`: block and linked-list primitives
  - `internal/metrics`: thread-safe metrics snapshots
- CI asset: `.github/workflows/ci.yml`
- Deployment asset: `Dockerfile`
- External runtime dependency: `github.com/gorilla/websocket`

## Phase 2 — Startup and Environment Validation

Commands executed:

```bash
go build -o /tmp/memory-allocator-audit ./cmd/server
MEMALLOC_PORT=8091 /tmp/memory-allocator-audit
curl -sS http://localhost:8091/health
curl -sSI http://localhost:8091/
```

Observed results:

- Build succeeded.
- Server started cleanly and logged startup/version.
- `/health` returned `{"clients":0,"simulatorReady":false,"status":"healthy"}`.
- Static responses included CSP and other security headers.
- Ctrl-C shutdown path completed cleanly.

## Phase 3 — Static and Behavioral Audit Findings

Confirmed issues found in the current codebase:

1. `FIT-001` Medium
   - Area: `internal/allocator/fit.go`
   - Finding: first/best/worst-fit deallocation only merged with the left neighbor reliably; right-neighbor merge logic incorrectly relied on `blockMap`, which tracks allocated blocks, not free list neighbors.

2. `SEG-001` Medium
   - Area: `internal/allocator/segregated.go`
   - Finding: the segregated allocator seeded the top class with a single full-region block instead of one block per top-class slot, which silently discarded usable capacity for sizes greater than one top-class unit.

3. `SEG-002` Low
   - Area: `internal/allocator/segregated.go`
   - Finding: buddy-address math used raw XOR against absolute addresses instead of base-relative offsets, which is only accidentally correct for some base/class combinations.

4. `WS-001` Medium
   - Area: `web/server.go`
   - Finding: client connection shutdown used raw `close(c.done)` from multiple paths, allowing double-close panics during shutdown/removal races.

5. `CFG-001` Low
   - Area: `web/server.go`
   - Finding: `MEMALLOC_PORT=:9090` produced `::9090`, and `MEMALLOC_MAX_CONN_PER_IP=0` did not actually mean unlimited despite the documented contract.

## Phase 4 — Frontend Audit

Static review and API-flow review completed for:

- allocator initialization flow
- start/pause/resume/stop/reset flow
- allocate/deallocate/coalesce/detect-leaks flow
- reconnect flow
- block selection and keyboard navigation flow

Observations:

- frontend now has keyboard selection/deallocation paths and screen-reader summaries
- owner strings are escaped before DOM insertion
- reconnect backoff and button-state transitions are coherent
- no new confirmed frontend correctness bug was found in this pass

Residual note:

- this pass did not run a full browser-based multi-breakpoint visual QA matrix; confidence is based on code review plus server/E2E validation

## Phase 5–12 — API, Concurrency, Resource, and Reliability Validation

Commands executed:

```bash
go test ./...
go vet ./...
go test -race -count=1 ./...
gofmt -l .
```

Observed results:

- all tests passed
- `go vet` passed cleanly
- race detector passed cleanly
- formatting check passed cleanly after updates

Specific concurrency and lifecycle checks validated:

- simulator replacement no longer leaves stale update senders active
- WebSocket shutdown/removal path is idempotent
- unlimited per-IP connection mode (`MaxConnPerIP=0`) now behaves as documented

## Phase 13–20 — Benchmark and Regression Evidence

Commands executed:

```bash
go test -run=^$ -bench='Benchmark(FirstFit|BestFit|WorstFit|Buddy)(Allocation|Deallocation)$' -benchmem -benchtime=100ms ./internal/allocator
go test -run=^$ -bench='BenchmarkAllocatorComparison$' -benchmem -benchtime=100ms ./internal/allocator
```

Observed benchmark highlights:

- `BenchmarkFirstFitAllocation`: `493.3 ns/op`
- `BenchmarkBestFitAllocation`: `501.5 ns/op`
- `BenchmarkWorstFitAllocation`: `508.3 ns/op`
- `BenchmarkBuddyAllocation`: `757.8 ns/op`
- `BenchmarkAllocatorComparison/FirstFit`: `637.1 ns/op`
- `BenchmarkAllocatorComparison/BestFit`: `441.2 ns/op`
- `BenchmarkAllocatorComparison/WorstFit`: `508.6 ns/op`
- `BenchmarkAllocatorComparison/Buddy`: `513.7 ns/op`

Regression tests added in this audit:

- fit-family right-neighbor auto-coalescing regression
- segregated full-capacity regression for multi-slot top-class regions
- segregated invalid non-doubling class definition regression
- config parsing regressions for colon-prefixed ports and unlimited connections
- server shutdown double-close regression

## Final Audit Position

- Repo visibility: public
- Current working tree: contains audited fixes and matching regression tests
- Confirmed code/runtime issues remaining after fixes: none at critical/high/medium severity
- Remaining risks are operational or scope-based, not confirmed correctness defects
