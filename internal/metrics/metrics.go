package metrics

import (
	"sort"
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
	LastClientIP    string // New field to track last client IP
	LastFileSize    int64  // New field to track last file size
}

// PackageStats tracks statistics about a package
type PackageStats struct {
	URL        string
	Count      int
	LastAccess time.Time
	Size       int64
}

// ClientStats tracks statistics about a client
type ClientStats struct {
	IP        string
	Requests  int
	BytesSent int64
}

// TopPackage represents package statistics for display or sorting
type TopPackage struct {
	URL        string
	Count      int
	LastAccess time.Time
	Size       int64
}

// TopClient represents client statistics for display or sorting
type TopClient struct {
	IP        string
	Requests  int
	BytesSent int64
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
	packages       map[string]PackageStats
	clients        map[string]ClientStats
	lastClientIP   string // New field to track last client IP
	lastFileSize   int64  // New field to track last file size
}

// New creates a new metrics collector
func New() *Collector {
	return &Collector{
		requests:       make([]RequestInfo, 0),
		maxRecentItems: 100, // Keep last 100 requests
		packages:       make(map[string]PackageStats),
		clients:        make(map[string]ClientStats),
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

// SetLastClientIP sets the last client IP
func (c *Collector) SetLastClientIP(ip string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.lastClientIP = ip
}

// SetLastFileSize sets the last file size
func (c *Collector) SetLastFileSize(size int64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.lastFileSize = size
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
		LastClientIP:   c.lastClientIP, // Add this field
		LastFileSize:   c.lastFileSize, // Add this field
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

// GetTopPackages returns the most frequently accessed packages
func (c *Collector) GetTopPackages(limit int) []TopPackage {
	packages := make([]TopPackage, 0, len(c.packages))

	for url, stats := range c.packages {
		packages = append(packages, TopPackage{
			URL:        url,
			Count:      stats.Count,
			LastAccess: stats.LastAccess,
			Size:       stats.Size,
		})
	}

	// Sort by count in descending order
	sort.Slice(packages, func(i, j int) bool {
		return packages[i].Count > packages[j].Count
	})

	// Limit the result
	if limit > 0 && len(packages) > limit {
		packages = packages[:limit]
	}

	return packages
}

// GetTopClients returns the clients with the most requests
func (c *Collector) GetTopClients(limit int) []TopClient {
	clients := make([]TopClient, 0, len(c.clients))

	for ip, stats := range c.clients {
		clients = append(clients, TopClient{
			IP:        ip,
			Requests:  stats.Requests,
			BytesSent: stats.BytesSent,
		})
	}

	// Sort by requests in descending order
	sort.Slice(clients, func(i, j int) bool {
		return clients[i].Requests > clients[j].Requests
	})

	// Limit the result
	if limit > 0 && len(clients) > limit {
		clients = clients[:limit]
	}

	return clients
}
