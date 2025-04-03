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
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	LastAccess time.Time `json:"lastAccess"`
	Expires    time.Time `json:"expires,omitempty"`
	Package    string    `json:"package,omitempty"` // Added this field
}

// adminDashboard serves the admin dashboard
func (s *Server) adminDashboard(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Log.Debug.TraceHTTPRequests {
		log.Printf("[HTTP TRACE] Admin dashboard requested from %s", r.RemoteAddr)
	}

	s.mutex.Lock()
	stats := s.metrics.GetStatistics()
	cacheStats := s.cache.GetStats()
	s.mutex.Unlock()

	// Get memory statistics
	s.mutex.Lock()
	memStats := s.memoryMonitor.GetMemoryUsage()
	s.mutex.Unlock()

	html := fmt.Sprintf(`
    <!DOCTYPE html>
    <html>
    <head>
        <title>apt-cacher-go Admin</title>
        <meta charset="UTF-8">
        <meta name="viewport" content="width=device-width, initial-scale=1.0">
        <style>
            body {
                font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
                margin: 20px;
                line-height: 1.6;
                color: #333;
            }
            h1 {
                color: #2c3e50;
                border-bottom: 1px solid #eee;
                padding-bottom: 10px;
            }
            h2 {
                color: #3498db;
                margin-top: 20px;
            }
            .stats {
                background: #f8f9fa;
                padding: 20px;
                border-radius: 8px;
                margin-bottom: 20px;
                box-shadow: 0 1px 3px rgba(0,0,0,0.1);
            }
            .actions {
                margin: 20px 0;
                display: flex;
                flex-wrap: wrap;
                gap: 10px;
            }
            button {
                background: #3498db;
                color: white;
                border: none;
                padding: 10px 15px;
                border-radius: 4px;
                cursor: pointer;
                transition: background 0.2s;
            }
            button:hover {
                background: #2980b9;
            }
            table {
                border-collapse: collapse;
                width: 100%%;
                margin: 20px 0;
                box-shadow: 0 1px 3px rgba(0,0,0,0.1);
            }
            th, td {
                border: 1px solid #ddd;
                padding: 12px 15px;
                text-align: left;
            }
            th {
                background: #f2f2f2;
                font-weight: bold;
            }
            tr:nth-child(even) {
                background: #f9f9f9;
            }
            tr:hover {
                background: #f1f1f1;
            }
            .truncate {
                max-width: 300px;
                overflow: hidden;
                text-overflow: ellipsis;
                white-space: nowrap;
            }
            .meter {
                height: 20px;
                background-color: #ecf0f1;
                border-radius: 4px;
                margin-bottom: 15px;
                overflow: hidden;
            }
            .meter-bar {
                height: 100%%;
                border-radius: 4px;
                transition: width 0.5s ease-in-out;
            }
            .container {
                max-width: 1200px;
                margin: 0 auto;
            }
        </style>
    </head>
    <body>
        <div class="container">
            <h1>apt-cacher-go Administration</h1>

            <div class="stats">
                <h2>Statistics</h2>
                <p>Server uptime: %s</p>
                <p>Version: %s</p>
                <p>Total requests: %d</p>
                <p>Cache hits: %d (%.1f%%)</p>
                <p>Cache misses: %d</p>
                <p>Total bytes served: %s</p>
                <p>Cache entries: %d</p>
                <p>Cache size: %.2f MB / %.2f MB (%.1f%%)</p>
            </div>

            <div class="stats">
                <h2>Memory Management</h2>
                <div class="meter">
                    <div class="meter-bar" style="width: %.2f%%; background-color:%s;"></div>
                </div>
                <p>Memory Pressure: %.2f%% %s</p>
                <p>Allocated: %.2f MB / %.2f MB</p>
                <p>System Memory: %.2f MB</p>
                <p>GC Cycles: %d</p>
                <p>Heap Objects: %d</p>
                <form method="post" action="/admin/cleanup-memory" style="margin-top:10px;">
                    <button type="submit">Force Memory Cleanup</button>
                </form>
            </div>

            <div class="actions">
                <form method="post" action="/admin/clear">
                    <button type="submit">Clear Cache</button>
                </form>
                <form method="post" action="/admin/flush">
                    <button type="submit">Flush Expired</button>
                </form>
                <form method="post" action="/admin/cleanup-prefetcher">
                    <button type="submit">Cleanup Prefetcher</button>
                </form>
                <a href="/admin/search" style="text-decoration: none;">
                    <button type="button">Search Cache</button>
                </a>
            </div>

            <h2>Recent Requests</h2>
            <table>
                <thead>
                    <tr>
                        <th>Path</th>
                        <th>Package</th>
                        <th>Client IP</th>
                        <th>Duration (ms)</th>
                        <th>Result</th>
                        <th>Size</th>
                    </tr>
                </thead>
                <tbody>`,
		time.Since(s.startTime).Round(time.Second),
		s.version,
		stats.TotalRequests,
		stats.CacheHits,
		stats.HitRate*100,
		stats.CacheMisses,
		byteCountSI(stats.BytesServed),
		cacheStats.Items,
		float64(cacheStats.CurrentSize)/(1024*1024),
		float64(cacheStats.MaxSize)/(1024*1024),
		float64(cacheStats.CurrentSize)/float64(cacheStats.MaxSize)*100,
		memStats["memory_pressure"].(float64)*100,
		getColorForPressure(memStats["memory_pressure"].(float64)*100),
		memStats["memory_pressure"].(float64)*100,
		getStatusForPressure(memStats["memory_pressure"].(float64)*100),
		memStats["allocated_mb"].(float64),
		memStats["system_mb"].(float64),
		memStats["system_mb"].(float64),
		memStats["gc_cycles"].(int),
		memStats["heap_objects"].(int))

	// Add table rows for recent requests
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

		html += fmt.Sprintf(`
                    <tr>
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

	// Close table and HTML structure
	html += `
                </tbody>
            </table>

            <div class="stats">
                <h2>System Information</h2>
                <p>Go Version: %s</p>
                <p>Operating System: %s</p>
                <p>CPU Architecture: %s</p>
                <p>Number of CPUs: %d</p>
                <p>Goroutines: %d</p>
            </div>
        </div>
    </body>
    </html>
    `

	html = fmt.Sprintf(html,
		runtime.Version(),
		runtime.GOOS,
		runtime.GOARCH,
		runtime.NumCPU(),
		runtime.NumGoroutine())

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Error writing admin dashboard HTML: %v", err)
	}
}

// adminClearCache handles cache clearing
func (s *Server) adminClearCache(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Log.Debug.TraceHTTPRequests {
		log.Printf("[HTTP TRACE] Admin cache clear requested from %s", r.RemoteAddr)
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if cacheClearer, ok := s.cache.(interface{ Clear() error }); ok {
		err := cacheClearer.Clear()
		if err != nil {
			http.Error(w, "Failed to clear cache", http.StatusInternalServerError)
			log.Printf("Failed to clear cache: %v", err)
			return
		}
	} else {
		http.Error(w, "Cache does not support clearing", http.StatusInternalServerError)
		return
	}

	log.Printf("Admin action: Cleared cache")

	html := `
        <!DOCTYPE html>
        <html>
        <head>
            <title>Cache Cleared</title>
            <meta charset="UTF-8">
            <meta name="viewport" content="width=device-width, initial-scale=1.0">
            <style>
                body {
                    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
                    margin: 20px;
                    line-height: 1.6;
                    color: #333;
                    max-width: 800px;
                    margin: 0 auto;
                    padding: 20px;
                }
                h1 { color: #2c3e50; }
                a {
                    color: #3498db;
                    text-decoration: none;
                }
                a:hover { text-decoration: underline; }
                .box {
                    background: #f8f9fa;
                    border-radius: 8px;
                    padding: 20px;
                    margin: 20px 0;
                    box-shadow: 0 1px 3px rgba(0,0,0,0.1);
                }
            </style>
        </head>
        <body>
            <div class="box">
                <h1>Cache Cleared</h1>
                <p>Cache has been successfully cleared.</p>
                <p><a href="/admin">Return to Admin Dashboard</a></p>
            </div>
        </body>
        </html>
    `

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Error writing cache clear response: %v", err)
	}
}

// adminFlushExpired handles flushing expired cache entries
func (s *Server) adminFlushExpired(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Log.Debug.TraceHTTPRequests {
		log.Printf("[HTTP TRACE] Admin flush expired requested from %s", r.RemoteAddr)
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var flushed int
	if flusher, ok := s.cache.(interface{ FlushExpired() (int, error) }); ok {
		var err error
		flushed, err = flusher.FlushExpired()
		if err != nil {
			http.Error(w, "Failed to flush expired items", http.StatusInternalServerError)
			log.Printf("Failed to flush expired items: %v", err)
			return
		}
	} else {
		http.Error(w, "Cache does not support flushing expired items", http.StatusInternalServerError)
		return
	}

	log.Printf("Admin action: Flushed %d expired items", flushed)

	html := fmt.Sprintf(`
        <!DOCTYPE html>
        <html>
        <head>
            <title>Expired Items Flushed</title>
            <meta charset="UTF-8">
            <meta name="viewport" content="width=device-width, initial-scale=1.0">
            <style>
                body {
                    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
                    margin: 20px;
                    line-height: 1.6;
                    color: #333;
                    max-width: 800px;
                    margin: 0 auto;
                    padding: 20px;
                }
                h1 { color: #2c3e50; }
                a {
                    color: #3498db;
                    text-decoration: none;
                }
                a:hover { text-decoration: underline; }
                .box {
                    background: #f8f9fa;
                    border-radius: 8px;
                    padding: 20px;
                    margin: 20px 0;
                    box-shadow: 0 1px 3px rgba(0,0,0,0.1);
                }
            </style>
        </head>
        <body>
            <div class="box">
                <h1>Expired Items Flushed</h1>
                <p>%d expired items have been removed from the cache.</p>
                <p><a href="/admin">Return to Admin Dashboard</a></p>
            </div>
        </body>
        </html>
    `, flushed)

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Error writing flush expired response: %v", err)
	}
}

// adminGetStats returns cache statistics in JSON format
func (s *Server) adminGetStats(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Log.Debug.TraceHTTPRequests {
		log.Printf("[HTTP TRACE] Admin stats requested from %s", r.RemoteAddr)
	}

	s.mutex.Lock()
	stats := s.metrics.GetStatistics()
	cacheStats := s.cache.GetStats() // No error to handle since we updated the interface
	s.mutex.Unlock()

	// Format uptime
	uptime := time.Since(s.startTime).Round(time.Second).String()

	// Create JSON response
	response := map[string]any{
		"version": s.version,
		"uptime":  uptime,
		"requests": map[string]any{
			"total":                stats.TotalRequests,
			"cache_hits":           stats.CacheHits,
			"cache_misses":         stats.CacheMisses,
			"hit_rate":             stats.HitRate * 100,
			"avg_response_time_ms": stats.AvgResponseTime,
			"bytes_served":         stats.BytesServed,
			"last_client_ip":       stats.LastClientIP,
		},
		"cache": map[string]any{
			"entries":        cacheStats.Items,
			"size_bytes":     cacheStats.CurrentSize,
			"max_size_bytes": cacheStats.MaxSize,
			"usage_percent":  float64(cacheStats.CurrentSize) / float64(cacheStats.MaxSize) * 100,
			"last_file_size": stats.LastFileSize,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	if err := encoder.Encode(response); err != nil {
		log.Printf("Error encoding stats JSON: %v", err)
		http.Error(w, "Error generating statistics", http.StatusInternalServerError)
	}
}

// adminSearchCache function
func (s *Server) adminSearchCache(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Log.Debug.TraceHTTPRequests {
		log.Printf("[HTTP TRACE] Admin search requested from %s", r.RemoteAddr)
	}

	query := r.URL.Query().Get("q")

	// If no query, show search form
	if query == "" {
		html := `
        <!DOCTYPE html>
        <html>
        <head>
            <title>Search Cache</title>
            <meta charset="UTF-8">
            <meta name="viewport" content="width=device-width, initial-scale=1.0">
            <style>
                body {
                    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
                    margin: 20px;
                    line-height: 1.6;
                    color: #333;
                    max-width: 800px;
                    margin: 0 auto;
                    padding: 20px;
                }
                h1 { color: #2c3e50; }
                input[type="text"] {
                    width: 100%;
                    padding: 10px;
                    margin: 10px 0;
                    border: 1px solid #ddd;
                    border-radius: 4px;
                    font-size: 16px;
                }
                button {
                    background: #3498db;
                    color: white;
                    border: none;
                    padding: 10px 20px;
                    border-radius: 4px;
                    cursor: pointer;
                    font-size: 16px;
                }
                button:hover {
                    background: #2980b9;
                }
                .box {
                    background: #f8f9fa;
                    border-radius: 8px;
                    padding: 20px;
                    margin: 20px 0;
                    box-shadow: 0 1px 3px rgba(0,0,0,0.1);
                }
                a {
                    color: #3498db;
                    text-decoration: none;
                }
                a:hover { text-decoration: underline; }
            </style>
        </head>
        <body>
            <div class="box">
                <h1>Search Cache</h1>
                <form action="/admin/search" method="GET">
                    <input type="text" name="q" placeholder="Enter package name or path...">
                    <button type="submit">Search</button>
                </form>
                <p><a href="/admin">Return to Admin Dashboard</a></p>
            </div>
        </body>
        </html>
        `
		w.Header().Set("Content-Type", "text/html")
		if _, err := w.Write([]byte(html)); err != nil {
			log.Printf("Error writing search form: %v", err)
		}
		return
	}

	// Search by path with mutex protection
	s.mutex.Lock()
	pathResults, err := s.cache.Search(query)
	if err != nil {
		s.mutex.Unlock()
		log.Printf("Error searching cache: %v", err)
		http.Error(w, "Error searching cache", http.StatusInternalServerError)
		return
	}
	s.mutex.Unlock()

	// Convert string results to CacheEntry objects for display
	entryResults := make([]CacheEntry, len(pathResults))
	for i, path := range pathResults {
		// Get metadata for each path
		s.mutex.Lock()
		lastModified := s.cache.GetLastModified(path)
		s.mutex.Unlock()

		entryResults[i] = CacheEntry{
			Path:       path,
			LastAccess: lastModified,
		}
	}

	// Search by package name with mutex protection
	s.mutex.Lock()
	packageResults, err := s.cache.SearchByPackageName(query)
	if err != nil {
		s.mutex.Unlock()
		log.Printf("Error searching packages: %v", err)
		http.Error(w, "Error searching packages", http.StatusInternalServerError)
		return
	}
	s.mutex.Unlock()

	// Create combined results array to use packageResults
	combinedResults := make([]CacheEntry, len(entryResults)+len(packageResults))
	copy(combinedResults, entryResults)

	// Convert package results to CacheEntry objects
	for i, pkgResult := range packageResults {
		combinedResults[len(entryResults)+i] = CacheEntry{
			Path:       pkgResult.Path,
			Size:       pkgResult.Size,
			LastAccess: pkgResult.LastAccess,
			Package:    pkgResult.PackageName,
		}
	}

	// Determine if we should return JSON or HTML based on Accept header
	if r.Header.Get("Accept") == "application/json" {
		// Return JSON response
		w.Header().Set("Content-Type", "application/json")
		encoder := json.NewEncoder(w)
		if err := encoder.Encode(combinedResults); err != nil {
			log.Printf("Error encoding search results JSON: %v", err)
		}
	} else {
		// Return HTML response
		html := fmt.Sprintf(`
        <!DOCTYPE html>
        <html>
        <head>
            <title>Search Results: %s</title>
            <meta charset="UTF-8">
            <meta name="viewport" content="width=device-width, initial-scale=1.0">
            <style>
                body {
                    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
                    margin: 20px;
                    line-height: 1.6;
                    color: #333;
                    max-width: 1200px;
                    margin: 0 auto;
                    padding: 20px;
                }
                h1 { color: #2c3e50; }
                input[type="text"] {
                    width: 50%%;
                    padding: 10px;
                    margin: 10px 0;
                    border: 1px solid #ddd;
                    border-radius: 4px;
                    font-size: 16px;
                }
                button {
                    background: #3498db;
                    color: white;
                    border: none;
                    padding: 10px 20px;
                    border-radius: 4px;
                    cursor: pointer;
                    font-size: 16px;
                    margin-left: 10px;
                }
                button:hover {
                    background: #2980b9;
                }
                table {
                    width: 100%%;
                    border-collapse: collapse;
                    margin: 20px 0;
                }
                th, td {
                    border: 1px solid #ddd;
                    padding: 12px;
                    text-align: left;
                }
                th {
                    background-color: #f2f2f2;
                    font-weight: bold;
                }
                tr:nth-child(even) {
                    background-color: #f9f9f9;
                }
                .truncate {
                    max-width: 300px;
                    overflow: hidden;
                    text-overflow: ellipsis;
                    white-space: nowrap;
                }
                a {
                    color: #3498db;
                    text-decoration: none;
                }
                a:hover { text-decoration: underline; }
            </style>
        </head>
        <body>
            <h1>Search Results for "%s"</h1>

            <form action="/admin/search" method="GET">
                <input type="text" name="q" value="%s" placeholder="Enter package name or path...">
                <button type="submit">Search</button>
            </form>

            <p>Found %d results</p>

            <table>
                <thead>
                    <tr>
                        <th>Path</th>
                        <th>Package</th>
                        <th>Size</th>
                        <th>Last Access</th>
                    </tr>
                </thead>
                <tbody>`,
			query, query, query, len(combinedResults))

		for _, entry := range combinedResults {
			packageName := entry.Package
			if packageName == "" {
				packageName = "-"
			}

			sizeText := "-"
			if entry.Size > 0 {
				sizeText = byteCountSI(entry.Size)
			}

			lastAccessText := "-"
			if !entry.LastAccess.IsZero() {
				lastAccessText = entry.LastAccess.Format("2006-01-02 15:04:05")
			}

			html += fmt.Sprintf(`
                <tr>
                    <td class="truncate" title="%s">%s</td>
                    <td>%s</td>
                    <td>%s</td>
                    <td>%s</td>
                </tr>`,
				entry.Path, entry.Path,
				packageName,
				sizeText,
				lastAccessText)
		}

		html += `
                </tbody>
            </table>

            <p><a href="/admin">Return to Admin Dashboard</a></p>
        </body>
        </html>
        `

		w.Header().Set("Content-Type", "text/html")
		if _, err := w.Write([]byte(html)); err != nil {
			log.Printf("Error writing search results HTML: %v", err)
		}
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
		http.Error(w, "Path is required", http.StatusBadRequest)
		return
	}

	// Start package download in background with mutex protection
	go func() {
		// Use the backend to fetch the package
		s.mutex.Lock()
		defer s.mutex.Unlock()

		data, err := s.backend.Fetch(path)
		if err != nil {
			log.Printf("Error caching package %s: %v", path, err)
			return
		}

		log.Printf("Successfully cached package %s (%d bytes)", path, len(data))
	}()

	html := fmt.Sprintf(`
    <!DOCTYPE html>
    <html>
    <head>
        <title>Package Caching</title>
        <meta charset="UTF-8">
        <meta name="viewport" content="width=device-width, initial-scale=1.0">
        <style>
            body {
                font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
                margin: 20px;
                line-height: 1.6;
                color: #333;
                max-width: 800px;
                margin: 0 auto;
                padding: 20px;
            }
            h1 { color: #2c3e50; }
            .box {
                background: #f8f9fa;
                border-radius: 8px;
                padding: 20px;
                margin: 20px 0;
                box-shadow: 0 1px 3px rgba(0,0,0,0.1);
            }
            a {
                color: #3498db;
                text-decoration: none;
            }
            a:hover { text-decoration: underline; }
        </style>
    </head>
    <body>
        <div class="box">
            <h1>Package Caching Started</h1>
            <p>Caching of package %s has been initiated in the background.</p>
            <p><a href="/admin">Return to Admin Dashboard</a></p>
        </div>
    </body>
    </html>
    `, path)

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Error writing package cache response: %v", err)
	}
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

	log.Printf("Admin action: Cleaned up %d prefetch operations", count)

	html := fmt.Sprintf(`
    <!DOCTYPE html>
    <html>
    <head>
        <title>Prefetcher Cleanup</title>
        <meta charset="UTF-8">
        <meta name="viewport" content="width=device-width, initial-scale=1.0">
        <style>
            body {
                font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
                margin: 20px;
                line-height: 1.6;
                color: #333;
                max-width: 800px;
                margin: 0 auto;
                padding: 20px;
            }
            h1 { color: #2c3e50; }
            .box {
                background: #f8f9fa;
                border-radius: 8px;
                padding: 20px;
                margin: 20px 0;
                box-shadow: 0 1px 3px rgba(0,0,0,0.1);
            }
            a {
                color: #3498db;
                text-decoration: none;
            }
            a:hover { text-decoration: underline; }
        </style>
    </head>
    <body>
        <div class="box">
            <h1>Prefetcher Cleanup Complete</h1>
            <p>Cleaned up %d prefetch operations.</p>
            <p><a href="/admin">Return to Admin Dashboard</a></p>
        </div>
    </body>
    </html>
    `, count)

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Error writing prefetcher cleanup response: %v", err)
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
	s.handleHighMemoryPressure(1.0)

	log.Printf("Admin action: Forced memory cleanup")

	html := `
    <!DOCTYPE html>
    <html>
    <head>
        <title>Memory Cleanup</title>
        <meta charset="UTF-8">
        <meta name="viewport" content="width=device-width, initial-scale=1.0">
        <style>
            body {
                font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
                margin: 20px;
                line-height: 1.6;
                color: #333;
                max-width: 800px;
                margin: 0 auto;
                padding: 20px;
            }
            h1 { color: #2c3e50; }
            .box {
                background: #f8f9fa;
                border-radius: 8px;
                padding: 20px;
                margin: 20px 0;
                box-shadow: 0 1px 3px rgba(0,0,0,0.1);
            }
            a {
                color: #3498db;
                text-decoration: none;
            }
            a:hover { text-decoration: underline; }
        </style>
    </head>
    <body>
        <div class="box">
            <h1>Memory Cleanup Complete</h1>
            <p>Forced garbage collection and memory cleanup has been executed.</p>
            <p><a href="/admin">Return to Admin Dashboard</a></p>
        </div>
    </body>
    </html>
    `

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Error writing memory cleanup response: %v", err)
	}
}

// getColorForPressure returns a color code based on memory pressure
func getColorForPressure(pressure float64) string {
	if pressure < 60 {
		return "#2ecc71" // green
	} else if pressure < 80 {
		return "#f39c12" // orange
	} else {
		return "#e74c3c" // red
	}
}

// getStatusForPressure returns a status description based on memory pressure
func getStatusForPressure(pressure float64) string {
	if pressure < 60 {
		return "(Normal)"
	} else if pressure < 80 {
		return "(High)"
	} else {
		return "(Critical!)"
	}
}

// HandleAdminAuth wraps a handler function with admin authentication
// Exported for testing
func (s *Server) HandleAdminAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.cfg.AdminAuth {
			// Admin auth is disabled
			handler(w, r)
			return
		}

		// Extract credentials
		username, password, ok := r.BasicAuth()
		if !ok || !s.validateAdminAuth(username, password) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Admin Access"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Authentication successful
		handler(w, r)
	}
}

// validateAdminAuth checks if the provided credentials match the configured admin auth
func (s *Server) validateAdminAuth(username, password string) bool {
	return username == s.cfg.AdminUser && password == s.cfg.AdminPassword
}
