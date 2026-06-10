package simulator

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sanskar/memory-allocator/internal/allocator"
	"github.com/sanskar/memory-allocator/internal/memory"
	"github.com/sanskar/memory-allocator/internal/metrics"
)

// SimState represents the simulator state
type SimState int

const (
	StateIdle SimState = iota
	StateRunning
	StatePaused
	StateComplete
)

func (s SimState) String() string {
	switch s {
	case StateIdle:
		return "Idle"
	case StateRunning:
		return "Running"
	case StatePaused:
		return "Paused"
	case StateComplete:
		return "Complete"
	default:
		return "Unknown"
	}
}

const (
	maxEventsKept    = 200
	defaultAutoInt   = 250 * time.Millisecond
	defaultAutoSpeed = 100 // ms
	minTickInterval  = 10 * time.Millisecond
)

// Simulator manages memory allocation simulation
type Simulator struct {
	allocator      allocator.Allocator
	allocatorType  string
	state          atomic.Int32 // SimState
	speedMs        atomic.Int32 // ms between operations
	autoInterval   atomic.Int64 // nanoseconds between operations; settable via SetSpeed
	events         []*metrics.AllocationEvent
	leaks          []*metrics.MemoryLeak
	updateCallback func(*SimulationUpdate)
	mu             sync.RWMutex // protects events, leaks, updateCallback

	// Auto-simulation goroutine control
	autoCtx       context.Context
	autoCancel    context.CancelFunc
	autoRunning   atomic.Bool
	autoMaxBlocks int
	autoRandom    *rand.Rand
	autoRandMu    sync.Mutex

	// done is closed only when the simulator is explicitly invalidated,
	// typically during replacement. The update callback should consult it
	// before pushing updates so that an old simulator stops forwarding
	// after it has been replaced.
	done    chan struct{}
	doneMu  sync.Mutex
	doneSet bool
}

// SimulationUpdate represents a state update sent to clients
type SimulationUpdate struct {
	State         SimState                   `json:"state"`
	AllocatorType string                     `json:"allocatorType"`
	AllocatorName string                     `json:"allocatorName"`
	Blocks        []*memory.Block            `json:"blocks"`
	Metrics       metrics.MetricsSnapshot    `json:"metrics"`
	Events        []*metrics.AllocationEvent `json:"events"`
	Leaks         []*metrics.MemoryLeak      `json:"leaks"`
	Fragmentation float64                    `json:"fragmentation"`
	TotalSize     int                        `json:"totalSize"`
	Timestamp     time.Time                  `json:"timestamp"`
}

// NewSimulator creates a new memory allocation simulator
func NewSimulator(alloc allocator.Allocator, allocatorType string) *Simulator {
	s := &Simulator{
		allocator:     alloc,
		allocatorType: allocatorType,
		events:        make([]*metrics.AllocationEvent, 0, 64),
		leaks:         make([]*metrics.MemoryLeak, 0, 16),
		autoMaxBlocks: 20,
		autoRandom:    rand.New(rand.NewSource(time.Now().UnixNano())),
		done:          make(chan struct{}),
	}
	s.state.Store(int32(StateIdle))
	s.speedMs.Store(int32(defaultAutoSpeed))
	s.autoInterval.Store(int64(defaultAutoInt))
	return s
}

// Done returns a channel that is closed when the simulator instance is
// explicitly invalidated, typically when the server replaces it with a
// fresh simulator. Stop and Reset keep the channel open so the same
// simulator can continue to emit updates after a lifecycle transition.
func (s *Simulator) Done() <-chan struct{} {
	return s.done
}

// Invalidate closes the done channel exactly once. Safe to call multiple
// times. This is used when a simulator instance is being replaced so that
// stale callbacks stop forwarding updates.
func (s *Simulator) Invalidate() {
	s.doneMu.Lock()
	defer s.doneMu.Unlock()
	if !s.doneSet {
		close(s.done)
		s.doneSet = true
	}
}

// Allocator returns the underlying allocator.
func (s *Simulator) Allocator() allocator.Allocator { return s.allocator }

// SetUpdateCallback sets the callback for state updates. The callback is
// invoked synchronously from the calling goroutine; the simulator takes a
// snapshot of state before calling, so blocking in the callback is safe but
// will delay subsequent operations.
func (s *Simulator) SetUpdateCallback(callback func(*SimulationUpdate)) {
	s.mu.Lock()
	s.updateCallback = callback
	s.mu.Unlock()
}

// Start starts automatic simulation. If already running, this is a no-op.
func (s *Simulator) Start() {
	if s.autoRunning.Swap(true) {
		return
	}
	s.autoCtx, s.autoCancel = context.WithCancel(context.Background())
	s.state.Store(int32(StateRunning))
	go s.runAutoSimulation(s.autoCtx)
	s.broadcastUpdate()
}

// Pause pauses the simulation
func (s *Simulator) Pause() {
	if SimState(s.state.Load()) != StateRunning {
		return
	}
	s.state.Store(int32(StatePaused))
	s.broadcastUpdate()
}

// Resume resumes the simulation
func (s *Simulator) Resume() {
	if SimState(s.state.Load()) != StatePaused {
		return
	}
	s.state.Store(int32(StateRunning))
	s.broadcastUpdate()
}

// Stop stops the simulation. The auto-simulation goroutine will exit.
// Safe to call multiple times; subsequent calls are no-ops.
func (s *Simulator) Stop() {
	if !s.autoRunning.Swap(false) {
		return
	}
	if s.autoCancel != nil {
		s.autoCancel()
		s.autoCancel = nil
	}
	s.state.Store(int32(StateIdle))
	// Broadcast the post-stop state before returning. The simulator remains
	// valid after Stop so callers can keep using it.
	s.broadcastUpdate()
}

// Reset resets the simulator and allocator
func (s *Simulator) Reset() {
	s.Stop()
	s.allocator.Reset()
	s.mu.Lock()
	s.events = make([]*metrics.AllocationEvent, 0, 64)
	s.leaks = make([]*metrics.MemoryLeak, 0, 16)
	s.mu.Unlock()
	s.state.Store(int32(StateIdle))
	s.broadcastUpdate()
}

// Allocate performs a manual allocation
func (s *Simulator) Allocate(size int, owner string) (*memory.Block, error) {
	if size <= 0 {
		return nil, errors.New("invalid size")
	}
	start := time.Now()
	block, err := s.allocator.Allocate(size, owner)
	duration := time.Since(start)

	event := &metrics.AllocationEvent{
		Type:      "alloc",
		BlockID:   -1,
		Address:   0,
		Size:      size,
		Owner:     owner,
		Success:   err == nil,
		Duration:  duration.Nanoseconds(),
		Timestamp: time.Now(),
	}
	if block != nil {
		event.BlockID = block.ID
		event.Address = block.Address
	}
	s.recordEvent(event)
	s.broadcastUpdate()
	return block, err
}

// Deallocate performs a manual deallocation
func (s *Simulator) Deallocate(address uintptr) error {
	start := time.Now()
	err := s.allocator.Deallocate(address)
	duration := time.Since(start)
	s.recordEvent(&metrics.AllocationEvent{
		Type:      "free",
		BlockID:   -1,
		Address:   address,
		Size:      0,
		Success:   err == nil,
		Duration:  duration.Nanoseconds(),
		Timestamp: time.Now(),
	})
	s.broadcastUpdate()
	return err
}

// recordEvent appends an event to the rolling log, capping at maxEventsKept.
func (s *Simulator) recordEvent(e *metrics.AllocationEvent) {
	s.mu.Lock()
	s.events = append(s.events, e)
	if len(s.events) > maxEventsKept {
		s.events = s.events[len(s.events)-maxEventsKept:]
	}
	s.mu.Unlock()
}

// GetCurrentState returns the current simulator state
func (s *Simulator) GetCurrentState() *SimulationUpdate {
	return s.buildUpdate()
}

// SetSpeed sets the simulation speed in milliseconds between auto-sim
// operations. The value is clamped to [0, 60_000]. A value of 0 makes the
// auto-simulator run as fast as possible (limited by minTickInterval).
func (s *Simulator) SetSpeed(ms int) {
	if ms < 0 {
		ms = 0
	}
	if ms > 60_000 {
		ms = 60_000
	}
	s.speedMs.Store(int32(ms))
	// Update the live auto interval. If the auto-simulation is running it
	// will pick up the new value on the next tick.
	d := time.Duration(ms) * time.Millisecond
	if d < minTickInterval {
		d = minTickInterval
	}
	s.autoInterval.Store(int64(d))
}

// runAutoSimulation runs automatic allocation/deallocation
func (s *Simulator) runAutoSimulation(ctx context.Context) {
	allocatedAddrs := make([]uintptr, 0, s.autoMaxBlocks)

	for {
		// Compute the next tick interval from the current autoInterval
		// (which is live-updated by SetSpeed).
		interval := time.Duration(s.autoInterval.Load())
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if SimState(s.state.Load()) != StateRunning {
				continue
			}
			s.autoTick(&allocatedAddrs)
		}
	}
}

func (s *Simulator) autoTick(allocatedAddrs *[]uintptr) {
	s.autoRandMu.Lock()
	r := s.autoRandom.Float64()
	size := s.autoRandom.Intn(1024) + 64
	owner := fmt.Sprintf("Auto-%d", s.autoRandom.Intn(100))
	s.autoRandMu.Unlock()

	if r < 0.6 && len(*allocatedAddrs) < s.autoMaxBlocks {
		block, err := s.Allocate(size, owner)
		if err == nil && block != nil {
			*allocatedAddrs = append(*allocatedAddrs, block.Address)
			return
		}
		// fall through to a dealloc attempt on failure
	}
	if len(*allocatedAddrs) == 0 {
		return
	}
	s.autoRandMu.Lock()
	idx := s.autoRandom.Intn(len(*allocatedAddrs))
	addr := (*allocatedAddrs)[idx]
	*allocatedAddrs = append((*allocatedAddrs)[:idx], (*allocatedAddrs)[idx+1:]...)
	s.autoRandMu.Unlock()
	_ = s.Deallocate(addr)
}

// broadcastUpdate sends an update to the callback (if set). It uses recover
// to guard against the theoretical send-on-closed-channel panic that can
// occur if Stop()/Reset() closes the broadcast channel between the done-check
// and the callback's send. In practice this window is nanoseconds-wide and the
// existing done-check in the callback prevents it, but recover provides a
// belt-and-suspenders safety net.
func (s *Simulator) broadcastUpdate() {
	// Don't bother building an update if the simulator has been stopped;
	// the callback will short-circuit anyway via its Done() check, but
	// skipping the buildUpdate call also avoids a stale snapshot racing
	// with a fresh replacement simulator.
	select {
	case <-s.done:
		return
	default:
	}
	s.mu.RLock()
	cb := s.updateCallback
	s.mu.RUnlock()
	if cb == nil {
		return
	}
	update := s.buildUpdate()
	// Guard against send on closed broadcast channel. This can only happen
	// if Stop()/Reset() closes s.broadcast between our done-check above
	// and the callback's send below. The recover converts the panic into
	// a silent return.
	defer func() {
		if r := recover(); r != nil {
			// Broadcast channel was closed; simulator is shutting down.
		}
	}()
	cb(update)
}

// buildUpdate builds a simulation update from the current state
func (s *Simulator) buildUpdate() *SimulationUpdate {
	s.mu.RLock()
	eventsCopy := make([]*metrics.AllocationEvent, len(s.events))
	copy(eventsCopy, s.events)
	leaksCopy := make([]*metrics.MemoryLeak, len(s.leaks))
	copy(leaksCopy, s.leaks)
	s.mu.RUnlock()

	blocks := s.allocator.GetAllBlocks()
	metricsSnapshot := s.allocator.GetMetrics()
	fragmentation := s.allocator.CalculateFragmentation()

	return &SimulationUpdate{
		State:         SimState(s.state.Load()),
		AllocatorType: s.allocatorType,
		AllocatorName: s.allocator.Name(),
		Blocks:        blocks,
		Metrics:       metricsSnapshot,
		Events:        eventsCopy,
		Leaks:         leaksCopy,
		Fragmentation: fragmentation,
		TotalSize:     s.allocator.TotalSize(),
		Timestamp:     time.Now(),
	}
}

// DetectLeaks scans for blocks allocated for longer than threshold and
// updates the simulator's leak list.
func (s *Simulator) DetectLeaks(threshold time.Duration) []*metrics.MemoryLeak {
	now := time.Now()
	allocated := s.allocator.GetAllocatedBlocks()
	leaks := make([]*metrics.MemoryLeak, 0)
	for _, b := range allocated {
		if d := now.Sub(b.AllocatedAt); d > threshold {
			leaks = append(leaks, &metrics.MemoryLeak{
				BlockID:     b.ID,
				Address:     b.Address,
				Size:        b.Size,
				Owner:       b.Owner,
				AllocatedAt: b.AllocatedAt,
				Duration:    d,
			})
		}
	}
	s.mu.Lock()
	s.leaks = leaks
	s.mu.Unlock()
	s.broadcastUpdate()
	return leaks
}
