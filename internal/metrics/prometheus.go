package metrics

import (
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// PrometheusCollector provides Prometheus metrics
type PrometheusCollector struct {
	registry              *prometheus.Registry
	requestsTotal         *prometheus.CounterVec
	cacheHits             *prometheus.Counter
	cacheMisses           *prometheus.Counter
	bytesServed           *prometheus.Counter
	responseTimes         *prometheus.HistogramVec
	cacheSize             *prometheus.Gauge
	currentConnections    *prometheus.Gauge
	requestsInProgress    *prometheus.Gauge
	lastGarbageCollection *prometheus.Gauge
	mutex                 sync.Mutex
}

// NewPrometheusCollector creates a new Prometheus metrics collector
func NewPrometheusCollector() *PrometheusCollector {
	registry := prometheus.NewRegistry()

	requestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "apt_cacher_requests_total",
			Help: "Total number of requests by status",
		},
		[]string{"status"},
	)
	registry.MustRegister(requestsTotal)

	cacheHits := prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "apt_cacher_cache_hits",
			Help: "Total number of cache hits",
		},
	)
	registry.MustRegister(cacheHits)

	cacheMisses := prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "apt_cacher_cache_misses",
			Help: "Total number of cache misses",
		},
	)
	registry.MustRegister(cacheMisses)

	bytesServed := prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "apt_cacher_bytes_served",
			Help: "Total bytes served",
		},
	)
	registry.MustRegister(bytesServed)

	responseTimes := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "apt_cacher_response_time_seconds",
			Help:    "Response time in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"path_type"},
	)
	registry.MustRegister(responseTimes)

	cacheSize := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "apt_cacher_cache_size_bytes",
			Help: "Current cache size in bytes",
		},
	)
	registry.MustRegister(cacheSize)

	currentConnections := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "apt_cacher_current_connections",
			Help: "Current number of active connections",
		},
	)
	registry.MustRegister(currentConnections)

	requestsInProgress := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "apt_cacher_requests_in_progress",
			Help: "Current number of requests being processed",
		},
	)
	registry.MustRegister(requestsInProgress)

	lastGarbageCollection := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "apt_cacher_last_garbage_collection",
			Help: "Timestamp of the last garbage collection run",
		},
	)
	registry.MustRegister(lastGarbageCollection)

	return &PrometheusCollector{
		registry:              registry,
		requestsTotal:         requestsTotal,
		cacheHits:             &cacheHits,
		cacheMisses:           &cacheMisses,
		bytesServed:           &bytesServed,
		responseTimes:         responseTimes,
		cacheSize:             cacheSize,
		currentConnections:    currentConnections,
		requestsInProgress:    requestsInProgress,
		lastGarbageCollection: lastGarbageCollection,
	}
}

// RecordRequest records a request in Prometheus metrics
func (p *PrometheusCollector) RecordRequest(status string) {
	p.requestsTotal.WithLabelValues(status).Inc()
}

// RecordCacheHit records a cache hit
func (p *PrometheusCollector) RecordCacheHit(bytes int64) {
	(*p.cacheHits).Inc()
	p.bytesServed.Add(float64(bytes))
}

// RecordCacheMiss records a cache miss
func (p *PrometheusCollector) RecordCacheMiss(bytes int64) {
	(*p.cacheMisses).Inc()
	p.bytesServed.Add(float64(bytes))
}

// RecordResponseTime records a response time
func (p *PrometheusCollector) RecordResponseTime(pathType string, duration time.Duration) {
	p.responseTimes.WithLabelValues(pathType).Observe(duration.Seconds())
}

// SetCacheSize sets the current cache size
func (p *PrometheusCollector) SetCacheSize(bytes int64) {
	p.cacheSize.Set(float64(bytes))
}

// IncConnections increments the active connections counter
func (p *PrometheusCollector) IncConnections() {
	p.currentConnections.Inc()
}

// DecConnections decrements the active connections counter
func (p *PrometheusCollector) DecConnections() {
	p.currentConnections.Dec()
}

// IncRequestsInProgress increments the in-progress requests counter
func (p *PrometheusCollector) IncRequestsInProgress() {
	p.requestsInProgress.Inc()
}

// DecRequestsInProgress decrements the in-progress requests counter
func (p *PrometheusCollector) DecRequestsInProgress() {
	p.requestsInProgress.Dec()
}

// RecordGarbageCollection records a garbage collection run
func (p *PrometheusCollector) RecordGarbageCollection() {
	p.lastGarbageCollection.SetToCurrentTime()
}

// Handler returns an HTTP handler for Prometheus metrics
func (p *PrometheusCollector) Handler() http.Handler {
	return promhttp.HandlerFor(p.registry, promhttp.HandlerOpts{})
}
