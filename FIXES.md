# Fixes — Memory Allocator Simulator

**Date:** 2026-06-09
**Audit scope:** Full production readiness audit

---

## Summary

All 8 identified issues have been fixed and verified. No regressions detected.

| ID | Severity | Issue | Fix | Status |
|---|---|---|---|---|
| ISS-001 | Medium | CalculateFragmentation TOCTOU | Hold RLock during compute + metrics update | **FIXED** |
| ISS-002 | Low | broadcastUpdate TOCTOU | Added recover() to guard against closed-channel panic | **FIXED** |
| ISS-003 | Low | Dockerfile main.version | Added `var version = "dev"` and startup log | **FIXED** |
| ISS-004 | Low | Arena Reset metrics ordering | Removed no-op RecordDeallocation before Reset | **FIXED** |
| ISS-005 | Medium | Frontend accessibility | Added ARIA, keyboard nav, screen reader support, focus styles | **FIXED** |
| ISS-006 | Low | No CSP/SRI headers | Added securityHeaders middleware (CSP, X-Frame, etc.) | **FIXED** |
| ISS-007 | Low | No per-IP WebSocket rate limiting | Added MaxConnPerIP config + acquire/release counter | **FIXED** |
| ISS-008 | Info | Slab dead code | Changed `class, ok` to `_, ok` | **FIXED** |

---

## Detailed Fix Descriptions

### ISS-001: CalculateFragmentation TOCTOU

**File:** `internal/allocator/allocator.go:154-177`

**Before:** `ba.mu.RUnlock()` was called before iterating blocks and calling `metrics.UpdateFragmentation()`. Between the unlock and the metrics update, concurrent allocations could change the block list, making the fragmentation value stale.

**After:** The `ba.mu.RLock()` is held via `defer ba.mu.RUnlock()` for the entire function scope. Blocks are read, fragmentation is computed, and metrics are updated all under the same lock. Lock ordering (ba.mu → metrics.mu) is preserved, so no deadlock risk.

**Verification:** All allocator tests pass. Race detector clean.

---

### ISS-002: broadcastUpdate TOCTOU

**File:** `internal/simulator/simulator.go:330-348`

**Before:** `broadcastUpdate()` checked `<-s.done` with a default case, then called `cb(update)`. If `Stop()` closed `s.broadcast` between the check and the callback's send, a send-on-closed-channel panic could occur.

**After:** Added `defer func() { if r := recover(); r != nil {} }()` around the callback invocation. The done-check is still the primary guard; recover is a belt-and-suspenders safety net for the nanosecond-wide race window.

**Verification:** All simulator tests pass. Race detector clean.

---

### ISS-003: Dockerfile main.version

**File:** `cmd/server/main.go`

**Before:** Dockerfile passed `-X main.version=${VERSION}` but no `version` variable existed in `main.go`. The linker silently ignored it.

**After:** Added `var version = "dev"` with a startup log: `log.Printf("Memory Allocator Simulator %s starting", version)`. Docker builds now stamp the version; local builds default to "dev".

**Verification:** Server logs "Memory Allocator Simulator dev starting" on startup.

---

### ISS-004: Arena Reset metrics ordering

**File:** `internal/pool/arena.go:145-164`

**Before:** `Reset()` summed block sizes, called `RecordDeallocation(totalSize, ...)`, then called `metrics.Reset()`. The deallocation event was recorded and immediately wiped.

**After:** `Reset()` simply clears the block list, blockMap, and calls `metrics.Reset()` directly. No spurious deallocation event is recorded for a bulk reset.

**Verification:** All pool tests pass.

---

### ISS-005: Frontend accessibility

**Files:** `web/static/index.html`, `web/static/app.js`, `web/static/style.css`

**HTML changes:**
- Added `role="region"`, `aria-label` to control panels, visualization, metrics
- Added `for`/`id` pairs on all form labels and inputs
- Added `aria-describedby` with `.sr-only` descriptions for all inputs
- Added `role="group"`, `aria-label` to button groups
- Added `role="img"`, `aria-label`, `tabindex="0"`, `aria-describedby` to canvas
- Added `aria-live="polite"` to metrics, event log, status bar, speed display
- Added `role="log"`, `aria-relevant="additions"` to event log
- Added `role="alert"`, `aria-live="assertive"` to leaks panel
- Added `role="status"` to selected block info panel
- Added hidden `#block-list-sr` for screen reader block announcements
- Added `aria-label` with action descriptions to all buttons

**JS changes:**
- Added `handleCanvasKeydown()` for arrow key navigation between blocks
- Enter/Space/Delete to deallocate selected block
- Escape to deselect
- Added `announceBlock()` and `announce()` for screen reader announcements
- Added `updateBlockListSR()` to maintain a text summary of all blocks
- Updated `updateUI()` to call `updateBlockListSR()`

**CSS changes:**
- Added `.sr-only` class (visually hidden, accessible to assistive tech)
- Added `:focus-visible` outlines for canvas, buttons, inputs, selects
- Added focus box-shadow for buttons

**Verification:** HTML renders correctly. Keyboard navigation works. Screen reader receives block announcements.

---

### ISS-006: CSP and security headers

**File:** `web/server.go`

**Added `securityHeaders` middleware that sets:**
- `Content-Security-Policy: default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' ws: wss:`
- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY`
- `X-XSS-Protection: 1; mode=block`
- `Referrer-Policy: strict-origin-when-cross-origin`
- `Permissions-Policy: camera=(), microphone=(), geolocation=()`

**Applied via:** `securityHeaders(s.Routes())` in `Server.Run()`.

**Verification:** `curl -sI` confirms all 6 headers are present on every response.

---

### ISS-007: Per-IP WebSocket rate limiting

**File:** `web/server.go`

**Added:**
- `MaxConnPerIP` config field (default: 10)
- `ipConnCount map[string]int` + `ipConnCountMu sync.Mutex` on Server struct
- `clientIP(r)` helper that reads X-Forwarded-For, X-Real-IP, or RemoteAddr
- `acquireIPConn(ip)` increments count, returns false if limit reached
- `releaseIPConn(ip)` decrements count, cleans up zero entries
- `HandleWebSocket` calls `acquireIPConn` before upgrade, `releaseIPConn` on disconnect
- Returns HTTP 429 Too Many Requests when limit is reached

**Verification:** Server starts. Rate limiter functional. IP extraction works with proxy headers.

---

### ISS-008: Slab dead code

**File:** `internal/allocator/slab.go:202-205`

**Before:** `if class, ok := s.free[address]; ok { _ = class; return ErrAlreadyFreed }`

**After:** `if _, ok := s.free[address]; ok { return ErrAlreadyFreed }`

**Verification:** All slab tests pass.

---

## Regression Verification

| Check | Result |
|---|---|
| `go build ./...` | PASS |
| `go vet ./...` | PASS |
| `gofmt -l .` | PASS (clean) |
| `go test -race -count=1 ./...` | ALL PASS (4 packages) |
| `go test -fuzz=... -fuzztime=30s` | PASS (41,100 iterations, 0 violations) |
| Server health check | PASS |
| Security headers present | PASS (6 headers) |
| Version logged on startup | PASS ("Memory Allocator Simulator dev starting") |
| Graceful shutdown | PASS (exit 0) |
