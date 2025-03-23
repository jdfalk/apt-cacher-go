package server

import (
	"encoding/json"
	"fmt"
	"html/template"
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

// adminDashboard serves the admin dashboard
func (s *Server) adminDashboard(w http.ResponseWriter, r *http.Request) {
	// Gather stats
	stats := s.metrics.GetStatistics()
	cacheStats, err := s.cache.GetStats()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting cache stats: %v", err), http.StatusInternalServerError)
		return
	}

    // Convert metrics.TopPackage to server.TopPackage
    metricPackages := s.metrics.GetTopPackages(10)
    topPackages := make([]TopPackage, len(metricPackages))
    for i, pkg := range metricPackages {
        topPackages[i] = TopPackage{
            URL:        pkg.URL,
            Count:      pkg.Count,
            LastAccess: pkg.LastAccess,
            Size:       pkg.Size,
        }
    }

    // Convert metrics.TopClient to server.TopClient
    metricClients := s.metrics.GetTopClients(10)
    topClients := make([]TopClient, len(metricClients))
    for i, client := range metricClients {
        topClients[i] = TopClient{
            IP:        client.IP,
            Requests:  client.Requests,
            BytesSent: client.BytesSent,
        }
    }

    adminStats := AdminStats{
        CacheSize:       cacheStats.CurrentSize,
        CacheMaxSize:    cacheStats.MaxSize,
        CachePercentage: float64(cacheStats.CurrentSize) / float64(cacheStats.MaxSize) * 100,
        CacheItems:      cacheStats.Items,
        BytesServed:     stats.BytesServed,
        RequestsTotal:   stats.TotalRequests,
        RequestsHit:     stats.CacheHits,
        RequestsMiss:    stats.CacheMisses,
        HitRate:         stats.HitRate * 100, // Convert to percentage
        UpSince:         s.startTime,
        PackagesTop:     topPackages,
        ClientsTop:      topClients,
    }

	// Serve the dashboard template
	tmpl, err := template.ParseFiles("templates/admin/dashboard.html")
	if err != nil {
		// Fall back to inline template if file not found
		tmpl, err = template.New("dashboard").Parse(adminDashboardTemplate)
		if err != nil {
			http.Error(w, "Template error", http.StatusInternalServerError)
			return
		}
	}

	err = tmpl.Execute(w, adminStats)
	if err != nil {
		http.Error(w, fmt.Sprintf("Template execution error: %v", err), http.StatusInternalServerError)
	}
}

// adminClearCache handles the cache clearing action
func (s *Server) adminClearCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := s.cache.Clear()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to clear cache: %v", err), http.StatusInternalServerError)
		return
	}

	// Return success
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "Cache cleared successfully",
	})
}

// adminFlushExpired handles flushing expired cache items
func (s *Server) adminFlushExpired(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	removed, err := s.cache.FlushExpired()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to flush expired items: %v", err), http.StatusInternalServerError)
		return
	}

	// Return success
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":       "success",
		"message":      fmt.Sprintf("Flushed %d expired items", removed),
		"itemsRemoved": removed,
	})
}

// adminGetStats returns JSON statistics
func (s *Server) adminGetStats(w http.ResponseWriter, r *http.Request) {
	// Gather stats
	stats := s.metrics.GetStatistics()
	cacheStats, err := s.cache.GetStats()
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting cache stats: %v", err), http.StatusInternalServerError)
		return
	}

	// Format the stats as JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"cache":    cacheStats,
		"requests": stats,
		"uptime":   time.Since(s.startTime).String(),
	})
}

// adminSearchCache searches the cache
func (s *Server) adminSearchCache(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Missing query parameter", http.StatusBadRequest)
		return
	}

	results, err := s.cache.Search(query)
	if err != nil {
		http.Error(w, fmt.Sprintf("Search error: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// Admin dashboard HTML template
const adminDashboardTemplate = `
<!DOCTYPE html>
<html>
<head>
    <title>apt-cacher-go Admin Dashboard</title>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <style>
        body { font-family: Arial, sans-serif; margin: 0; padding: 20px; color: #333; }
        h1, h2 { color: #2c3e50; }
        .container { max-width: 1200px; margin: 0 auto; }
        .stats-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 20px; margin-bottom: 30px; }
        .stat-card { background: #f8f9fa; border-radius: 8px; padding: 15px; box-shadow: 0 2px 5px rgba(0,0,0,0.1); }
        .stat-card h3 { margin-top: 0; color: #7f8c8d; font-size: 14px; }
        .stat-card p { margin-bottom: 0; font-size: 24px; font-weight: bold; color: #2c3e50; }
        .progress-bar { height: 10px; background: #ecf0f1; border-radius: 5px; overflow: hidden; margin-top: 5px; }
        .progress-bar-fill { height: 100%; background: #3498db; width: 0; }
        table { width: 100%; border-collapse: collapse; margin-bottom: 30px; }
        th, td { text-align: left; padding: 12px; border-bottom: 1px solid #ddd; }
        th { background-color: #f2f2f2; }
        tr:hover { background-color: #f5f5f5; }
        .btn { display: inline-block; background: #3498db; color: white; padding: 8px 16px; border-radius: 4px; text-decoration: none; border: none; cursor: pointer; }
        .btn-danger { background: #e74c3c; }
        .btn-warning { background: #f39c12; }
        .action-row { margin-bottom: 30px; }
        .action-row button { margin-right: 10px; }
        .footer { margin-top: 50px; text-align: center; font-size: 14px; color: #7f8c8d; }
    </style>
</head>
<body>
    <div class="container">
        <h1>apt-cacher-go Admin Dashboard</h1>

        <div class="stats-grid">
            <div class="stat-card">
                <h3>Cache Usage</h3>
                <p>{{printf "%.1f" .CachePercentage}}%</p>
                <div class="progress-bar">
                    <div class="progress-bar-fill" style="width: {{.CachePercentage}}%;"></div>
                </div>
                <small>{{formatBytes .CacheSize}} / {{formatBytes .CacheMaxSize}}</small>
            </div>

            <div class="stat-card">
                <h3>Cache Items</h3>
                <p>{{.CacheItems}}</p>
            </div>

            <div class="stat-card">
                <h3>Total Requests</h3>
                <p>{{.RequestsTotal}}</p>
            </div>

            <div class="stat-card">
                <h3>Hit Rate</h3>
                <p>{{printf "%.1f" .HitRate}}%</p>
            </div>

            <div class="stat-card">
                <h3>Bytes Served</h3>
                <p>{{formatBytes .BytesServed}}</p>
            </div>

            <div class="stat-card">
                <h3>Uptime</h3>
                <p>{{formatDuration .UpSince}}</p>
            </div>
        </div>

        <div class="action-row">
            <button class="btn btn-danger" onclick="clearCache()">Clear Cache</button>
            <button class="btn btn-warning" onclick="flushExpired()">Flush Expired</button>
        </div>

        <h2>Top Packages</h2>
        <table>
            <tr>
                <th>Package</th>
                <th>Requests</th>
                <th>Size</th>
                <th>Last Access</th>
            </tr>
            {{range .PackagesTop}}
            <tr>
                <td>{{.URL}}</td>
                <td>{{.Count}}</td>
                <td>{{formatBytes .Size}}</td>
                <td>{{formatTime .LastAccess}}</td>
            </tr>
            {{end}}
        </table>

        <h2>Top Clients</h2>
        <table>
            <tr>
                <th>Client IP</th>
                <th>Requests</th>
                <th>Data Transferred</th>
            </tr>
            {{range .ClientsTop}}
            <tr>
                <td>{{.IP}}</td>
                <td>{{.Requests}}</td>
                <td>{{formatBytes .BytesSent}}</td>
            </tr>
            {{end}}
        </table>

        <div class="footer">
            <p>apt-cacher-go v1.0.0</p>
        </div>
    </div>

    <script>
        function clearCache() {
            if (confirm('Are you sure you want to clear the entire cache?')) {
                fetch('/admin/clearcache', { method: 'POST' })
                    .then(response => response.json())
                    .then(data => {
                        alert(data.message);
                        location.reload();
                    })
                    .catch(error => {
                        alert('Error: ' + error);
                    });
            }
        }

        function flushExpired() {
            fetch('/admin/flushexpired', { method: 'POST' })
                .then(response => response.json())
                .then(data => {
                    alert(data.message);
                    location.reload();
                })
                .catch(error => {
                    alert('Error: ' + error);
                });
        }

        // Initialize progress bars on load
        document.addEventListener('DOMContentLoaded', function() {
            const fills = document.querySelectorAll('.progress-bar-fill');
            fills.forEach(fill => {
                const width = fill.style.width;
                fill.style.width = '0';
                setTimeout(() => {
                    fill.style.transition = 'width 1s ease-in-out';
                    fill.style.width = width;
                }, 100);
            });
        });
    </script>
</body>
</html>
`
