package server

import (
	"log"
	"runtime"
	"time"
)

// MemoryMonitor tracks and manages memory usage
type MemoryMonitor struct {
	highWatermark float64 // Threshold as percentage of system memory
	checkInterval time.Duration
	stopCh        chan struct{}
	callbacks     []func(float64)
}

// NewMemoryMonitor creates a memory monitor
func NewMemoryMonitor(highWatermark float64, interval time.Duration) *MemoryMonitor {
	return &MemoryMonitor{
		highWatermark: highWatermark,
		checkInterval: interval,
		stopCh:        make(chan struct{}),
		callbacks:     make([]func(float64), 0),
	}
}

// RegisterCallback adds a function to call when memory pressure is high
func (m *MemoryMonitor) RegisterCallback(cb func(float64)) {
	m.callbacks = append(m.callbacks, cb)
}

// Start begins monitoring memory
func (m *MemoryMonitor) Start() {
	go m.monitorLoop()
}

// Stop stops the monitor
func (m *MemoryMonitor) Stop() {
	close(m.stopCh)
}

// monitorLoop periodically checks memory usage
func (m *MemoryMonitor) monitorLoop() {
	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.checkMemory()
		}
	}
}

// checkMemory checks current memory usage and triggers callbacks if needed
func (m *MemoryMonitor) checkMemory() {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// Calculate memory pressure (0.0-1.0)
	memoryPressure := float64(memStats.Alloc) / float64(memStats.Sys)

	// Log memory usage periodically
	log.Printf("Memory usage: %.2f%% (%.2f MB / %.2f MB)",
		memoryPressure*100,
		float64(memStats.Alloc)/(1024*1024),
		float64(memStats.Sys)/(1024*1024))

	// If we're above high watermark, trigger callbacks
	if memoryPressure > m.highWatermark {
		log.Printf("HIGH MEMORY PRESSURE: %.2f%%", memoryPressure*100)
		for _, cb := range m.callbacks {
			cb(memoryPressure)
		}
	}
}

// GetMemoryUsage returns the current memory usage stats
func (m *MemoryMonitor) GetMemoryUsage() map[string]any {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	return map[string]any{
		"allocated_mb":       float64(memStats.Alloc) / (1024 * 1024),
		"total_allocated_mb": float64(memStats.TotalAlloc) / (1024 * 1024),
		"system_mb":          float64(memStats.Sys) / (1024 * 1024),
		"gc_cycles":          memStats.NumGC,
		"memory_pressure":    float64(memStats.Alloc) / float64(memStats.Sys),
	}
}
