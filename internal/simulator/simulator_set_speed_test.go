package simulator

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/sanskar/memory-allocator/internal/allocator"
)

// TestSimulator_SetSpeedActuallyChangesTickInterval verifies the bug where
// SetSpeed only updated the legacy speedMs atomic, leaving the
// auto-simulation tick rate driven by the immutable autoInterval.
func TestSimulator_SetSpeedActuallyChangesTickInterval(t *testing.T) {
	sim := NewSimulator(allocator.NewFirstFitAllocator(1<<20), "firstfit")
	defer sim.Reset()

	// Default interval is 250ms.
	if got := time.Duration(sim.autoInterval.Load()); got != defaultAutoInt {
		t.Fatalf("default autoInterval: got %v, want %v", got, defaultAutoInt)
	}

	sim.SetSpeed(50) // 50ms
	if got := time.Duration(sim.autoInterval.Load()); got != 50*time.Millisecond {
		t.Errorf("after SetSpeed(50), autoInterval = %v, want 50ms", got)
	}

	sim.SetSpeed(500)
	if got := time.Duration(sim.autoInterval.Load()); got != 500*time.Millisecond {
		t.Errorf("after SetSpeed(500), autoInterval = %v, want 500ms", got)
	}

	// Negative should clamp to minTickInterval
	sim.SetSpeed(-100)
	if got := time.Duration(sim.autoInterval.Load()); got < minTickInterval {
		t.Errorf("after SetSpeed(-100), autoInterval = %v, want >= %v", got, minTickInterval)
	}
}

// TestSimulator_DoneChannel verifies that Stop and Reset keep the Done
// channel open, while Invalidate closes it for simulator replacement.
func TestSimulator_DoneChannel(t *testing.T) {
	sim := NewSimulator(allocator.NewFirstFitAllocator(1024), "firstfit")

	select {
	case <-sim.Done():
		t.Fatal("Done channel should not be closed yet")
	default:
	}
	sim.Reset()
	select {
	case <-sim.Done():
		t.Fatal("Done channel should stay open after Reset")
	default:
	}

	sim.Stop()
	select {
	case <-sim.Done():
		t.Fatal("Done channel should stay open after Stop")
	default:
	}

	sim.Invalidate()
	select {
	case <-sim.Done():
		// ok
	default:
		t.Fatal("Done channel should be closed after Invalidate")
	}

	// A second invalidate should be safe (idempotent).
	sim.Invalidate()
}

// TestSimulator_BroadcastStillWorksAfterStopAndReset verifies that the same
// simulator instance continues to emit updates after lifecycle transitions.
func TestSimulator_BroadcastStillWorksAfterStopAndReset(t *testing.T) {
	sim := NewSimulator(allocator.NewFirstFitAllocator(1024), "firstfit")

	var calls atomic.Int32
	sim.SetUpdateCallback(func(u *SimulationUpdate) {
		calls.Add(1)
	})

	sim.Reset()
	if got := calls.Load(); got == 0 {
		t.Error("expected at least one callback invocation from Reset")
	}
	calls.Store(0)

	if _, err := sim.Allocate(64, "after-reset"); err != nil {
		t.Fatalf("Allocate after Reset failed: %v", err)
	}
	if got := calls.Load(); got == 0 {
		t.Error("expected callback invocation after Reset")
	}

	sim.Start()
	sim.Stop()
	calls.Store(0)
	if _, err := sim.Allocate(64, "after-stop"); err != nil {
		t.Fatalf("Allocate after Stop failed: %v", err)
	}
	if got := calls.Load(); got == 0 {
		t.Error("expected callback invocation after Stop")
	}
}
