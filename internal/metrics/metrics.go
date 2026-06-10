package metrics

import (
	"sync"
	"time"
)

// AllocationMetrics tracks memory allocation statistics
type AllocationMetrics struct {
	TotalAllocations    int64         // Total number of allocations
	TotalDeallocations  int64         // Total number of deallocations
	CurrentAllocations  int64         // Current number of active allocations
	TotalBytesAllocated int64         // Total bytes allocated
	TotalBytesFreed     int64         // Total bytes freed
	CurrentBytesUsed    int64         // Current bytes in use
	PeakBytesUsed       int64         // Peak memory usage
	FailedAllocations   int64         // Number of failed allocations
	Fragmentation       float64       // Fragmentation percentage
	AverageAllocTime    time.Duration // Average allocation time
	AverageFreeTime     time.Duration // Average deallocation time
	mu                  sync.RWMutex
}

// NewAllocationMetrics creates a new metrics tracker
func NewAllocationMetrics() *AllocationMetrics {
	return &AllocationMetrics{}
}

// RecordAllocation records a successful allocation
func (m *AllocationMetrics) RecordAllocation(size int, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.TotalAllocations++
	m.CurrentAllocations++
	m.TotalBytesAllocated += int64(size)
	m.CurrentBytesUsed += int64(size)

	if m.CurrentBytesUsed > m.PeakBytesUsed {
		m.PeakBytesUsed = m.CurrentBytesUsed
	}

	// Update average allocation time
	if m.TotalAllocations == 1 {
		m.AverageAllocTime = duration
	} else {
		total := m.AverageAllocTime * time.Duration(m.TotalAllocations-1)
		m.AverageAllocTime = (total + duration) / time.Duration(m.TotalAllocations)
	}
}

// RecordDeallocation records a successful deallocation
func (m *AllocationMetrics) RecordDeallocation(size int, duration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.TotalDeallocations++
	m.CurrentAllocations--
	m.TotalBytesFreed += int64(size)
	m.CurrentBytesUsed -= int64(size)

	// Update average free time
	if m.TotalDeallocations == 1 {
		m.AverageFreeTime = duration
	} else {
		total := m.AverageFreeTime * time.Duration(m.TotalDeallocations-1)
		m.AverageFreeTime = (total + duration) / time.Duration(m.TotalDeallocations)
	}
}

// RecordFailedAllocation records a failed allocation attempt
func (m *AllocationMetrics) RecordFailedAllocation() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.FailedAllocations++
}

// UpdateFragmentation updates the fragmentation percentage
func (m *AllocationMetrics) UpdateFragmentation(frag float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Fragmentation = frag
}

// GetSnapshot returns a snapshot of current metrics
func (m *AllocationMetrics) GetSnapshot() MetricsSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return MetricsSnapshot{
		TotalAllocations:    m.TotalAllocations,
		TotalDeallocations:  m.TotalDeallocations,
		CurrentAllocations:  m.CurrentAllocations,
		TotalBytesAllocated: m.TotalBytesAllocated,
		TotalBytesFreed:     m.TotalBytesFreed,
		CurrentBytesUsed:    m.CurrentBytesUsed,
		PeakBytesUsed:       m.PeakBytesUsed,
		FailedAllocations:   m.FailedAllocations,
		Fragmentation:       m.Fragmentation,
		AverageAllocTime:    m.AverageAllocTime,
		AverageFreeTime:     m.AverageFreeTime,
		Utilization:         m.calculateUtilization(),
		Timestamp:           time.Now(),
	}
}

// calculateUtilization calculates memory utilization percentage
func (m *AllocationMetrics) calculateUtilization() float64 {
	if m.PeakBytesUsed == 0 {
		return 0
	}
	return float64(m.CurrentBytesUsed) / float64(m.PeakBytesUsed) * 100
}

// Reset resets all metrics
func (m *AllocationMetrics) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.TotalAllocations = 0
	m.TotalDeallocations = 0
	m.CurrentAllocations = 0
	m.TotalBytesAllocated = 0
	m.TotalBytesFreed = 0
	m.CurrentBytesUsed = 0
	m.PeakBytesUsed = 0
	m.FailedAllocations = 0
	m.Fragmentation = 0
	m.AverageAllocTime = 0
	m.AverageFreeTime = 0
}

// MetricsSnapshot represents a point-in-time snapshot of metrics
type MetricsSnapshot struct {
	TotalAllocations    int64         `json:"totalAllocations"`
	TotalDeallocations  int64         `json:"totalDeallocations"`
	CurrentAllocations  int64         `json:"currentAllocations"`
	TotalBytesAllocated int64         `json:"totalBytesAllocated"`
	TotalBytesFreed     int64         `json:"totalBytesFreed"`
	CurrentBytesUsed    int64         `json:"currentBytesUsed"`
	PeakBytesUsed       int64         `json:"peakBytesUsed"`
	FailedAllocations   int64         `json:"failedAllocations"`
	Fragmentation       float64       `json:"fragmentation"`
	AverageAllocTime    time.Duration `json:"averageAllocTime"`
	AverageFreeTime     time.Duration `json:"averageFreeTime"`
	Utilization         float64       `json:"utilization"`
	Timestamp           time.Time     `json:"timestamp"`
}

// AllocationEvent represents a memory allocation/deallocation event
type AllocationEvent struct {
	Type      string    `json:"type"` // "alloc" or "free"
	BlockID   int       `json:"blockId"`
	Address   uintptr   `json:"address"`
	Size      int       `json:"size"`
	Owner     string    `json:"owner"`
	Success   bool      `json:"success"`
	Duration  int64     `json:"duration"` // in nanoseconds
	Timestamp time.Time `json:"timestamp"`
}

// MemoryLeak represents a detected memory leak
type MemoryLeak struct {
	BlockID     int           `json:"blockId"`
	Address     uintptr       `json:"address"`
	Size        int           `json:"size"`
	Owner       string        `json:"owner"`
	AllocatedAt time.Time     `json:"allocatedAt"`
	Duration    time.Duration `json:"duration"`
}
