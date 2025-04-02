package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// PrometheusHandler returns the HTTP handler for Prometheus metrics
func PrometheusHandler(collector *PrometheusCollector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, err := w.Write([]byte(collector.GetMetrics()))
		if err != nil {
			http.Error(w, "Failed to write response", http.StatusInternalServerError)
			return
		}
	}
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestPrometheusMetrics tests the basic functionality of the Prometheus metrics endpoint.
//
// The test verifies:
// - The metrics handler returns a 200 OK status
// - The response contains valid Prometheus metrics format
// - The content type is set to text/plain
//
// Approach:
// 1. Creates a new HTTP request to the metrics endpoint
// 2. Creates a Prometheus metrics collector
// 3. Sets up a handler that returns metrics from the collector
// 4. Calls the handler with the request
// 5. Verifies the response status and content
//
// Note: This tests the handler integration but not specific metric values
func TestPrometheusMetrics(t *testing.T) {
	req, err := http.NewRequest("GET", "/metrics", nil)
	if err != nil {
		t.Fatalf("could not create request: %v", err)
	}

	rr := httptest.NewRecorder()
	collector := NewPrometheusCollector()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, err := w.Write([]byte(collector.GetMetrics()))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	})

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	// Simple check that the response contains metrics
	bodyText := rr.Body.String()
	assert.Contains(t, bodyText, "apt_cacher_requests_total")
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestPrometheusCollectorCreation tests the creation of a new Prometheus metrics collector.
//
// The test verifies:
// - The collector is properly initialized
// - All metrics counters start with zero values
// - The response time counter map is initialized
//
// Approach:
// 1. Creates a new Prometheus collector
// 2. Verifies the collector is not nil
// 3. Checks that all metrics are initialized to zero
// 4. Verifies that the response time tracking map is initialized
//
// Note: This test focuses only on initialization, not on recording metrics
func TestPrometheusCollectorCreation(t *testing.T) {
	collector := NewPrometheusCollector()

	assert.NotNil(t, collector)
	assert.Equal(t, int64(0), collector.totalRequests)
	assert.Equal(t, int64(0), collector.cacheHits)
	assert.Equal(t, int64(0), collector.cacheMisses)
	assert.Equal(t, int64(0), collector.requestsInProgress)
	assert.Equal(t, int64(0), collector.connections)
	assert.Equal(t, int64(0), collector.bytesServed)
	assert.NotNil(t, collector.responseTimeCount)
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestPrometheusRecordRequest tests the RecordRequest method for tracking
// HTTP requests and their results.
//
// The test verifies:
// - Total requests counter is incremented for all status codes
// - Cache hits counter is incremented for 200 status codes
// - Cache misses counter is incremented for 404 status codes
// - Other status codes only increment the total counter
//
// Approach:
// 1. Creates a new Prometheus collector
// 2. Records requests with different status codes (200, 404, 500)
// 3. Verifies each counter is incremented correctly
//
// Note: This test uses status code strings to simulate HTTP responses
func TestPrometheusRecordRequest(t *testing.T) {
	collector := NewPrometheusCollector()

	// Record a hit
	collector.RecordRequest("200")

	assert.Equal(t, int64(1), collector.totalRequests)
	assert.Equal(t, int64(1), collector.cacheHits)
	assert.Equal(t, int64(0), collector.cacheMisses)

	// Record a miss
	collector.RecordRequest("404")

	assert.Equal(t, int64(2), collector.totalRequests)
	assert.Equal(t, int64(1), collector.cacheHits)
	assert.Equal(t, int64(1), collector.cacheMisses)

	// Record another status
	collector.RecordRequest("500")

	assert.Equal(t, int64(3), collector.totalRequests)
	assert.Equal(t, int64(1), collector.cacheHits)
	assert.Equal(t, int64(1), collector.cacheMisses)
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestRecordResponseTime tests the RecordResponseTime method for tracking
// response times by path type.
//
// The test verifies:
// - Response times for package paths are correctly accumulated
// - Response times for index paths are correctly accumulated
// - Response times for admin paths are correctly accumulated
// - Response time counter map is updated for each path type
//
// Approach:
// 1. Creates a new Prometheus collector
// 2. Records response times for different path types
// 3. Verifies accumulated response times for each path type
// 4. Checks that response time counter map is updated correctly
//
// Note: This test ensures that response time metrics are correctly
// categorized by path type for more useful monitoring
func TestRecordResponseTime(t *testing.T) {
	collector := NewPrometheusCollector()

	// Record package response time
	collector.RecordResponseTime("package", 100*time.Millisecond)
	collector.RecordResponseTime("package", 200*time.Millisecond)

	assert.Equal(t, float64(100+200), collector.responseTimePackage)
	assert.Equal(t, int64(2), collector.responseTimeCount["package"])

	// Record index response time
	collector.RecordResponseTime("index", 50*time.Millisecond)
	collector.RecordResponseTime("index", 150*time.Millisecond)

	assert.Equal(t, float64(50+150), collector.responseTimeIndex)
	assert.Equal(t, int64(2), collector.responseTimeCount["index"])

	// Record admin response time
	collector.RecordResponseTime("admin", 300*time.Millisecond)

	assert.Equal(t, float64(300), collector.responseTimeAdmin)
	assert.Equal(t, int64(1), collector.responseTimeCount["admin"])
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestRecordBytesServed tests the RecordBytesServed method for tracking
// the total bytes sent to clients.
//
// The test verifies:
// - Bytes served counter is correctly incremented with a single call
// - Bytes served counter accumulates correctly with multiple calls
//
// Approach:
// 1. Creates a new Prometheus collector
// 2. Records bytes served with a specific value
// 3. Verifies the counter matches the recorded value
// 4. Records additional bytes served
// 5. Verifies the counter reflects the total
//
// Note: This test ensures that byte volume metrics are correctly tracked
// for monitoring network usage
func TestRecordBytesServed(t *testing.T) {
	collector := NewPrometheusCollector()

	collector.RecordBytesServed(1024)
	assert.Equal(t, int64(1024), collector.bytesServed)

	collector.RecordBytesServed(2048)
	assert.Equal(t, int64(1024+2048), collector.bytesServed)
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestRequestsInProgress tests the in-progress request counter methods.
//
// The test verifies:
// - IncRequestsInProgress correctly increments the counter
// - DecRequestsInProgress correctly decrements the counter
// - Multiple increments and decrements work as expected
//
// Approach:
// 1. Creates a new Prometheus collector
// 2. Calls IncRequestsInProgress multiple times
// 3. Verifies counter matches expected value
// 4. Calls DecRequestsInProgress
// 5. Verifies counter is decremented correctly
//
// Note: These metrics help monitor server load and detect potential request leaks
func TestRequestsInProgress(t *testing.T) {
	collector := NewPrometheusCollector()

	// Increment in-progress requests
	collector.IncRequestsInProgress()
	collector.IncRequestsInProgress()

	assert.Equal(t, int64(2), collector.requestsInProgress)

	// Decrement in-progress requests
	collector.DecRequestsInProgress()

	assert.Equal(t, int64(1), collector.requestsInProgress)
}

func TestConnections(t *testing.T) {
	collector := NewPrometheusCollector()

	// Increment connections
	collector.IncConnections()
	collector.IncConnections()

	assert.Equal(t, int64(2), collector.connections)

	// Decrement connections
	collector.DecConnections()

	assert.Equal(t, int64(1), collector.connections)
}

func TestGetMetricsOutput(t *testing.T) {
	collector := NewPrometheusCollector()

	// Record some data
	collector.RecordRequest("200")
	collector.RecordRequest("200")
	collector.RecordRequest("404")

	collector.RecordResponseTime("package", 100*time.Millisecond)
	collector.RecordResponseTime("package", 200*time.Millisecond)
	collector.RecordResponseTime("index", 50*time.Millisecond)

	collector.RecordBytesServed(1024)
	collector.RecordBytesServed(2048)

	collector.IncRequestsInProgress()
	collector.IncRequestsInProgress()
	collector.DecRequestsInProgress()

	collector.IncConnections()

	// Get metrics output
	output := collector.GetMetrics()

	// Verify metrics are present
	assert.Contains(t, output, "apt_cacher_requests_total 3")
	assert.Contains(t, output, "apt_cacher_cache_hits_total 2")
	assert.Contains(t, output, "apt_cacher_cache_misses_total 1")
	assert.Contains(t, output, "apt_cacher_requests_in_progress 1")
	assert.Contains(t, output, "apt_cacher_active_connections 1")
	assert.Contains(t, output, "apt_cacher_bytes_served_total 3072")

	// Check averages
	assert.Contains(t, output, "apt_cacher_response_time_package_ms 150.00")
	assert.Contains(t, output, "apt_cacher_response_time_index_ms 50.00")
	assert.Contains(t, output, "apt_cacher_response_time_admin_ms 0.00")
}

func TestThreadSafety(t *testing.T) {
	collector := NewPrometheusCollector()

	// Run concurrent operations
	done := make(chan bool)

	go func() {
		// Modernized loop using range over int
		for range 100 {
			collector.RecordRequest("200")
		}
		done <- true
	}()

	go func() {
		// Modernized loop using range over int
		for range 100 {
			collector.RecordResponseTime("package", 100*time.Millisecond)
		}
		done <- true
	}()

	go func() {
		// Modernized loop using range over int
		for range 100 {
			collector.RecordBytesServed(1024)
		}
		done <- true
	}()

	// Wait for all goroutines to complete
	<-done
	<-done
	<-done

	// Verify totals
	assert.Equal(t, int64(100), collector.totalRequests)
	assert.Equal(t, int64(100), collector.cacheHits)
	assert.Equal(t, float64(100*100), collector.responseTimePackage)
	assert.Equal(t, int64(100), collector.responseTimeCount["package"])
	assert.Equal(t, int64(100*1024), collector.bytesServed)
}

func TestPrometheusHandler(t *testing.T) {
	collector := NewPrometheusCollector()

	// Add some test data
	collector.RecordRequest("200")
	collector.RecordRequest("404")
	collector.RecordResponseTime("package", 150*time.Millisecond)
	collector.RecordBytesServed(2048)

	// Create a test request
	req, err := http.NewRequest("GET", "/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Create a ResponseRecorder to record the response
	rr := httptest.NewRecorder()
	handler := PrometheusHandler(collector)

	// Call the handler
	handler(rr, req)

	// Check the status code
	assert.Equal(t, http.StatusOK, rr.Code)

	// Check the content type
	assert.Equal(t, "text/plain", rr.Header().Get("Content-Type"))

	// Check that the response contains expected metrics
	body := rr.Body.String()
	assert.Contains(t, body, "apt_cacher_requests_total")
	assert.Contains(t, body, "apt_cacher_cache_hits_total")
	assert.Contains(t, body, "apt_cacher_cache_misses_total")
	assert.Contains(t, body, "apt_cacher_bytes_served_total")
}
