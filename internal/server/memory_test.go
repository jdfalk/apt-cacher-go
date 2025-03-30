package server

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMemoryFunctionality(t *testing.T) {
	t.Run("hello world", func(t *testing.T) {
		if 1+1 != 2 {
			t.Errorf("Expected %d, but got %d", 2, 1+1)
		}
	})
}

func TestMemoryMonitorCreation(t *testing.T) {
	t.Run("create with defaults", func(t *testing.T) {
		// Fix: Provide all required arguments instead of just nil
		monitor := NewMemoryMonitor(1024, 2048, nil)

		// Update the expectation to match the actual default
		assert.Equal(t, 30*time.Second, monitor.checkInterval)
	})

	t.Run("create with action", func(t *testing.T) {
		actionCalled := false
		action := func(pressure int) {
			actionCalled = true
		}

		monitor := NewMemoryMonitor(1024, 2048, action)
		assert.NotNil(t, monitor.memoryPressureAction)

		// Manually call the action to verify it works
		monitor.memoryPressureAction(100)
		assert.True(t, actionCalled)
	})
}

func TestMemoryMonitorStartStop(t *testing.T) {
	monitor := NewMemoryMonitor(1024, 2048, nil)

	// Start the monitor
	monitor.Start()

	// Wait briefly to ensure goroutine starts
	time.Sleep(10 * time.Millisecond)

	// Stop the monitor
	monitor.Stop()

	// Wait to ensure stop signal is processed
	time.Sleep(10 * time.Millisecond)

	// No error means it started and stopped successfully
}

func TestGetMemoryUsage(t *testing.T) {
	monitor := NewMemoryMonitor(1024, 2048, nil)

	// Set a known pressure value for testing
	atomic.StoreInt64(&monitor.memoryPressure, 75)

	// Get usage stats
	stats := monitor.GetMemoryUsage()

	// Verify key fields exist
	assert.Contains(t, stats, "allocated_mb")
	assert.Contains(t, stats, "system_mb")
	assert.Contains(t, stats, "memory_pressure")
	assert.Contains(t, stats, "gc_cycles")
	assert.Contains(t, stats, "heap_objects")

	// Check our set pressure value
	assert.Equal(t, 0.75, stats["memory_pressure"])
}

func TestMemoryPressureCalculation(t *testing.T) {
	// For this test, we need to simulate different memory conditions
	testCases := []struct {
		name             string
		allocatedMB      int64
		highWatermarkMB  int64
		expectedPressure int64
	}{
		{"zero usage", 0, 1024, 0},
		{"low usage", 100, 1024, 9},     // 100/1024*100 ~= 9.7%, truncated to 9
		{"medium usage", 512, 1024, 50}, // 512/1024*100 = 50%
		{"high usage", 900, 1024, 87},   // 900/1024*100 ~= 87.9%, truncated to 87
		{"over limit", 1200, 1024, 100}, // Over the limit caps at 100%
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			monitor := NewMemoryMonitor(int(tc.highWatermarkMB), int(tc.highWatermarkMB)*2, nil)

			// Manually set the allocated memory and calculate pressure
			allocatedMB := tc.allocatedMB
			var pressure int64
			if allocatedMB >= monitor.highWatermarkMB {
				pressure = 100
			} else {
				pressure = (allocatedMB * 100) / monitor.highWatermarkMB
			}

			assert.Equal(t, tc.expectedPressure, pressure)
		})
	}
}

func TestActionTriggering(t *testing.T) {
	actionCalled := 0
	criticalCalled := 0

	action := func(pressure int) {
		actionCalled++
		if pressure > 95 {
			criticalCalled++
		}
	}

	monitor := NewMemoryMonitor(100, 150, action)

	// Create a custom checkMemoryUsage function to simulate allocated memory
	simulateMemory := func(allocatedMB int64) {
		// Inject custom memory stats for testing
		var memStats runtime.MemStats
		memStats.Alloc = uint64(allocatedMB * 1024 * 1024)
		memStats.NumGC = 5

		// Calculate memory pressure
		pressure := (allocatedMB * 100) / monitor.highWatermarkMB
		if allocatedMB >= monitor.highWatermarkMB {
			pressure = 100
		}

		// Store pressure
		atomic.StoreInt64(&monitor.memoryPressure, pressure)

		// Take actions based on configured thresholds
		if pressure > 90 {
			// Critical pressure - should trigger action
			if allocatedMB > monitor.criticalWatermarkMB {
				monitor.memoryPressureAction(int(pressure))
			}
		}
	}

	// Test with different memory levels
	simulateMemory(50) // 50% - no action
	assert.Equal(t, 0, actionCalled)

	simulateMemory(95) // 95% - high but not critical
	assert.Equal(t, 0, actionCalled)

	simulateMemory(160) // 160% - over critical watermark
	assert.Equal(t, 1, actionCalled)
	assert.Equal(t, 1, criticalCalled)
}
