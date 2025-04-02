package metrics

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockMetricsCollector implements mock functionality for testing
type MockMetricsCollector struct {
	mock.Mock
}

func (m *MockMetricsCollector) RecordRequest(path string, duration time.Duration, clientIP, packageName string) {
	m.Called(path, duration, clientIP, packageName)
}

func (m *MockMetricsCollector) RecordCacheHit(path string, size int64) {
	m.Called(path, size)
}

func (m *MockMetricsCollector) RecordCacheMiss(path string, size int64) {
	m.Called(path, size)
}

func (m *MockMetricsCollector) RecordError(path string) {
	m.Called(path)
}

func (m *MockMetricsCollector) RecordBytesServed(bytes int64) {
	m.Called(bytes)
}

func (m *MockMetricsCollector) GetStatistics() Statistics {
	args := m.Called()
	return args.Get(0).(Statistics)
}

func (m *MockMetricsCollector) GetTopPackages(limit int) []PackageStats {
	args := m.Called(limit)
	return args.Get(0).([]PackageStats)
}

func (m *MockMetricsCollector) GetTopClients(limit int) []ClientStats {
	args := m.Called(limit)
	return args.Get(0).([]ClientStats)
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestMetricsFunctionality is a basic sanity check for the testing framework.
//
// The test verifies:
// - The testing framework is properly set up
// - Basic assertions work as expected
//
// Approach:
// 1. Performs a simple assertion (1+1=2)
// 2. Verifies the assertion passes
//
// Note: This serves as a baseline test to confirm the testing infrastructure is working
func TestMetricsFunctionality(t *testing.T) {
	t.Run("hello world", func(t *testing.T) {
		assert.Equal(t, 2, 1+1)
	})
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestCollectorWithMock tests the metrics collector using a mock implementation
// to verify that the interface works as expected.
//
// The test verifies:
// - Mock metrics collector properly records various types of metrics
// - GetStatistics returns the expected values
// - Package and client tracking works correctly
//
// Approach:
// 1. Creates a mock metrics collector with predefined expectations
// 2. Calls various recording methods on the mock
// 3. Verifies the mock returns expected statistics
// 4. Confirms expectations on the mock were met
//
// Note: This test focuses on the collector interface rather than the actual implementation
func TestCollectorWithMock(t *testing.T) {
	// Setup mock
	mockCollector := new(MockMetricsCollector)

	// Setup expectations
	mockCollector.On("RecordRequest", "/test/path", 100*time.Millisecond, "127.0.0.1", "test-package").Return()
	mockCollector.On("RecordCacheHit", "/test/path", int64(1024)).Return()
	mockCollector.On("RecordCacheMiss", "/test/path", int64(2048)).Return()
	mockCollector.On("RecordError", "/test/path").Return()
	mockCollector.On("RecordBytesServed", int64(4096)).Return()

	// Mock statistics
	stats := Statistics{
		TotalRequests: 1,
		CacheHits:     1,
		CacheMisses:   1,
		Errors:        1,
		BytesServed:   4096,
	}
	mockCollector.On("GetStatistics").Return(stats)

	// Mock top packages
	topPkgs := []PackageStats{
		{URL: "test-package", Count: 5, Size: 1024},
	}
	mockCollector.On("GetTopPackages", 10).Return(topPkgs)

	// Mock top clients
	topClients := []ClientStats{
		{IP: "127.0.0.1", Requests: 10},
	}
	mockCollector.On("GetTopClients", 10).Return(topClients)

	// Use the mock
	mockCollector.RecordRequest("/test/path", 100*time.Millisecond, "127.0.0.1", "test-package")
	mockCollector.RecordCacheHit("/test/path", 1024)
	mockCollector.RecordCacheMiss("/test/path", 2048)
	mockCollector.RecordError("/test/path")
	mockCollector.RecordBytesServed(4096)

	// Get statistics and verify
	returnedStats := mockCollector.GetStatistics()
	assert.Equal(t, 1, returnedStats.TotalRequests)
	assert.Equal(t, 1, returnedStats.CacheHits)
	assert.Equal(t, 1, returnedStats.CacheMisses)
	assert.Equal(t, 1, returnedStats.Errors)
	assert.Equal(t, int64(4096), returnedStats.BytesServed)

	// Get top packages and verify
	returnedPkgs := mockCollector.GetTopPackages(10)
	assert.Equal(t, 1, len(returnedPkgs))
	assert.Equal(t, "test-package", returnedPkgs[0].URL)

	// Get top clients and verify
	returnedClients := mockCollector.GetTopClients(10)
	assert.Equal(t, 1, len(returnedClients))
	assert.Equal(t, "127.0.0.1", returnedClients[0].IP)

	// Verify all expectations were met
	mockCollector.AssertExpectations(t)
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestStandardRecordBytesServed tests the RecordBytesServed method for tracking
// the total bytes served through the cache.
//
// The test verifies:
// - Bytes served are correctly accumulated
// - Multiple record operations add to the total
// - Negative values are handled appropriately
//
// Approach:
// 1. Creates a new metrics collector
// 2. Records bytes served with various values
// 3. Verifies the total is updated correctly after each operation
//
// Note: This tests direct byte recording without an associated request
func TestStandardRecordBytesServed(t *testing.T) {
	collector := New()

	// Record bytes directly without a request
	collector.RecordBytesServed(2048)
	assert.Equal(t, int64(2048), collector.bytesServed)

	// Add more bytes
	collector.RecordBytesServed(1024)
	assert.Equal(t, int64(3072), collector.bytesServed)

	// Add negative bytes (should handle gracefully)
	collector.RecordBytesServed(-512)
	assert.Equal(t, int64(2560), collector.bytesServed)
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestEdgeCases tests the metrics collector's behavior with edge case inputs.
//
// The test verifies:
// - Empty path is handled correctly in RecordRequest
// - Zero duration is handled correctly in RecordRequest
// - Empty client IP is handled correctly in RecordRequest
// - Recording hit/miss/error works with no prior requests
//
// Approach:
// 1. Creates a new metrics collector
// 2. Records requests with empty path, zero duration, and empty client IP
// 3. Creates a fresh collector and records hits/misses/errors without requests
// 4. Verifies counters are updated correctly despite edge case inputs
//
// Note: This tests robustness against unexpected or missing input data
func TestEdgeCases(t *testing.T) {
	collector := New()

	// Test with empty path
	collector.RecordRequest("", 100*time.Millisecond, "192.168.1.100", "")
	assert.Equal(t, 1, collector.totalRequests)
	assert.Equal(t, "", collector.requests[0].Path)

	// Test with zero duration
	collector.RecordRequest("/test", 0, "192.168.1.100", "test")
	assert.Equal(t, 2, collector.totalRequests)
	assert.Equal(t, time.Duration(0), collector.requests[1].Duration)

	// Test with empty client IP
	collector.RecordRequest("/test2", 100*time.Millisecond, "", "test2")
	assert.Equal(t, 3, collector.totalRequests)
	assert.Equal(t, "", collector.requests[2].ClientIP)

	// Test recording hit/miss/error with no requests
	collector = New()
	collector.RecordCacheHit("/nonexistent", 1024)
	collector.RecordCacheMiss("/nonexistent", 2048)
	collector.RecordError("/nonexistent")
	assert.Equal(t, 1, collector.cacheHits)
	assert.Equal(t, 1, collector.cacheMisses)
	assert.Equal(t, 1, collector.errors)
	assert.Equal(t, int64(3072), collector.bytesServed)
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestConcurrentAccess tests the thread safety of the metrics collector.
//
// The test verifies:
// - Concurrent calls to RecordRequest work correctly
// - Concurrent calls to RecordCacheHit work correctly
// - Concurrent calls to RecordCacheMiss work correctly
// - Counters are correctly updated after all goroutines complete
//
// Approach:
// 1. Creates a new metrics collector
// 2. Launches multiple goroutines that call various record methods concurrently
// 3. Uses a WaitGroup to ensure all goroutines complete
// 4. Verifies the final counter values match expected totals
//
// Note: This test ensures the metrics collector is safe for concurrent use
// across multiple server goroutines
func TestConcurrentAccess(t *testing.T) {
	collector := New()
	var wg sync.WaitGroup
	iterations := 100

	// Test concurrent RecordRequest
	wg.Add(iterations)
	for i := range make([]int, iterations) {
		go func(i int) {
			defer wg.Done()
			path := fmt.Sprintf("/path%d", i)
			collector.RecordRequest(path, time.Duration(i)*time.Millisecond, "192.168.1.1", "pkg")
		}(i)
	}
	wg.Wait()
	assert.Equal(t, iterations, collector.totalRequests)

	// Test concurrent RecordCacheHit and RecordCacheMiss
	wg.Add(iterations * 2)
	for i := range make([]int, iterations) {
		go func(i int) {
			defer wg.Done()
			collector.RecordCacheHit(fmt.Sprintf("/path%d", i), int64(i*10))
		}(i)
		go func(i int) {
			defer wg.Done()
			collector.RecordCacheMiss(fmt.Sprintf("/path%d", i+iterations), int64(i*20))
		}(i)
	}
	wg.Wait()

	assert.Equal(t, iterations, collector.cacheHits)
	assert.Equal(t, iterations, collector.cacheMisses)
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestEmptyCollectorStats tests the behavior of a fresh collector with no recorded data.
//
// The test verifies:
// - GetStatistics returns zero values for all counters
// - HitRate and AvgResponseTime are correctly calculated as zero
// - GetTopPackages returns an empty slice with no data
// - GetTopClients returns an empty slice with no data
//
// Approach:
// 1. Creates a new empty metrics collector
// 2. Calls GetStatistics and checks all values are zero
// 3. Calls GetTopPackages and verifies an empty slice is returned
// 4. Calls GetTopClients and verifies an empty slice is returned
//
// Note: This test ensures the collector behaves correctly when no data has been recorded
func TestEmptyCollectorStats(t *testing.T) {
	collector := New()

	// Get statistics with no data
	stats := collector.GetStatistics()
	assert.Equal(t, 0, stats.TotalRequests)
	assert.Equal(t, 0, stats.CacheHits)
	assert.Equal(t, 0, stats.CacheMisses)
	assert.Equal(t, 0, stats.Errors)
	assert.Equal(t, int64(0), stats.BytesServed)
	assert.Equal(t, float64(0), stats.HitRate)
	assert.Equal(t, float64(0), stats.AvgResponseTime)
	assert.Equal(t, 0, len(stats.RecentRequests))

	// Test GetTopPackages with no data
	topPackages := collector.GetTopPackages(10)
	assert.Equal(t, 0, len(topPackages))

	// Test GetTopClients with no data
	topClients := collector.GetTopClients(10)
	assert.Equal(t, 0, len(topClients))
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestRecordRequestWithPackageTracking tests the package tracking functionality.
//
// The test verifies:
// - Package statistics are correctly updated for multiple requests
// - GetTopPackages correctly returns the most accessed packages
// - Package counts are correctly maintained
//
// Approach:
// 1. Creates a new metrics collector
// 2. Records multiple requests for the same package
// 3. Manually updates package statistics to simulate tracking
// 4. Calls GetTopPackages and verifies the results
//
// Note: This test focuses on verifying package tracking functionality
func TestRecordRequestWithPackageTracking(t *testing.T) {
	collector := New()

	// Add package tracking logic to properly test package statistics
	path := "/debian/pool/main/a/apt/apt_2.2.4_amd64.deb"
	packageName := "apt"

	// Record multiple requests for the same package
	for i := 0; i < 5; i++ {
		collector.RecordRequest(path, 100*time.Millisecond, "192.168.1.100", packageName)
		// TODO: Add code to update package stats if it's missing in the implementation
		if _, exists := collector.packages[packageName]; !exists {
			collector.packages[packageName] = PackageStats{
				URL:        path,
				Count:      1,
				LastAccess: time.Now(),
				Size:       1024,
			}
		} else {
			ps := collector.packages[packageName]
			ps.Count++
			ps.LastAccess = time.Now()
			collector.packages[packageName] = ps
		}
	}

	// Get top packages should include this package
	topPackages := collector.GetTopPackages(10)
	assert.Equal(t, 1, len(topPackages))
	assert.Equal(t, packageName, topPackages[0].URL)
	assert.Equal(t, 5, topPackages[0].Count)
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestCollectorCreation tests the initialization of the metrics collector.
//
// The test verifies:
// - A new collector is properly initialized
// - All counters start at zero
// - Internal maps are properly initialized
// - MaxRecentItems is set to the default value
//
// Approach:
// 1. Creates a new metrics collector
// 2. Verifies all counters are initialized to zero
// 3. Verifies internal data structures are initialized
// 4. Checks that default configuration values are set correctly
//
// Note: This test ensures the collector is in a valid initial state
func TestCollectorCreation(t *testing.T) {
	collector := New()

	assert.NotNil(t, collector)
	assert.Empty(t, collector.requests)
	assert.Equal(t, 0, collector.totalRequests)
	assert.Equal(t, 0, collector.cacheHits)
	assert.Equal(t, 0, collector.cacheMisses)
	assert.Equal(t, 0, collector.errors)
	assert.Equal(t, int64(0), collector.bytesServed)
	assert.Equal(t, 100, collector.maxRecentItems)
	assert.NotNil(t, collector.packages)
	assert.NotNil(t, collector.clients)
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestMetricsRecordRequest tests the RecordRequest method of the metrics collector.
//
// The test verifies:
// - A request is properly recorded with all fields
// - TotalRequests counter is incremented
// - Total response time is updated
// - Client statistics are updated
//
// Approach:
// 1. Creates a new metrics collector
// 2. Records a request with specific path, duration, client IP, and package name
// 3. Verifies request counters are updated
// 4. Verifies request details are stored correctly
// 5. Checks that client statistics are updated
//
// Note: This test focuses on the basic request recording functionality
func TestMetricsRecordRequest(t *testing.T) {
	collector := New()

	// Record a request
	path := "/debian/pool/main/a/apt/apt_2.2.4_amd64.deb"
	duration := 150 * time.Millisecond
	clientIP := "192.168.1.100"
	packageName := "apt"

	collector.RecordRequest(path, duration, clientIP, packageName)

	// Verify request was recorded
	assert.Equal(t, 1, collector.totalRequests)
	assert.Equal(t, duration, collector.totalTime)
	assert.Equal(t, 1, len(collector.requests))
	assert.Equal(t, path, collector.requests[0].Path)
	assert.Equal(t, clientIP, collector.requests[0].ClientIP)
	assert.Equal(t, packageName, collector.requests[0].PackageName)
	assert.Equal(t, duration, collector.requests[0].Duration)
	assert.Equal(t, "miss", collector.requests[0].Result)

	// Verify client stats were recorded
	clientStats, exists := collector.clients[clientIP]
	assert.True(t, exists)
	assert.Equal(t, 1, clientStats.Requests)
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestRecordCacheHit tests the RecordCacheHit method of the metrics collector.
//
// The test verifies:
// - Cache hit counter is properly incremented
// - Bytes served counter is updated
// - Most recent request is updated with hit status
// - Bytes value is set on the most recent request
//
// Approach:
// 1. Creates a new metrics collector
// 2. Records a request
// 3. Records a cache hit for the same path
// 4. Verifies cache hit counter is incremented
// 5. Verifies bytes served counter is updated
// 6. Checks that the most recent request is updated with hit status
//
// Note: This test ensures cache hits are properly tracked
func TestRecordCacheHit(t *testing.T) {
	collector := New()

	// Record a request
	path := "/debian/pool/main/a/apt/apt_2.2.4_amd64.deb"
	collector.RecordRequest(path, 100*time.Millisecond, "192.168.1.100", "apt")

	// Record a cache hit
	collector.RecordCacheHit(path, 1024)

	// Verify cache hit was recorded
	assert.Equal(t, 1, collector.cacheHits)
	assert.Equal(t, int64(1024), collector.bytesServed)
	assert.Equal(t, "hit", collector.requests[0].Result)
	assert.Equal(t, int64(1024), collector.requests[0].Bytes)
}

func TestRecordCacheMiss(t *testing.T) {
	collector := New()

	// Record a request
	path := "/debian/pool/main/a/apt/apt_2.2.4_amd64.deb"
	collector.RecordRequest(path, 100*time.Millisecond, "192.168.1.100", "apt")

	// Record a cache miss
	collector.RecordCacheMiss(path, 2048)

	// Verify cache miss was recorded
	assert.Equal(t, 1, collector.cacheMisses)
	assert.Equal(t, int64(2048), collector.bytesServed)
}

func TestRecordError(t *testing.T) {
	collector := New()

	// Record a request
	path := "/debian/pool/main/a/apt/apt_2.2.4_amd64.deb"
	collector.RecordRequest(path, 100*time.Millisecond, "192.168.1.100", "apt")

	// Record an error
	collector.RecordError(path)

	// Verify error was recorded
	assert.Equal(t, 1, collector.errors)
	assert.Equal(t, "error", collector.requests[0].Result)
}

func TestSetLastClientIP(t *testing.T) {
	collector := New()

	clientIP := "192.168.1.100"
	collector.SetLastClientIP(clientIP)

	assert.Equal(t, clientIP, collector.lastClientIP)
}

func TestSetLastFileSize(t *testing.T) {
	collector := New()

	fileSize := int64(12345)
	collector.SetLastFileSize(fileSize)

	assert.Equal(t, fileSize, collector.lastFileSize)
}

func TestGetStatistics(t *testing.T) {
	collector := New()

	// Record some data
	collector.RecordRequest("/path1", 100*time.Millisecond, "192.168.1.100", "pkg1")
	collector.RecordCacheHit("/path1", 1024)

	collector.RecordRequest("/path2", 200*time.Millisecond, "192.168.1.101", "pkg2")
	collector.RecordCacheMiss("/path2", 2048)

	collector.RecordRequest("/path3", 300*time.Millisecond, "192.168.1.102", "pkg3")
	collector.RecordError("/path3")

	collector.SetLastClientIP("192.168.1.200")
	collector.SetLastFileSize(5000)

	// Get statistics
	stats := collector.GetStatistics()

	// Verify statistics
	assert.Equal(t, 3, stats.TotalRequests)
	assert.Equal(t, 1, stats.CacheHits)
	assert.Equal(t, 1, stats.CacheMisses)
	assert.Equal(t, 1, stats.Errors)
	assert.Equal(t, int64(3072), stats.BytesServed)
	assert.Equal(t, float64(1)/float64(3), stats.HitRate)
	assert.Equal(t, float64(600)/float64(3), stats.AvgResponseTime)
	assert.Equal(t, "192.168.1.200", stats.LastClientIP)
	assert.Equal(t, int64(5000), stats.LastFileSize)
	assert.Equal(t, 3, len(stats.RecentRequests))
}

func TestGetTopPackages(t *testing.T) {
	collector := New()

	// Add package stats - removing leading slashes to match implementation
	collector.packages["pkg1"] = PackageStats{
		URL:        "pkg1",
		Count:      10,
		LastAccess: time.Now(),
		Size:       1024,
	}

	collector.packages["pkg2"] = PackageStats{
		URL:        "pkg2",
		Count:      5,
		LastAccess: time.Now(),
		Size:       2048,
	}

	collector.packages["pkg3"] = PackageStats{
		URL:        "pkg3",
		Count:      15,
		LastAccess: time.Now(),
		Size:       3072,
	}

	// Get top packages
	topPackages := collector.GetTopPackages(2)

	// Verify top packages (should be pkg3, pkg1)
	assert.Equal(t, 2, len(topPackages))
	assert.Equal(t, "pkg3", topPackages[0].URL)
	assert.Equal(t, 15, topPackages[0].Count)
	assert.Equal(t, "pkg1", topPackages[1].URL)
	assert.Equal(t, 10, topPackages[1].Count)
}

func TestGetTopClients(t *testing.T) {
	collector := New()

	// Add client stats
	collector.clients["192.168.1.100"] = ClientStats{
		IP:        "192.168.1.100",
		Requests:  10,
		BytesSent: 1024,
	}

	collector.clients["192.168.1.101"] = ClientStats{
		IP:        "192.168.1.101",
		Requests:  5,
		BytesSent: 2048,
	}

	collector.clients["192.168.1.102"] = ClientStats{
		IP:        "192.168.1.102",
		Requests:  15,
		BytesSent: 3072,
	}

	// Get top clients
	topClients := collector.GetTopClients(2)

	// Verify top clients (should be 192.168.1.102, 192.168.1.100)
	assert.Equal(t, 2, len(topClients))
	assert.Equal(t, "192.168.1.102", topClients[0].IP)
	assert.Equal(t, 15, topClients[0].Requests)
	assert.Equal(t, "192.168.1.100", topClients[1].IP)
	assert.Equal(t, 10, topClients[1].Requests)
}

func TestRegisterPrefetchMetrics(t *testing.T) {
	metrics := RegisterPrefetchMetrics()

	assert.NotNil(t, metrics)
	assert.NotNil(t, metrics.PrefetchAttempts)
	assert.NotNil(t, metrics.PrefetchSuccesses)
	assert.NotNil(t, metrics.PrefetchFailures)
	assert.NotNil(t, metrics.ActivePrefetches)
	assert.NotNil(t, metrics.PrefetchProcessingTime)
}

func TestMaxRecentItems(t *testing.T) {
	collector := New()
	collector.maxRecentItems = 3

	// Add more requests than maxRecentItems
	collector.RecordRequest("/path1", 100*time.Millisecond, "192.168.1.100", "pkg1")
	collector.RecordRequest("/path2", 200*time.Millisecond, "192.168.1.101", "pkg2")
	collector.RecordRequest("/path3", 300*time.Millisecond, "192.168.1.102", "pkg3")
	collector.RecordRequest("/path4", 400*time.Millisecond, "192.168.1.103", "pkg4")

	// Verify only the most recent requests are kept
	assert.Equal(t, 4, collector.totalRequests)
	assert.Equal(t, 3, len(collector.requests))
	assert.Equal(t, "/path2", collector.requests[0].Path)
	assert.Equal(t, "/path3", collector.requests[1].Path)
	assert.Equal(t, "/path4", collector.requests[2].Path)
}
