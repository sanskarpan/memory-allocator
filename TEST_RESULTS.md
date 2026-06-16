# Test Results — Memory Allocator Simulator

**Date:** 2026-06-16
**Environment:** macOS arm64, Apple M3 Pro

## Commands Executed

```bash
go test ./...
go vet ./...
go test -race -count=1 ./...
gofmt -l .
go build -o /tmp/memory-allocator-audit ./cmd/server
MEMALLOC_PORT=8091 /tmp/memory-allocator-audit
curl -sS http://localhost:8091/health
curl -sSI http://localhost:8091/
go test -run=^$ -bench='Benchmark(FirstFit|BestFit|WorstFit|Buddy)(Allocation|Deallocation)$' -benchmem -benchtime=100ms ./internal/allocator
go test -run=^$ -bench='BenchmarkAllocatorComparison$' -benchmem -benchtime=100ms ./internal/allocator
```

## Results

### Static and correctness validation

| Check | Result |
|---|---|
| `go test ./...` | Pass |
| `go vet ./...` | Pass |
| `go test -race -count=1 ./...` | Pass |
| `gofmt -l .` | Pass |
| `go build -o /tmp/memory-allocator-audit ./cmd/server` | Pass |

### Runtime validation

| Check | Result |
|---|---|
| Server startup | Pass |
| `/health` endpoint | Pass |
| Security headers on `/` | Pass |
| Graceful interrupt shutdown | Pass |

Observed health payload:

```json
{"clients":0,"simulatorReady":false,"status":"healthy"}
```

Observed response headers included:

- `Content-Security-Policy`
- `Permissions-Policy`
- `Referrer-Policy`
- `X-Content-Type-Options`
- `X-Frame-Options`
- `X-Xss-Protection`

### Regression scope validated

| Scenario | Evidence | Result |
|---|---|---|
| Fit-family right-neighbor auto-coalescing | `TestFirstFitAutoCoalescesWithRightNeighbor` | Pass |
| Segregated allocator full-capacity usage | `TestSegregated_UsesFullCapacityAcrossTopLevelSlots` | Pass |
| Segregated class contract enforcement | `TestSegregated_PanicOnNonDoublingClasses` | Pass |
| Colon-prefixed port parsing | `TestServer_ConfigFromEnvAllowsColonPortAndUnlimitedConnections` | Pass |
| Unlimited per-IP connections | `TestServer_UnlimitedConnectionsAllowed` | Pass |
| Shutdown no double-close panic | `TestServer_ShutdownClosesClientsWithoutDoubleClosePanic` | Pass |

## Critical Flow Coverage

| Flow | Coverage |
|---|---|
| Allocator initialization | Existing web tests |
| Allocate / deallocate | Existing allocator + web tests |
| Coalesce | Existing allocator tests plus regression coverage |
| Simulator lifecycle | Existing simulator tests |
| WebSocket reconnect/reinit | Existing `web/server_extra_test.go` |
| Startup / health / shutdown | Manual runtime validation in this audit |

## Gaps

- No browser-automation pass was run for a full visual responsive matrix in this audit.
- No long soak test was run beyond standard test/runtime coverage.
