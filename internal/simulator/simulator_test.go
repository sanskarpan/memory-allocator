package simulator

import (
	"sync"
	"testing"
	"time"

	"github.com/sanskar/memory-allocator/internal/allocator"
	"github.com/sanskar/memory-allocator/internal/pool"
)

func TestSimulator_FirstFitAllocateDeallocate(t *testing.T) {
	sim := NewSimulator(allocator.NewFirstFitAllocator(1024), "firstfit")
	defer sim.Reset()

	b, err := sim.Allocate(256, "owner1")
	if err != nil {
		t.Fatalf("Allocate failed: %v", err)
	}
	if b == nil {
		t.Fatal("Expected non-nil block")
	}
	if b.Size != 256 {
		t.Errorf("Expected size 256, got %d", b.Size)
	}

	if err := sim.Deallocate(b.Address); err != nil {
		t.Fatalf("Deallocate failed: %v", err)
	}
}

func TestSimulator_BuddyAllocate(t *testing.T) {
	sim := NewSimulator(allocator.NewBuddyAllocator(1024), "buddy")
	defer sim.Reset()

	for i := 0; i < 4; i++ {
		b, err := sim.Allocate(64, "owner")
		if err != nil {
			t.Fatalf("Allocate %d failed: %v", i, err)
		}
		if b == nil {
			t.Fatalf("Allocate %d returned nil", i)
		}
	}
	// 5th 64-byte allocation should succeed (1024 / 64 = 16 max, we did 4)
	b, err := sim.Allocate(64, "owner")
	if err != nil {
		t.Errorf("5th allocation should have succeeded: %v", err)
	}
	if b != nil {
		_ = sim.Deallocate(b.Address)
	}
}

func TestSimulator_PoolAllocator(t *testing.T) {
	poolAlloc := pool.NewPoolAllocator(128, 4)
	sim := NewSimulator(poolAlloc, "pool")
	defer sim.Reset()

	// Allocating more than 4 blocks should fail
	for i := 0; i < 4; i++ {
		b, err := sim.Allocate(64, "owner")
		if err != nil {
			t.Fatalf("Pool alloc %d failed: %v", i, err)
		}
		if b == nil {
			t.Fatalf("Pool alloc %d returned nil", i)
		}
	}
	if _, err := sim.Allocate(64, "excess"); err == nil {
		t.Error("Expected pool exhausted error")
	}
}

func TestSimulator_ArenaAllocator(t *testing.T) {
	arena := pool.NewArenaAllocator(512)
	sim := NewSimulator(arena, "arena")
	defer sim.Reset()

	b, err := sim.Allocate(128, "owner")
	if err != nil {
		t.Fatalf("Arena alloc failed: %v", err)
	}
	// Individual dealloc should error
	if err := sim.Deallocate(b.Address); err == nil {
		t.Error("Expected error deallocating from arena")
	}
	// Reset should free everything
	sim.Reset()
	b2, err := sim.Allocate(512, "owner")
	if err != nil {
		t.Errorf("Should be able to allocate full size after reset: %v", err)
	}
	if b2 == nil || b2.Size != 512 {
		t.Errorf("Expected 512-byte block, got %v", b2)
	}
}

func TestSimulator_StateUpdateCallback(t *testing.T) {
	sim := NewSimulator(allocator.NewFirstFitAllocator(1024), "firstfit")
	defer sim.Reset()

	var (
		mu    sync.Mutex
		calls int
	)
	sim.SetUpdateCallback(func(u *SimulationUpdate) {
		mu.Lock()
		calls++
		mu.Unlock()
	})

	if _, err := sim.Allocate(128, "x"); err != nil {
		t.Fatal(err)
	}
	// Give the callback a moment (it's synchronous so this should already be done)
	if c := readCount(&mu, &calls); c == 0 {
		t.Error("Expected update callback to fire on allocate")
	}

	mu.Lock()
	calls = 0
	mu.Unlock()
	sim.GetCurrentState() // should not trigger callback
	mu.Lock()
	if calls != 0 {
		t.Errorf("GetCurrentState should not trigger callback, got %d", calls)
	}
	mu.Unlock()
}

func readCount(mu *sync.Mutex, c *int) int {
	mu.Lock()
	defer mu.Unlock()
	return *c
}

func TestSimulator_StartStopLifecycle(t *testing.T) {
	sim := NewSimulator(allocator.NewFirstFitAllocator(8192), "firstfit")
	defer sim.Reset()

	sim.Start()
	if !sim.autoRunning.Load() {
		t.Error("Expected autoRunning=true after Start")
	}
	// Calling Start again should be a no-op
	sim.Start()
	if !sim.autoRunning.Load() {
		t.Error("Second Start should keep autoRunning=true")
	}
	sim.Stop()
	if sim.autoRunning.Load() {
		t.Error("Expected autoRunning=false after Stop")
	}
}

func TestSimulator_DetectLeaks(t *testing.T) {
	sim := NewSimulator(allocator.NewFirstFitAllocator(1024), "firstfit")
	defer sim.Reset()

	b, err := sim.Allocate(256, "owner")
	if err != nil {
		t.Fatal(err)
	}
	// Wait so that the block's allocation age exceeds the threshold
	time.Sleep(150 * time.Millisecond)
	leaks := sim.DetectLeaks(100 * time.Millisecond)
	if len(leaks) == 0 {
		t.Error("Expected to detect a leak")
	}
	_ = sim.Deallocate(b.Address)
}

func TestSimulator_ResetClearsState(t *testing.T) {
	sim := NewSimulator(allocator.NewFirstFitAllocator(1024), "firstfit")

	for i := 0; i < 3; i++ {
		if _, err := sim.Allocate(100, "x"); err != nil {
			t.Fatal(err)
		}
	}
	sim.Reset()
	state := sim.GetCurrentState()
	if state.Metrics.TotalAllocations != 0 {
		t.Errorf("After reset, expected 0 allocations, got %d", state.Metrics.TotalAllocations)
	}
	if len(state.Blocks) != 1 {
		// After reset there is exactly one free block of total size
		t.Errorf("Expected exactly 1 free block after reset, got %d", len(state.Blocks))
	}
}

func TestSimulator_SetSpeedStores(t *testing.T) {
	sim := NewSimulator(allocator.NewFirstFitAllocator(1024), "firstfit")
	defer sim.Reset()
	sim.SetSpeed(250)
	if got := sim.speedMs.Load(); got != 250 {
		t.Errorf("Expected speedMs=250, got %d", got)
	}
	// Negative speed should clamp to 0
	sim.SetSpeed(-10)
	if got := sim.speedMs.Load(); got != 0 {
		t.Errorf("Expected speedMs=0 (clamped), got %d", got)
	}
}
