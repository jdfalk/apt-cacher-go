package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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
	stats := s.metrics.GetStatistics()
	cacheStats, err := s.cache.GetStats()
	if err != nil {
		http.Error(w, "Failed to retrieve cache statistics", http.StatusInternalServerError)
		log.Printf("Failed to get cache statistics: %v", err)
		return
	}

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

        <div class="actions">
            <h2>Actions</h2>
            <form method="post" action="/admin/clearcache" style="display:inline;">
                <button type="submit">Clear Cache</button>
            </form>
            <form method="post" action="/admin/flushexpired" style="display:inline;">
                <button type="submit">Flush Expired Items</button>
            </form>
        </div>

        <div>
            <h2>Search Cache</h2>
            <form method="get" action="/admin/search">
                <input type="text" name="q" placeholder="Package name or pattern">
                <button type="submit">Search</button>
            </form>
        </div>

        <h2>Recent Requests</h2>
        <table>
            <tr><th>Path</th><th>Time (ms)</th><th>Result</th></tr>
    `,
		time.Since(s.startTime).Round(time.Second),
		s.version,
		stats.TotalRequests,
		stats.CacheHits,
		stats.HitRate*100,
		stats.CacheMisses,
		stats.BytesServed,
		cacheStats.Items, // Changed from ItemCount to Items
		float64(cacheStats.CurrentSize)/(1024*1024),
		float64(cacheStats.MaxSize)/(1024*1024))

	for _, req := range stats.RecentRequests {
		html += fmt.Sprintf("<tr><td>%s</td><td>%.2f</td><td>%s</td></tr>",
			req.Path, float64(req.Duration.Milliseconds()), req.Result)
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

	count := s.cache.Clear()
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

	count, err := s.cache.FlushExpired()
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
	stats := s.metrics.GetStatistics()
	cacheStats, err := s.cache.GetStats()
	if err != nil {
		http.Error(w, "Failed to retrieve cache statistics", http.StatusInternalServerError)
		log.Printf("Failed to get cache statistics: %v", err)
		return
	}

	// Format uptime
	uptime := time.Since(s.startTime).Round(time.Second).String()

	// Create JSON response
	response := map[string]any{ // Changed from interface{} to any
		"version": s.version,
		"uptime":  uptime,
		"requests": map[string]any{ // Changed from interface{} to any
			"total":                stats.TotalRequests,
			"cache_hits":           stats.CacheHits,
			"cache_misses":         stats.CacheMisses,
			"hit_rate":             stats.HitRate * 100,
			"avg_response_time_ms": stats.AvgResponseTime,
			"bytes_served":         stats.BytesServed,
			"last_client_ip":       stats.LastClientIP, // Add this field
		},
		"cache": map[string]any{ // Changed from interface{} to any
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

// adminSearchCache searches the cache for packages matching the query
func (s *Server) adminSearchCache(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Missing search query", http.StatusBadRequest)
		return
	}

	results, err := s.cache.Search(query)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to search cache: %v", err), http.StatusInternalServerError)
		log.Printf("Failed to search cache: %v", err)
		return
	}

	// Convert the results to a usable format based on what Search actually returns
	entries := make([]CacheEntry, len(results))
	for i, res := range results {
		// This structure depends on what cache.Search actually returns
		// For this fix, assuming it returns path strings, we create minimal entries
		entries[i] = CacheEntry{
			Path:       res,
			Size:       0, // We don't have this information without additional calls
			LastAccess: time.Time{},
			Expires:    time.Time{},
		}
	}

	html := fmt.Sprintf(`
    <!DOCTYPE html>
    <html>
    <head>
        <title>Cache Search Results</title>
        <style>
            body { font-family: Arial, sans-serif; margin: 20px; }
            h1 { color: #333; }
            table { border-collapse: collapse; width: 100%%; }
            th, td { border: 1px solid #ddd; padding: 8px; text-align: left; }
            th { background: #f2f2f2; }
            tr:nth-child(even) { background: #f9f9f9; }
        </style>
    </head>
    <body>
        <h1>Search Results for "%s"</h1>
        <p>Found %d matches</p>
        <p><a href="/admin">Return to Admin Dashboard</a></p>

        <table>
            <tr>
                <th>Path</th>
                <th>Size</th>
                <th>Last Access</th>
                <th>Expiration</th>
            </tr>
    `, query, len(entries))

	// Display the search results with the information we have
	for _, entry := range entries {
		html += fmt.Sprintf(`
            <tr>
                <td>%s</td>
                <td>%d bytes</td>
                <td>%s</td>
                <td>%s</td>
            </tr>
        `, entry.Path,
			entry.Size,
			entry.LastAccess.Format("2006-01-02 15:04:05"),
			entry.Expires.Format("2006-01-02 15:04:05"))
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
