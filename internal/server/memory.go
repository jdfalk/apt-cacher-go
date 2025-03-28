package server

import (
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// MemoryMonitor tracks and manages memory usage
type MemoryMonitor struct {
	highWatermarkMB      int64
	criticalWatermarkMB  int64
	memoryPressure       int64 // Atomic value 0-100
	lastGCCount          uint32
	gcCycles             int
	checkInterval        time.Duration
	stopCh               chan struct{}
	memoryPressureAction func(pressure int)
	stopOnce             sync.Once
}

// NewMemoryMonitor creates a new monitor
func NewMemoryMonitor(highWatermarkMB, criticalWatermarkMB int, action func(pressure int)) *MemoryMonitor {
	return &MemoryMonitor{
		highWatermarkMB:      int64(highWatermarkMB),
		criticalWatermarkMB:  int64(criticalWatermarkMB),
		checkInterval:        30 * time.Second,
		stopCh:               make(chan struct{}),
		memoryPressureAction: action,
	}
}

// Start begins monitoring memory usage
func (m *MemoryMonitor) Start() {
	m.stopOnce.Do(func() {
		m.stopCh = make(chan struct{})
		go func() {
			ticker := time.NewTicker(m.checkInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					m.checkMemoryUsage()
				case <-m.stopCh:
					return
				}
			}
		}()
	})
}

// Stop stops monitoring
func (m *MemoryMonitor) Stop() {
	close(m.stopCh)
}

// GetMemoryUsage returns current memory statistics
func (m *MemoryMonitor) GetMemoryUsage() map[string]interface{} {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// Calculate memory pressure
	pressure := atomic.LoadInt64(&m.memoryPressure)

	return map[string]interface{}{
		"allocated_mb":    float64(memStats.Alloc) / (1024 * 1024),
		"system_mb":       float64(memStats.Sys) / (1024 * 1024),
		"memory_pressure": float64(pressure) / 100.0,
		"gc_cycles":       m.gcCycles,
		"heap_objects":    memStats.HeapObjects,
	}
}

// checkMemoryUsage checks memory usage and takes action if needed
func (m *MemoryMonitor) checkMemoryUsage() {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	allocatedMB := int64(memStats.Alloc) / (1024 * 1024)

	// Calculate memory pressure as percentage of high watermark
	var pressure int64
	if allocatedMB >= m.highWatermarkMB {
		pressure = 100
	} else {
		pressure = (allocatedMB * 100) / m.highWatermarkMB
	}

	// Update the atomic pressure value
	atomic.StoreInt64(&m.memoryPressure, pressure)

	// Track GC cycles
	if memStats.NumGC > m.lastGCCount {
		m.gcCycles += int(memStats.NumGC - m.lastGCCount)
		m.lastGCCount = memStats.NumGC
	}

	// Take action based on memory pressure
	if pressure > 90 {
		log.Printf("Critical memory pressure: %d%% (%dMB used)", pressure, allocatedMB)

		// Force garbage collection
		runtime.GC()

		// If we're above critical watermark, take drastic action
		if allocatedMB > m.criticalWatermarkMB {
			log.Printf("Memory usage critical: %dMB used, forcing cleanup", allocatedMB)
			if m.memoryPressureAction != nil {
				m.memoryPressureAction(int(pressure))
			}
		}
	} else if pressure > 75 {
		log.Printf("High memory pressure: %d%% (%dMB used)", pressure, allocatedMB)
		// Suggest garbage collection
		runtime.GC()
	}
}
