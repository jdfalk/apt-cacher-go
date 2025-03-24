package metrics

import (
	"fmt"
	"sync"
	"time"
)

// PrometheusCollector collects metrics in Prometheus format
type PrometheusCollector struct {
	// Metrics
	totalRequests      int64
	cacheHits          int64
	cacheMisses        int64
	requestsInProgress int64
	connections        int64
	bytesServed        int64

	// Response time tracking
	responseTimePackage float64
	responseTimeIndex   float64
	responseTimeAdmin   float64
	responseTimeCount   map[string]int64

	// Synchronization for thread-safe operations
	mutex sync.Mutex
}

// NewPrometheusCollector creates a new prometheus metrics collector
func NewPrometheusCollector() *PrometheusCollector {
	return &PrometheusCollector{
		responseTimeCount: make(map[string]int64),
	}
}

// RecordRequest records a request with its result
func (p *PrometheusCollector) RecordRequest(status string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.totalRequests++

	if status == "200" {
		p.cacheHits++
	} else if status == "404" {
		p.cacheMisses++
	}
}

// RecordResponseTime records response time for a specific path type
func (p *PrometheusCollector) RecordResponseTime(pathType string, duration time.Duration) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	ms := float64(duration.Milliseconds())

	switch pathType {
	case "package":
		p.responseTimePackage += ms
		p.responseTimeCount["package"]++
	case "index":
		p.responseTimeIndex += ms
		p.responseTimeCount["index"]++
	case "admin":
		p.responseTimeAdmin += ms
		p.responseTimeCount["admin"]++
	}
}

// RecordBytesServed records bytes sent to clients
func (p *PrometheusCollector) RecordBytesServed(bytes int64) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.bytesServed += bytes
}

// IncRequestsInProgress increments the in-progress request counter
func (p *PrometheusCollector) IncRequestsInProgress() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.requestsInProgress++
}

// DecRequestsInProgress decrements the in-progress request counter
func (p *PrometheusCollector) DecRequestsInProgress() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.requestsInProgress--
}

// IncConnections increments the active connections counter
func (p *PrometheusCollector) IncConnections() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.connections++
}

// DecConnections decrements the active connections counter
func (p *PrometheusCollector) DecConnections() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.connections--
}

// GetMetrics returns all metrics in Prometheus format
func (p *PrometheusCollector) GetMetrics() string {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	var avgPackage, avgIndex, avgAdmin float64

	if p.responseTimeCount["package"] > 0 {
		avgPackage = p.responseTimePackage / float64(p.responseTimeCount["package"])
	}

	if p.responseTimeCount["index"] > 0 {
		avgIndex = p.responseTimeIndex / float64(p.responseTimeCount["index"])
	}

	if p.responseTimeCount["admin"] > 0 {
		avgAdmin = p.responseTimeAdmin / float64(p.responseTimeCount["admin"])
	}

	return fmt.Sprintf(`# HELP apt_cacher_requests_total Total number of HTTP requests
# TYPE apt_cacher_requests_total counter
apt_cacher_requests_total %d

# HELP apt_cacher_cache_hits_total Total number of cache hits
# TYPE apt_cacher_cache_hits_total counter
apt_cacher_cache_hits_total %d

# HELP apt_cacher_cache_misses_total Total number of cache misses
# TYPE apt_cacher_cache_misses_total counter
apt_cacher_cache_misses_total %d

# HELP apt_cacher_requests_in_progress Current number of requests being processed
# TYPE apt_cacher_requests_in_progress gauge
apt_cacher_requests_in_progress %d

# HELP apt_cacher_active_connections Current number of active connections
# TYPE apt_cacher_active_connections gauge
apt_cacher_active_connections %d

# HELP apt_cacher_bytes_served_total Total number of bytes served
# TYPE apt_cacher_bytes_served_total counter
apt_cacher_bytes_served_total %d

# HELP apt_cacher_response_time_package_ms Average response time for package requests in ms
# TYPE apt_cacher_response_time_package_ms gauge
apt_cacher_response_time_package_ms %.2f

# HELP apt_cacher_response_time_index_ms Average response time for index requests in ms
# TYPE apt_cacher_response_time_index_ms gauge
apt_cacher_response_time_index_ms %.2f

# HELP apt_cacher_response_time_admin_ms Average response time for admin requests in ms
# TYPE apt_cacher_response_time_admin_ms gauge
apt_cacher_response_time_admin_ms %.2f
`,
		p.totalRequests,
		p.cacheHits,
		p.cacheMisses,
		p.requestsInProgress,
		p.connections,
		p.bytesServed,
		avgPackage,
		avgIndex,
		avgAdmin)
}
