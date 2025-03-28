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

func TestRecordBytesServed(t *testing.T) {
	collector := NewPrometheusCollector()

	collector.RecordBytesServed(1024)
	assert.Equal(t, int64(1024), collector.bytesServed)

	collector.RecordBytesServed(2048)
	assert.Equal(t, int64(1024+2048), collector.bytesServed)
}

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
