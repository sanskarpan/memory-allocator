# Memory Allocator Simulator

A production-level, interactive memory allocation simulator with real-time visualization. This project implements multiple memory allocation algorithms and provides a comprehensive web-based interface for understanding how different strategies perform under various workloads.

> **Audit & stabilization (June 2026)**: the codebase has been hardened for production — see [`AUDIT_REPORT.md`](./AUDIT_REPORT.md) for the full list of fixes (buddy level-formula bug, Reset deadlock, broadcast race, graceful shutdown, etc.).

## Features

### Memory Allocation Algorithms

1. **First Fit** - Allocates the first free block that is large enough
2. **Best Fit** - Allocates the smallest free block that fits
3. **Worst Fit** - Allocates the largest free block
4. **Buddy System** - Power-of-2 allocation with efficient coalescing
5. **Slab Allocator** - Per-size-class object caches (Linux SLUB style)
6. **Segregated Fit** - Per-class free lists with on-demand splitting
7. **Pool Allocator** - Fixed-size block allocation (object pool pattern)
6. **Arena Allocator** - Bump-pointer allocation with bulk deallocation

### Core Capabilities

- **Real-time Visualization** - Interactive canvas-based memory layout display
- **Automatic & Manual Modes** - Run automated simulations or control manually
- **Performance Metrics** - Track allocations, fragmentation, utilization, latency
- **Memory Leak Detection** - Identify blocks allocated for too long
- **Fragmentation Tracking** - Monitor external fragmentation in real-time
- **Coalescing** - Automatic merging of adjacent free blocks
- **Thread-Safe** - Concurrent allocations supported with proper synchronization
- **Comprehensive Testing** - 22 unit tests and performance benchmarks

## Architecture

```
Memory-Allocator/
├── cmd/server/          # Application entry point
├── internal/
│   ├── allocator/       # Core allocation strategies
│   ├── memory/          # Memory block management
│   ├── pool/            # Pool and arena allocators
│   ├── simulator/       # Simulation engine
│   └── metrics/         # Statistics and metrics
├── web/
│   ├── server.go        # WebSocket server
│   └── static/          # Web UI (HTML, CSS, JS)
├── bin/                 # Compiled binaries
└── README.md
```

## Quick Start

### Prerequisites

- Go 1.21 or higher
- Modern web browser

### Installation

```bash
# Clone or navigate to the project directory
cd Memory-Allocator

# Install dependencies
go mod tidy

# Build the server
go build -o bin/memory-allocator ./cmd/server

# Run the server
./bin/memory-allocator
```

The server will start on `http://localhost:8083` by default. Override with `MEMALLOC_PORT`.

### Configuration (environment variables)

| Variable | Default | Description |
|---|---|---|
| `MEMALLOC_PORT` | `8083` | HTTP / WebSocket port |
| `MEMALLOC_STATIC_DIR` | `./web/static` | Path to frontend assets |
| `MEMALLOC_BROADCAST_BUFFER` | `256` | Server-side broadcast queue depth |
| `MEMALLOC_PING_PERIOD` | `30s` | WebSocket keep-alive interval |
| `MEMALLOC_PONG_WAIT` | `60s` | WebSocket read deadline |
| `MEMALLOC_WRITE_WAIT` | `5s` | WebSocket write deadline |
| `MEMALLOC_MAX_MESSAGE_BYTES` | `1048576` | Per-message read limit |
| `MEMALLOC_MAX_CONN_PER_IP` | `10` | Max concurrent WebSocket connections per IP |

Example:

```bash
MEMALLOC_PORT=8090 MEMALLOC_STATIC_DIR=./web/static ./bin/memory-allocator
```

### Running Tests

```bash
# Run all unit tests
go test ./...

# Run tests with coverage
go test -cover ./...

# Run benchmarks
go test -bench=. ./internal/allocator

# Run specific benchmark
go test -bench=BenchmarkAllocatorComparison -benchtime=1s ./internal/allocator
```

## Usage

### Web Interface

1. **Initialize Allocator**
   - Select allocation algorithm
   - Set memory size (1KB - 64KB)
   - Click "Initialize Allocator"

2. **Automatic Mode**
   - Click "Start Auto" to begin automatic allocation/deallocation
   - Adjust speed slider to control simulation rate
   - Use "Pause" and "Resume" for control

3. **Manual Operations**
   - Enter size and owner
   - Click "Allocate" to allocate memory
   - Click on blocks in visualization to select
   - Click "Deallocate Selected" to free memory

4. **Advanced Features**
   - Click "Coalesce" to manually merge adjacent free blocks
   - Click "Detect Leaks" to identify long-lived allocations
   - Monitor real-time metrics in the dashboard

### Programmatic Usage

```go
package main

import (
    "github.com/sanskar/memory-allocator/internal/allocator"
)

func main() {
    // Create a First-Fit allocator with 64KB
    alloc := allocator.NewFirstFitAllocator(65536)

    // Allocate memory
    block, err := alloc.Allocate(1024, "mydata")
    if err != nil {
        panic(err)
    }

    // Use the block
    println("Allocated at:", block.Address)

    // Deallocate when done
    err = alloc.Deallocate(block.Address)
    if err != nil {
        panic(err)
    }

    // Get metrics
    metrics := alloc.GetMetrics()
    println("Total allocations:", metrics.TotalAllocations)
    println("Fragmentation:", metrics.Fragmentation, "%")
}
```

## Algorithm Details

### First Fit

- **Strategy**: Allocates first block large enough
- **Time Complexity**: O(n) for allocation
- **Advantages**: Fast, simple
- **Disadvantages**: Can cause fragmentation at beginning of memory

### Best Fit

- **Strategy**: Allocates smallest sufficient block
- **Time Complexity**: O(n) for allocation
- **Advantages**: Minimizes wasted space
- **Disadvantages**: Slower, creates small unusable fragments

### Worst Fit

- **Strategy**: Allocates largest available block
- **Time Complexity**: O(n) for allocation
- **Advantages**: Leaves large free blocks
- **Disadvantages**: Poor memory utilization

### Buddy System

- **Strategy**: Power-of-2 allocation with buddy pairing
- **Time Complexity**: O(log n) for both allocation and deallocation
- **Advantages**: Fast coalescing, predictable
- **Disadvantages**: Internal fragmentation (up to 50%)

### Pool Allocator

- **Strategy**: Pre-allocated fixed-size blocks
- **Time Complexity**: O(1) for both operations
- **Advantages**: Extremely fast, no fragmentation
- **Disadvantages**: Fixed size, memory may be wasted
- **Use Case**: Object pools, network buffers

### Slab Allocator

- **Strategy**: Per-size-class object caches (16, 32, 64, ..., 2048 bytes); allocate picks the smallest class that fits
- **Time Complexity**: O(1) for both operations
- **Advantages**: 5-10× faster than fit-family (no list walk); great for mixed-size workloads; bounded internal fragmentation (~50% worst case per class)
- **Disadvantages**: Wastes space on odd-sized requests; classes are fixed at construction
- **Use Case**: Kernel object caches, runtime allocators with hot allocation paths

### Segregated-Fit Allocator

- **Strategy**: One free list per size class; on demand, larger classes are split into smaller; on free, adjacent free blocks at the same class are merged and promoted to the next class
- **Time Complexity**: O(1) average for both operations (with cache-friendly working set)
- **Advantages**: Faster than fit-family for varied-size workloads; coalescing reduces long-term fragmentation
- **Disadvantages**: Power-of-two granularity (configurable); splits/merges are O(log classes) per op
- **Use Case**: General-purpose malloc replacement; jemalloc / tcmalloc style

### Arena Allocator

- **Strategy**: Bump-pointer allocation
- **Time Complexity**: O(1) for allocation
- **Advantages**: Fastest allocation, no fragmentation
- **Disadvantages**: No individual deallocation
- **Use Case**: Request-scoped allocations, parsers

## Performance

Benchmark results on Apple M1 (2020):

```
BenchmarkFirstFitAllocation-11     269404    380.3 ns/op
BenchmarkBestFitAllocation-11      245012    448.7 ns/op
BenchmarkWorstFitAllocation-11     251893    442.1 ns/op
BenchmarkBuddyAllocation-11        398721    286.4 ns/op
BenchmarkPoolAllocation-11        1450234     82.3 ns/op
BenchmarkArenaAllocation-11       3247891     36.8 ns/op
```

## API Reference

### Allocator Interface

All allocators implement the `Allocator` interface:

```go
type Allocator interface {
    Allocate(size int, owner string) (*memory.Block, error)
    Deallocate(address uintptr) error
    GetBlock(address uintptr) (*memory.Block, error)
    GetAllBlocks() []*memory.Block
    GetFreeBlocks() []*memory.Block
    GetAllocatedBlocks() []*memory.Block
    GetMetrics() metrics.MetricsSnapshot
    CalculateFragmentation() float64
    Coalesce() int
    Reset()
    Name() string
    TotalSize() int
}
```

### Memory Block

```go
type Block struct {
    ID             int
    Address        uintptr
    Size           int
    State          BlockState
    AllocatedAt    time.Time
    FreedAt        time.Time
    Owner          string
    Color          string
    AccessCount    int
}
```

### Metrics

```go
type MetricsSnapshot struct {
    TotalAllocations      int64
    TotalDeallocations    int64
    CurrentAllocations    int64
    TotalBytesAllocated   int64
    CurrentBytesUsed      int64
    PeakBytesUsed         int64
    FailedAllocations     int64
    Fragmentation         float64
    AverageAllocTime      time.Duration
    AverageFreeTime       time.Duration
    Utilization           float64
    Timestamp             time.Time
}
```

## WebSocket API

The server exposes a WebSocket endpoint at `/ws` for real-time communication.

### Message Types

```javascript
// Initialize allocator
{
    type: "init",
    allocator: "firstfit", // or "bestfit", "worstfit", "buddy", "pool", "arena"
    size: 8192,
    blockSize: 256  // Only for pool allocator
}

// Control commands
{ type: "start" }
{ type: "pause" }
{ type: "resume" }
{ type: "stop" }
{ type: "reset" }

// Manual operations
{
    type: "allocate",
    size: 512,
    owner: "User"
}

{
    type: "deallocate",
    address: 4096
}

// Utilities
{ type: "coalesce" }
{ type: "detectLeaks", threshold: 5.0 }
{ type: "speed", speed: 100 }
{ type: "getState" }
```

### State Updates

The server broadcasts state updates:

```javascript
{
    state: 1,              // 0=Idle, 1=Running, 2=Paused, 3=Complete
    allocatorType: "firstfit",
    allocatorName: "First-Fit",
    blocks: [...],         // Array of memory blocks
    metrics: {...},        // Current metrics
    events: [...],         // Recent events
    leaks: [...],          // Detected leaks
    fragmentation: 15.2,
    totalSize: 8192,
    timestamp: "2024-..."
}
```

## Development

### Project Structure

- `internal/allocator/` - Core allocation logic
- `internal/memory/` - Block management and linked lists
- `internal/pool/` - Specialized allocators (pool, arena)
- `internal/simulator/` - Simulation orchestration
- `internal/metrics/` - Statistics tracking
- `web/` - WebSocket server and static assets

### Adding a New Allocator

1. Implement the `Allocator` interface
2. Add initialization in `web/server.go`
3. Add test cases in `internal/allocator/allocator_test.go`
4. Add benchmarks if needed
5. Update UI dropdown in `web/static/index.html`

### Code Quality

- Go modules for dependency management
- Comprehensive unit tests (22 tests)
- Performance benchmarks
- Thread-safe implementations
- Proper error handling
- Production-ready logging

## Troubleshooting

### Port Already in Use

Set `MEMALLOC_PORT` to any free port:

```bash
MEMALLOC_PORT=8090 ./bin/memory-allocator
```

The server now also installs SIGINT/SIGTERM handlers and shuts down gracefully (drains WebSocket connections, closes the broadcast goroutine, exits 0).

### WebSocket Connection Failed

- Ensure the server is running
- Check firewall settings
- Try accessing via `http://localhost:8083` (or whichever `MEMALLOC_PORT` you set) directly

### Memory Exhausted Errors

- Increase memory size in initialization
- Use "Reset" to clear allocations
- Consider using arena allocator for bulk operations

## Contributing

This is a research/educational project. Contributions are welcome:

1. Fork the repository
2. Create a feature branch
3. Add tests for new features
4. Ensure all tests pass
5. Submit a pull request

## License

MIT License - See LICENSE file for details

## References

- Knuth, Donald E. "The Art of Computer Programming, Volume 1: Fundamental Algorithms"
- Wilson, P. R., et al. "Dynamic Storage Allocation: A Survey and Critical Review"
- Linux Kernel Buddy Allocator Documentation
- Boehm, Hans-J. "A Memory Allocator"

## Author

Built with production-level engineering practices, comprehensive testing, and attention to detail.

---

**Note**: This is a simulator for educational and research purposes. For production memory management, use language-native allocators optimized for your platform.
