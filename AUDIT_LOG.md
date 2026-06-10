# Audit Log — Memory Allocator Simulator

**Date:** 2026-06-09
**Auditor:** opencode (automated full-system audit)
**Scope:** Full production readiness audit across 22 phases

---

## Phase 1 — Repository Discovery

- 12 Go source files, 14 test files, 3 frontend files
- 8 memory allocation algorithms: FirstFit, BestFit, WorstFit, Buddy, Slab, SegregatedFit, Pool, Arena
- Single external dependency: `gorilla/websocket v1.5.3`
- WebSocket-based server with vanilla JS frontend
- Previous audit (2026-06-04) fixed ~70 issues across 2 passes

## Phase 2 — Environment Validation

- Build: `go build ./...` — **PASS**
- Vet: `go vet ./...` — **PASS**
- Fmt: `gofmt -l .` — **PASS** (no unformatted files)
- Module verify: `go mod verify` — **PASS**
- Server startup: health endpoint returns `{"status":"healthy"}` — **PASS**
- Graceful shutdown: SIGTERM triggers clean exit — **PASS**
- Port selection: server picks available port when 8080 is in use — **PASS** (expected behavior)

## Phase 3 — Static Code Audit

### Issues Found (new, not in previous audit)

1. **CalculateFragmentation TOCTOU** — `BaseAllocator.CalculateFragmentation()` releases `ba.mu` before calling `metrics.UpdateFragmentation()`. Between the release and the metrics update, concurrent allocations can change the block list, making the fragmentation value stale. Severity: **Medium** (correctness, not safety).

2. **broadcastUpdate TOCTOU** — `Simulator.broadcastUpdate()` does `select { case <-s.done: return; default: }` then proceeds to call `cb(update)`. If `Stop()` is called between the check and the callback invocation, and the callback sends to `s.broadcast` (which is closed), a send-on-closed-channel panic can occur. Severity: **Low** (narrow window, existing done-check in callback mitigates).

3. **Dockerfile main.version** — Dockerfile passes `-X main.version=${VERSION}` but `cmd/server/main.go` has no `version` variable. The ldflags silently no-op. Severity: **Low** (no functional impact).

4. **Arena Reset metrics ordering** — `ArenaAllocator.Reset()` calls `RecordDeallocation` then `Reset()` on metrics, briefly showing a deallocation event that's immediately wiped. Severity: **Low** (cosmetic).

5. **Frontend accessibility** — Canvas has no `aria-label`, `role`, or keyboard interaction. Form inputs lack `aria-describedby`. Tab order not verified. Severity: **Medium** (accessibility compliance).

6. **No CSP/SRI headers** — Frontend serves JS without Content-Security-Policy or Subresource Integrity. Severity: **Low** (demo app, not handling auth/sensitive data).

7. **No per-IP WebSocket rate limiting** — A single client can open many connections. Server relies on reverse proxy for rate limiting. Severity: **Low** (production deployment should use a reverse proxy).

8. **Pool allocator unused variable** — `slab.go:203` has `_ = class` which is a no-op suppressor. Not a bug but dead code. Severity: **Informational**.

### Verified Clean

- All allocator implementations correctly hold locks during mutations
- Clone-on-return pattern consistently applied across all allocators
- Linked list operations maintain sorted order invariant
- Buddy allocator level calculations correct (verified: 64→0, 8192→7, 16384→8)
- No nil pointer dereferences in normal code paths
- No integer overflow risks in practice (sizes bounded by config)
- No injection vectors (WebSocket messages parsed as JSON maps)
- No secrets or credentials in code
- No hardcoded ports (configurable via env)

## Phase 4-8 — Frontend, API, Security, Performance Audit

### Frontend

- Canvas visualization renders correctly for all block states
- Button state machine properly reflects connected/initialized/running/paused states
- Reconnect with exponential backoff (max 10 attempts, capped at 30s)
- XSS prevention via `escapeHtml()` on user-provided owner strings
- Event log capped at 50 entries (client-side)
- No memory leaks in long-running sessions (DOM elements properly managed)

### API (WebSocket)

- All 13 message types handled: init, start, pause, resume, stop, reset, allocate, deallocate, coalesce, detectLeaks, speed, getState
- Input validation on size (>0), address (float64→uintptr), allocator type
- Error messages returned for invalid operations
- No SQL/NoSQL injection (no database)
- No command injection
- Max message size enforced (1 MiB)

### Security

- WebSocket origin check configurable via `AllowedOrigins` config
- Docker runs as nonroot (UID 65532)
- Distroless final image (no shell, minimal attack surface)
- No secrets in code or environment defaults
- No unsafe deserialization

### Performance

- Benchmark results consistent across 3 runs
- No unbounded memory growth (event log capped at 200, block list bounded by memory size)
- No goroutine leaks (context-based lifecycle, done channels)
- No file handle or connection leaks (proper close/defer patterns)

## Phase 9-20 — Concurrency, Stress, Regression

- Race detector: **PASS** (3 consecutive runs, all packages)
- Fuzz testing: **PASS** (30s, 2606 iterations, 0 invariant violations, 1 new interesting input)
- Stress test: 20 rapid re-inits without crash — **PASS**
- Parallel servers: 4 servers, 4 origins, no clobber — **PASS**
- Concurrent alloc/dealloc: no data races — **PASS**
- E2E flow: init→alloc→dealloc→coalesce→reset→health — **PASS**

## Evidence Collected

- `go test -race -count=3` output: all PASS
- `go test -fuzz=... -fuzztime=30s` output: all PASS
- `go vet ./...` output: clean
- `gofmt -l .` output: clean (empty)
- Server health check: `{"status":"healthy","clients":0,"simulatorReady":false}`
- Graceful shutdown: exit code 0
