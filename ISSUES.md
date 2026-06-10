# Issues — Memory Allocator Simulator

**Date:** 2026-06-09
**Audit scope:** Full production readiness audit

---

## Issue Registry

| ID | Severity | Title | Affected Component | Status |
|---|---|---|---|---|
| ISS-001 | Medium | CalculateFragmentation releases lock before metrics update | `internal/allocator/allocator.go` | **FIXED** |
| ISS-002 | Low | broadcastUpdate TOCTOU with closed channel | `internal/simulator/simulator.go` | **FIXED** |
| ISS-003 | Low | Dockerfile references nonexistent main.version | `Dockerfile` + `cmd/server/main.go` | **FIXED** |
| ISS-004 | Low | Arena Reset records deallocation before clearing metrics | `internal/pool/arena.go` | **FIXED** |
| ISS-005 | Medium | Frontend lacks accessibility (ARIA, keyboard nav) | `web/static/*` | **FIXED** |
| ISS-006 | Low | No CSP or SRI headers on frontend | `web/server.go` | **FIXED** |
| ISS-007 | Low | No per-IP WebSocket rate limiting | `web/server.go` | **FIXED** |
| ISS-008 | Info | Slab allocator dead code (`_ = class`) | `internal/allocator/slab.go` | **FIXED** |

---

## Detailed Descriptions

### ISS-001: CalculateFragmentation TOCTOU

**Severity:** Medium
**Component:** `internal/allocator/allocator.go:154-177`
**Description:** `BaseAllocator.CalculateFragmentation()` acquires `ba.mu.RLock()`, calls `ba.blocks.GetBlocks()` (which clones blocks), releases `ba.mu.RUnlock()`, then iterates over the cloned blocks to compute fragmentation. After computing, it calls `ba.metrics.UpdateFragmentation(frag)`. Between the RUnlock and the metrics update, concurrent allocations/deallocations can change the actual block list, making the computed fragmentation value stale.
**Impact:** Fragmentation metric may be slightly inaccurate for a brief window. No data corruption or crash risk.
**Root cause:** Lock released too early to avoid holding allocator lock during metrics write.
**Reproduction:** Run concurrent allocation + fragmentation calculation. Observe that the fragmentation metric occasionally reflects a slightly older state.

### ISS-002: broadcastUpdate TOCTOU

**Severity:** Low
**Component:** `internal/simulator/simulator.go:330-348`
**Description:** `broadcastUpdate()` checks `<-s.done` with a default case, then proceeds to call the callback. If `Stop()` closes the done channel and the broadcast channel between the check and the callback's send to `s.broadcast`, a send-on-closed-channel panic occurs.
**Impact:** Theoretical panic in a very narrow timing window. The callback's own done-check and the broadcast goroutine's draining behavior make this extremely unlikely.
**Root cause:** Channel close and send not atomic.
**Mitigation already in place:** Callback checks `callbackDone` and `sim.Done()` before sending. Broadcast goroutine uses non-blocking send.

### ISS-003: Dockerfile main.version

**Severity:** Low
**Component:** `Dockerfile:32`
**Description:** The Dockerfile passes `-ldflags="-s -w -X main.version=${VERSION}"` but `cmd/server/main.go` has no `version` variable. The linker silently ignores the undefined variable.
**Impact:** No functional impact. Version stamping is not applied to the binary.
**Root cause:** Variable was planned but never declared.

### ISS-004: Arena Reset metrics ordering

**Severity:** Low
**Component:** `internal/pool/arena.go:145-164`
**Description:** `ArenaAllocator.Reset()` iterates over blocks to sum sizes, calls `RecordDeallocation(totalSize, ...)`, then calls `metrics.Reset()`. The deallocation event is recorded and immediately wiped. There's a brief window where metrics show a deallocation that didn't meaningfully happen.
**Impact:** Metrics snapshot taken during Reset may show inconsistent state. No functional impact.
**Root cause:** Reset should either record the bulk deallocation OR reset metrics, not both.

### ISS-005: Frontend accessibility

**Severity:** Medium
**Components:** `web/static/index.html`, `web/static/app.js`
**Description:** The canvas visualization lacks `role="img"`, `aria-label`, and keyboard interaction handlers. Form inputs lack `aria-describedby` for error guidance. The block info panel uses raw `innerHTML` without semantic structure. No focus management for dynamic content updates.
**Impact:** Users relying on screen readers or keyboard navigation cannot fully use the application. WCAG 2.1 AA compliance gap.
**Root cause:** Accessibility was not a design requirement for the initial implementation.

### ISS-006: No CSP/SRI headers

**Severity:** Low
**Component:** `web/server.go`
**Description:** The server does not set Content-Security-Policy, X-Content-Type-Options, or X-Frame-Options headers. Frontend JS is served without Subresource Integrity.
**Impact:** Reduced protection against XSS and code injection in a production deployment. Acceptable for a demo/educational tool.
**Root cause:** Security headers not implemented in the HTTP handler.

### ISS-007: No per-IP WebSocket rate limiting

**Severity:** Low
**Component:** `web/server.go`
**Description:** A single client IP can open multiple WebSocket connections. The server disconnects slow clients but does not limit connection count per IP.
**Impact:** Potential resource exhaustion under deliberate abuse. Should be handled at the reverse proxy level.
**Root cause:** Rate limiting deferred to deployment infrastructure.

### ISS-008: Slab allocator dead code

**Severity:** Informational
**Component:** `internal/allocator/slab.go:203`
**Description:** `_ = class` in `Deallocate` is a dead assignment. The variable is assigned from `s.free[address]` but never used.
**Impact:** No functional impact. Minor code cleanliness issue.
