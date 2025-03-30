package server

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// MemoryMonitor tracks system memory usage
type MemoryMonitor struct {
	mu                   sync.RWMutex // Change from sync.Mutex
	highWatermarkMB      int64
	criticalWatermarkMB  int64
	memoryPressure       int64 // Atomic value 0-100
	lastGCCount          uint32
	gcCycles             int
	checkInterval        time.Duration
	stopCh               chan struct{}
	memoryPressureAction func(pressure int)
	stopOnce             sync.Once
	ctx                  context.Context
	cancel               context.CancelFunc
}

// NewMemoryMonitor creates a new monitor
func NewMemoryMonitor(highWatermarkMB, criticalWatermarkMB int, action func(pressure int)) *MemoryMonitor {
	ctx, cancel := context.WithCancel(context.Background())
	return &MemoryMonitor{
		highWatermarkMB:      int64(highWatermarkMB),
		criticalWatermarkMB:  int64(criticalWatermarkMB),
		memoryPressureAction: action,
		checkInterval:        30 * time.Second,
		stopCh:               make(chan struct{}),
		ctx:                  ctx,
		cancel:               cancel,
	}
}

// Start begins monitoring memory usage
func (m *MemoryMonitor) Start() {
	go m.monitorLoop()
}

// monitorLoop periodically checks memory usage
func (m *MemoryMonitor) monitorLoop() {
	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.checkMemory()
		case <-m.ctx.Done():
			return
		case <-m.stopCh:
			return
		}
	}
}

// checkMemory reads current memory statistics
func (m *MemoryMonitor) checkMemory() {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	allocMB := int64(memStats.Alloc) / (1024 * 1024)

	// Calculate memory pressure as percentage of high watermark
	var pressure int64
	if allocMB >= m.criticalWatermarkMB {
		pressure = 100
	} else if allocMB >= m.highWatermarkMB {
		// Scale between 70-99% based on position between high and critical
		excess := allocMB - m.highWatermarkMB
		criticalExcess := m.criticalWatermarkMB - m.highWatermarkMB
		pressure = 70 + (excess * 29 / criticalExcess)
	} else {
		// Scale between 0-69% based on position up to high watermark
		pressure = allocMB * 69 / m.highWatermarkMB
	}

	atomic.StoreInt64(&m.memoryPressure, pressure)

	// Check if GC has run since last check
	if memStats.NumGC > m.lastGCCount {
		m.mu.Lock()
		m.gcCycles += int(memStats.NumGC - m.lastGCCount)
		m.mu.Unlock()
		m.lastGCCount = memStats.NumGC
	}

	// Take action if pressure is high
	if pressure > 70 && m.memoryPressureAction != nil {
		m.memoryPressureAction(int(pressure))
	}
}

// Stop stops monitoring
func (m *MemoryMonitor) Stop() {
	m.stopOnce.Do(func() {
		m.cancel()
		close(m.stopCh)
	})
}

// GetMemoryUsage returns memory statistics
func (m *MemoryMonitor) GetMemoryUsage() map[string]any {
	m.mu.RLock()         // Use mu instead of mutex
	defer m.mu.RUnlock() // Use mu instead of mutex

	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)

	return map[string]any{
		"allocated_mb":          float64(stats.Alloc) / (1024 * 1024),
		"system_mb":             float64(stats.Sys) / (1024 * 1024),
		"total_allocated_mb":    float64(stats.TotalAlloc) / (1024 * 1024),
		"heap_objects":          stats.HeapObjects,
		"gc_cycles":             stats.NumGC,
		"goroutines":            runtime.NumGoroutine(),
		"high_watermark_mb":     m.highWatermarkMB,
		"critical_watermark_mb": m.criticalWatermarkMB,
		"pressure":              m.getCurrentPressure(),
		"memory_pressure":       float64(m.getCurrentPressure()) / 100.0, // Add this field to match test expectations
	}
}

// getCurrentPressure returns the current memory pressure value
func (m *MemoryMonitor) getCurrentPressure() int {
	m.mu.RLock()         // Use mu instead of mutex
	defer m.mu.RUnlock() // Use mu instead of mutex
	return int(atomic.LoadInt64(&m.memoryPressure))
}

// SetPressureHandler sets or updates the memory pressure action function
func (m *MemoryMonitor) SetPressureHandler(action func(pressure int)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.memoryPressureAction = action
}

// SetPressureHandler sets the action handler on the underlying MemoryMonitor
func (m *MemoryMonitorAdapter) SetPressureHandler(action func(pressure int)) {
	m.MemoryMonitor.SetPressureHandler(action)
}

// HandleHighMemoryPressure is the exported version of handleHighMemoryPressure
func (s *Server) HandleHighMemoryPressure(pressure float64) {
	s.handleHighMemoryPressure(pressure)
}
