package metrics

import (
	"sync"
	"time"
)

// RequestInfo tracks information about a single request
type RequestInfo struct {
	Path     string
	Time     time.Time
	Duration time.Duration
	Result   string // "hit", "miss", "error"
	Bytes    int64
}

// Statistics holds aggregated statistics
type Statistics struct {
	TotalRequests   int
	CacheHits       int
	CacheMisses     int
	Errors          int
	HitRate         float64
	BytesServed     int64
	AvgResponseTime float64
	RecentRequests  []RequestInfo
}

// Collector tracks and aggregates metrics
type Collector struct {
	mutex          sync.RWMutex
	requests       []RequestInfo
	totalRequests  int
	cacheHits      int
	cacheMisses    int
	errors         int
	bytesServed    int64
	totalTime      time.Duration
	maxRecentItems int
}

// New creates a new metrics collector
func New() *Collector {
	return &Collector{
		requests:       make([]RequestInfo, 0),
		maxRecentItems: 100, // Keep last 100 requests
	}
}

// RecordRequest records information about a request
func (c *Collector) RecordRequest(path string, duration time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	info := RequestInfo{
		Path:     path,
		Time:     time.Now(),
		Duration: duration,
		Result:   "miss", // Default, should be updated by caller
	}

	// Add to recent requests
	c.requests = append(c.requests, info)

	// Trim if too many
	if len(c.requests) > c.maxRecentItems {
		c.requests = c.requests[1:]
	}

	// Update totals
	c.totalRequests++
	c.totalTime += duration
}

// RecordCacheHit records a cache hit
func (c *Collector) RecordCacheHit(path string, bytes int64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.cacheHits++
	c.bytesServed += bytes

	// Update the most recent request
	if len(c.requests) > 0 {
		c.requests[len(c.requests)-1].Result = "hit"
		c.requests[len(c.requests)-1].Bytes = bytes
	}
}

// RecordCacheMiss records a cache miss
func (c *Collector) RecordCacheMiss(path string, bytes int64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.cacheMisses++
	c.bytesServed += bytes

	// Update the most recent request
	if len(c.requests) > 0 {
		c.requests[len(c.requests)-1].Result = "miss"
		c.requests[len(c.requests)-1].Bytes = bytes
	}
}

// RecordError records an error
func (c *Collector) RecordError(path string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.errors++

	// Update the most recent request
	if len(c.requests) > 0 {
		c.requests[len(c.requests)-1].Result = "error"
	}
}

// GetStatistics returns the current statistics
func (c *Collector) GetStatistics() Statistics {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	stats := Statistics{
		TotalRequests:  c.totalRequests,
		CacheHits:      c.cacheHits,
		CacheMisses:    c.cacheMisses,
		Errors:         c.errors,
		BytesServed:    c.bytesServed,
		RecentRequests: make([]RequestInfo, len(c.requests)),
	}

	// Calculate hit rate
	if c.totalRequests > 0 {
		stats.HitRate = float64(c.cacheHits) / float64(c.totalRequests)
	}

	// Calculate average response time
	if c.totalRequests > 0 {
		stats.AvgResponseTime = float64(c.totalTime.Milliseconds()) / float64(c.totalRequests)
	}

	// Copy recent requests
	copy(stats.RecentRequests, c.requests)

	return stats
}
