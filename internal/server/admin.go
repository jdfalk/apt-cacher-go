package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"time"
)

// AdminStats contains statistics for the admin interface
type AdminStats struct {
	CacheSize       int64
	CacheMaxSize    int64
	CachePercentage float64
	CacheItems      int
	BytesServed     int64
	RequestsTotal   int
	RequestsHit     int
	RequestsMiss    int
	HitRate         float64
	UpSince         time.Time
	PackagesTop     []TopPackage
	ClientsTop      []TopClient
}

// TopPackage represents a frequently accessed package
type TopPackage struct {
	URL        string
	Count      int
	LastAccess time.Time
	Size       int64
}

// TopClient represents a client with many requests
type TopClient struct {
	IP        string
	Requests  int
	BytesSent int64
}

// CacheEntry represents an entry in the cache (for search results)
type CacheEntry struct {
	Path       string
	Size       int64
	LastAccess time.Time
	Expires    time.Time
}

// adminDashboard serves the admin dashboard
func (s *Server) adminDashboard(w http.ResponseWriter, r *http.Request) {
	s.mutex.Lock()
	stats := s.metrics.GetStatistics()
	cacheStats, err := s.cache.GetStats()
	s.mutex.Unlock()

	if err != nil {
		http.Error(w, "Failed to retrieve cache statistics", http.StatusInternalServerError)
		log.Printf("Failed to get cache statistics: %v", err)
		return
	}

	// Get memory statistics
	s.mutex.Lock()
	memStats := s.memoryMonitor.GetMemoryUsage()
	s.mutex.Unlock()

	html := fmt.Sprintf(`
    <!DOCTYPE html>
    <html>
    <head>
        <title>apt-cacher-go Admin</title>
        <style>
            body { font-family: Arial, sans-serif; margin: 20px; }
            h1 { color: #333; }
            .stats { background: #f5f5f5; padding: 15px; border-radius: 5px; }
            .actions { margin-top: 20px; }
            button { background: #0066cc; color: white; border: none; padding: 8px 15px; margin-right: 10px; cursor: pointer; }
            table { border-collapse: collapse; width: 100%%; margin-top: 20px; }
            th, td { border: 1px solid #ddd; padding: 8px; }
            th { background: #f2f2f2; }
            tr:nth-child(even) { background: #f9f9f9; }
            .truncate { max-width: 300px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
        </style>
    </head>
    <body>
        <h1>apt-cacher-go Administration</h1>

        <div class="stats">
            <h2>Statistics</h2>
            <p>Server uptime: %s</p>
            <p>Version: %s</p>
            <p>Total requests: %d</p>
            <p>Cache hits: %d (%.1f%%)</p>
            <p>Cache misses: %d</p>
            <p>Total bytes served: %d</p>
            <p>Cache entries: %d</p>
            <p>Cache size: %.2f MB</p>
            <p>Cache max size: %.2f MB</p>
        </div>

        <div class="stats">
            <h2>Memory Management</h2>
            <div class="meter" style="height:20px; background-color:#eee; border-radius:3px; margin-bottom:10px;">
                <div class="meter-bar" style="width: %.2f%%; height:100%%; background-color:%s; border-radius:3px;"></div>
            </div>
            <p>Memory Pressure: %.2f%% %s</p>
            <p>Allocated: %.2f MB / %.2f MB</p>
            <p>System Memory: %.2f MB</p>
            <p>GC Cycles: %d</p>
            <p>Heap Objects: %d</p>
            <form method="post" action="/admin/cleanup-memory" style="margin-top:10px;">
                <button type="submit">Force Memory Cleanup</button>
            </form>
        </div>`,
		time.Since(s.startTime).Round(time.Second),
		s.version,
		stats.TotalRequests,
		stats.CacheHits,
		stats.HitRate*100,
		stats.CacheMisses,
		stats.BytesServed,
		cacheStats.Items,
		float64(cacheStats.CurrentSize)/(1024*1024),
		float64(cacheStats.MaxSize)/(1024*1024),
		memStats["memory_pressure"].(float64)*100,
		getColorForPressure(memStats["memory_pressure"].(float64)*100),
		memStats["memory_pressure"].(float64)*100,
		getStatusForPressure(memStats["memory_pressure"].(float64)*100),
		memStats["allocated_mb"].(float64),
		memStats["system_mb"].(float64),
		memStats["system_mb"].(float64),
		memStats["gc_cycles"].(int),
		memStats["heap_objects"].(int))

	for _, req := range stats.RecentRequests {
		// Format request information with package name and client IP
		packageName := req.PackageName
		if packageName == "" {
			packageName = "-"
		}

		bytesText := "-"
		if req.Bytes > 0 {
			bytesText = byteCountSI(req.Bytes)
		}

		html += fmt.Sprintf(`<tr>
			<td class="truncate" title="%s">%s</td>
			<td>%s</td>
			<td>%s</td>
			<td>%.2f</td>
			<td>%s</td>
			<td>%s</td>
		</tr>`,
			req.Path, req.Path,
			packageName,
			req.ClientIP,
			float64(req.Duration.Milliseconds()),
			req.Result,
			bytesText)
	}

	html += `
        </table>
    </body>
    </html>
    `

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Error writing admin dashboard HTML: %v", err)
	}
}

// adminClearCache handles cache clearing
func (s *Server) adminClearCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mutex.Lock()
	count := s.cache.Clear()
	s.mutex.Unlock()

	log.Printf("Admin action: Cleared %d cache entries", count)

	html := fmt.Sprintf(`
        <html>
        <body>
            <h1>Cache Cleared</h1>
            <p>Removed %d cache entries.</p>
            <p><a href="/admin">Return to Admin Dashboard</a></p>
        </body>
        </html>
    `, count)

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Error writing cache clear response: %v", err)
	}
}

// adminFlushExpired handles flushing expired cache entries
func (s *Server) adminFlushExpired(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mutex.Lock()
	count, err := s.cache.FlushExpired()
	s.mutex.Unlock()

	if err != nil {
		http.Error(w, "Failed to flush expired entries", http.StatusInternalServerError)
		log.Printf("Failed to flush expired cache entries: %v", err)
		return
	}
	log.Printf("Admin action: Flushed %d expired cache entries", count)

	html := fmt.Sprintf(`
        <html>
        <body>
            <h1>Expired Items Flushed</h1>
            <p>Removed %d expired cache entries.</p>
            <p><a href="/admin">Return to Admin Dashboard</a></p>
        </body>
        </html>
    `, count)

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Error writing flush expired response: %v", err)
	}
}

// adminGetStats returns cache statistics in JSON format
func (s *Server) adminGetStats(w http.ResponseWriter, r *http.Request) {
	s.mutex.Lock()
	stats := s.metrics.GetStatistics()
	cacheStats, err := s.cache.GetStats()
	s.mutex.Unlock()

	if err != nil {
		http.Error(w, "Failed to retrieve cache statistics", http.StatusInternalServerError)
		log.Printf("Failed to get cache statistics: %v", err)
		return
	}

	// Format uptime
	uptime := time.Since(s.startTime).Round(time.Second).String()

	// Create JSON response
	response := map[string]any{ // Changed from any to any
		"version": s.version,
		"uptime":  uptime,
		"requests": map[string]any{ // Changed from any to any
			"total":                stats.TotalRequests,
			"cache_hits":           stats.CacheHits,
			"cache_misses":         stats.CacheMisses,
			"hit_rate":             stats.HitRate * 100,
			"avg_response_time_ms": stats.AvgResponseTime,
			"bytes_served":         stats.BytesServed,
			"last_client_ip":       stats.LastClientIP, // Add this field
		},
		"cache": map[string]any{ // Changed from any to any
			"entries":        cacheStats.Items,
			"size_bytes":     cacheStats.CurrentSize,
			"max_size_bytes": cacheStats.MaxSize,
			"usage_percent":  float64(cacheStats.CurrentSize) / float64(cacheStats.MaxSize) * 100,
			"last_file_size": stats.LastFileSize, // Add this field
		},
	}

	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	if err := encoder.Encode(response); err != nil {
		log.Printf("Error encoding JSON response: %v", err)
		http.Error(w, "Error generating stats", http.StatusInternalServerError)
	}
}

// adminSearchCache function
func (s *Server) adminSearchCache(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Missing search query", http.StatusBadRequest)
		return
	}

	// Search by path with mutex protection
	s.mutex.Lock()
	pathResults, _ := s.cache.Search(query)
	s.mutex.Unlock()

	// Convert string results to CacheEntry objects for display
	entryResults := make([]CacheEntry, len(pathResults))
	for i, path := range pathResults {
		// Create a basic CacheEntry with just the path since that's what we have
		entryResults[i] = CacheEntry{
			Path:       path,
			Size:       0,           // We don't have size information
			LastAccess: time.Time{}, // We don't have access time information
		}
	}

	// Search by package name with mutex protection
	s.mutex.Lock()
	packageResults, _ := s.cache.SearchByPackageName(query)
	s.mutex.Unlock()

	// Create HTML output
	html := fmt.Sprintf(`
    <!DOCTYPE html>
    <html>
    <head>
        <title>Cache Search Results</title>
        <style>
            body { font-family: Arial, sans-serif; margin: 20px; }
            h1, h2 { color: #333; }
            table { border-collapse: collapse; width: 100%%; }
            th, td { border: 1px solid #ddd; padding: 8px; text-align: left; }
            th { background: #f2f2f2; }
            tr:nth-child(even) { background: #f9f9f9; }
            .not-cached { color: #cc0000; }
            button { background: #0066cc; color: white; border: none; padding: 5px 10px; cursor: pointer; }
        </style>
    </head>
    <body>
        <h1>Search Results for "%s"</h1>
        <p><a href="/admin">Return to Admin Dashboard</a></p>

        <h2>Package Search Results</h2>
        <p>Found %d matching packages</p>
        <table>
            <tr>
                <th>Package Name</th>
                <th>Version</th>
                <th>Path</th>
                <th>Size</th>
                <th>Cached</th>
                <th>Action</th>
            </tr>
    `, query, len(packageResults))

	// Add package results rows
	for _, result := range packageResults {
		cacheStatus := "Yes"
		cacheClass := ""
		cacheAction := ""

		if !result.IsCached {
			cacheStatus = "No"
			cacheClass = "not-cached"
			cacheAction = fmt.Sprintf(`
                <form method="post" action="/admin/cache">
                    <input type="hidden" name="path" value="%s">
                    <button type="submit">Cache Now</button>
                </form>
            `, result.Path)
		}

		html += fmt.Sprintf(`
            <tr>
                <td>%s</td>
                <td>%s</td>
                <td>%s</td>
                <td>%d bytes</td>
                <td class="%s">%s</td>
                <td>%s</td>
            </tr>
        `, result.PackageName, result.Version, result.Path, result.Size, cacheClass, cacheStatus, cacheAction)
	}

	html += `
        </table>

        <h2>File Path Results</h2>
        <table>
            <tr>
                <th>Path</th>
                <th>Size</th>
                <th>Last Access</th>
            </tr>
    `

	// Add path-based results (existing code)
	for _, entry := range entryResults {
		html += fmt.Sprintf(`
			<tr>
				<td>%s</td>
				<td>%d bytes</td>
				<td>%s</td>
			</tr>
		`, entry.Path, entry.Size, entry.LastAccess.Format("2006-01-02 15:04:05"))
	}

	html += `
        </table>
    </body>
    </html>
    `

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Error writing search results: %v", err)
	}
}

// adminCachePackage handles package caching requests
func (s *Server) adminCachePackage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := r.FormValue("path")
	if path == "" {
		http.Error(w, "Missing path parameter", http.StatusBadRequest)
		return
	}

	// Start package download in background with mutex protection
	go func() {
		log.Printf("Admin-triggered package download: %s", path)
		s.mutex.Lock()
		_, err := s.backend.Fetch("/" + path)
		s.mutex.Unlock()

		if err != nil {
			log.Printf("Error caching package %s: %v", path, err)
		} else {
			log.Printf("Successfully cached package %s", path)
		}
	}()
}

// adminCleanupPrefetcher handles force-cleaning of the prefetcher
func (s *Server) adminCleanupPrefetcher(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mutex.Lock()
	count := s.backend.ForceCleanupPrefetcher()
	s.mutex.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"message": fmt.Sprintf("Cleaned up %d stale prefetch operations", count),
		"count":   count,
	}); err != nil {
		log.Printf("Error encoding JSON response: %v", err)
	}
}

// adminForceMemoryCleanup handles force memory cleanup
func (s *Server) adminForceMemoryCleanup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Force memory cleanup
	runtime.GC()

	// Call memory pressure handler with 100% to force all cleanup actions
	s.mutex.Lock()
	s.handleHighMemoryPressure(100)
	s.mutex.Unlock()
}

// Add these helper functions at the end of the file
// getColorForPressure returns a color code based on memory pressure
func getColorForPressure(pressure float64) string {
	if pressure > 90 {
		return "#cc0000" // red
	} else if pressure > 75 {
		return "#ff9900" // orange
	} else if pressure > 50 {
		return "#ffcc00" // yellow
	}
	return "#33cc33" // green
}

// getStatusForPressure returns a status description based on memory pressure
func getStatusForPressure(pressure float64) string {
	if pressure > 90 {
		return "(Critical)"
	} else if pressure > 75 {
		return "(High)"
	} else if pressure > 50 {
		return "(Moderate)"
	}
	return "(Normal)"
}

// HandleAdminAuth wraps a handler function with admin authentication
// Exported for testing
func (s *Server) HandleAdminAuth(handler http.HandlerFunc) http.HandlerFunc {
	return s.handleAdminAuth(handler)
}
