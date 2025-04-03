package server

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	// Use alias
	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/security"
)

// Server represents the apt-cacher HTTP server
type Server struct {
	cfg             *config.Config
	httpServer      *http.Server
	httpsServer     *http.Server
	adminServer     *http.Server
	cache           Cache
	backend         BackendManager
	metrics         MetricsCollector
	acl             *security.ACL
	mapper          PathMapper
	packageMapper   PackageMapper
	startTime       time.Time
	version         string
	logger          *log.Logger
	memoryMonitor   MemoryMonitorInterface
	mutex           sync.Mutex
	startOnce       sync.Once
	shutdownOnce    sync.Once
	shutdownCh      chan struct{}
	localKeyManager KeyManager
}

// New creates a new Server instance with the provided options
//
// This function initializes and configures a server instance with the specified options.
// It sets up the cache, mappers, backend manager, metrics collector, and other components
// needed to run the apt-cacher server.
//
// Parameters:
// - cfg: Configuration settings for the server
// - opts: Additional server options including dependencies and version info
//
// Returns:
// - A fully initialized Server instance
// - An error if initialization fails
func New(cfg *config.Config, opts ServerOptions) (*Server, error) {
	// Create cache or use provided
	var cache Cache
	if opts.Cache != nil {
		cache = opts.Cache
	} else {
		// Create default cache implementation
		// ...
	}

	// Create path mapper or use provided
	var pathMapper PathMapper
	if opts.PathMapper != nil {
		pathMapper = opts.PathMapper
	} else {
		// Create default path mapper
		// ...
	}

	// Create package mapper or use provided
	var packageMapper PackageMapper
	if opts.PackageMapper != nil {
		packageMapper = opts.PackageMapper
	} else {
		// Create default package mapper
		// ...
	}

	// Initialize key manager with debug options
	var localKeyManager KeyManager
	// ...

	// Create backend manager or use provided
	var backendManager BackendManager
	if opts.BackendManager != nil {
		backendManager = opts.BackendManager
	} else {
		// Create default backend manager
		// ...
	}

	// Create metrics collector or use provided
	var metricsCollector MetricsCollector
	if opts.MetricsCollector != nil {
		metricsCollector = opts.MetricsCollector
	} else {
		// Create default metrics collector
		// ...
	}

	// Create memory monitor or use provided
	var memoryMonitor MemoryMonitorInterface
	if opts.MemoryMonitor != nil {
		memoryMonitor = opts.MemoryMonitor
	} else {
		// Create default memory monitor
		// ...
	}

	// Create the server instance
	server := &Server{
		cfg:             cfg,
		cache:           cache,
		backend:         backendManager,
		mapper:          pathMapper,
		packageMapper:   packageMapper,
		metrics:         metricsCollector,
		version:         opts.Version,
		startTime:       time.Now(),
		shutdownCh:      make(chan struct{}),
		localKeyManager: localKeyManager,
		memoryMonitor:   memoryMonitor,
	}

	// Configure logger based on new log configuration
	if opts.Logger != nil {
		server.logger = log.New(opts.Logger, "", log.LstdFlags)
	} else {
		// Initialize default logger
		// ...
	}

	// Initialize ACL
	if len(cfg.AllowedIPs) > 0 {
		var err error
		server.acl, err = security.New(cfg.AllowedIPs)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize ACL: %w", err)
		}
	}

	// Configure HTTP handlers
	server.setupHTTPHandlers()

	// Start the memory monitor
	if server.memoryMonitor != nil {
		server.memoryMonitor.Start()
	}

	return server, nil
}

// setupHTTPHandlers creates and configures all HTTP handlers
//
// This method sets up all the HTTP routes for the main server and admin interfaces.
// It creates separate multiplexers for different server functions and configures
// security middleware as needed.
func (s *Server) setupHTTPHandlers() {
	// Create separate ServeMux instances for main server and admin
	mainMux := http.NewServeMux()
	adminMux := http.NewServeMux()

	// Set up main server handlers
	mainMux.HandleFunc("/", s.handlePackageRequest)
	mainMux.HandleFunc("/acng-report", s.handleReportRequest)
	mainMux.HandleFunc("/health", s.HandleHealth)
	mainMux.HandleFunc("/ready", s.HandleReady)
	mainMux.HandleFunc("/metrics", s.HandleMetrics)

	// Add direct key endpoint for GPG keys
	if s.localKeyManager != nil {
		mainMux.HandleFunc("/gpg/", s.ServeKey)
	}

	// Set up admin server handlers
	s.setupAdminHandlers(adminMux)

	// Use http.Server directly with the configured mux
	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", s.cfg.ListenAddress, s.cfg.Port),
		Handler: mainMux,
	}

	// Apply ACL middleware to the main HTTP server
	if s.acl != nil {
		s.httpServer.Handler = s.acl.Middleware(mainMux)
	}

	// Set up admin server if configured
	if s.cfg.AdminPort > 0 {
		var adminHandler http.Handler = adminMux

		// Apply auth middleware if configured
		if s.cfg.AdminAuth {
			wrappedMux := http.NewServeMux()
			for path, handler := range extractHandlers(adminMux) {
				wrappedMux.HandleFunc(path, s.HandleAdminAuth(handler))
			}
			adminHandler = wrappedMux
		}

		s.adminServer = &http.Server{
			Addr:    fmt.Sprintf("%s:%d", s.cfg.ListenAddress, s.cfg.AdminPort),
			Handler: adminHandler,
		}
	}

	// Configure HTTPS server if enabled
	if s.cfg.TLSEnabled {
		s.setupHTTPSServer(mainMux)
	}
}

// extractHandlers is a helper function to extract handlers from a ServeMux
// This is needed for wrapping each handler with authentication
func extractHandlers(mux *http.ServeMux) map[string]http.HandlerFunc {
	// Implementation would extract handlers from the mux
	// But for now, we'll return an empty map as this is just a placeholder
	return make(map[string]http.HandlerFunc)
}

// setupAdminHandlers configures admin-related HTTP handlers
//
// This method sets up all the administrative HTTP routes including dashboard,
// statistics, cache operations, and monitoring functions.
func (s *Server) setupAdminHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/", s.adminDashboard)
	mux.HandleFunc("/admin", s.adminDashboard)
	mux.HandleFunc("/stats", s.adminGetStats)
	mux.HandleFunc("/flush", s.adminFlushCache)
	mux.HandleFunc("/clear", s.adminClearCache)
	mux.HandleFunc("/search", s.adminSearchCache)
	mux.HandleFunc("/memory", s.adminMemoryStats)
	mux.HandleFunc("/report", s.handleReportRequest)
	mux.HandleFunc("/cleanup-prefetcher", s.adminCleanupPrefetcher)
	mux.HandleFunc("/cleanup-memory", s.adminForceMemoryCleanup)
}

// handleDirectoryRequest serves directory listing or redirects to dashboard
//
// This method provides a simple directory listing for repositories or
// redirects to the admin dashboard depending on the request path.
func (s *Server) handleDirectoryRequest(w http.ResponseWriter, r *http.Request) {
	// Check if this is an admin request
	if strings.HasPrefix(r.URL.Path, "/admin") {
		// Redirect to dashboard
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	// Otherwise show a simple directory listing
	path := strings.TrimPrefix(r.URL.Path, "/")
	dirs := []string{"debian", "ubuntu", "centos", "fedora"}

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, "<html><head><title>Repository Listing</title></head><body>")
	fmt.Fprintf(w, "<h1>Available Repositories</h1><ul>")

	for _, dir := range dirs {
		fmt.Fprintf(w, `<li><a href="/%s">%s</a></li>`, dir, dir)
	}

	fmt.Fprintf(w, "</ul></body></html>")
}

// StartWithContext begins listening for HTTP requests with the provided context
//
// This method starts the HTTP server with a specific context for lifecycle management.
// It allows for controlled startup and graceful shutdown of the server.
//
// Parameters:
// - ctx: Context for lifecycle management
//
// Returns:
// - Error if server startup fails
func (s *Server) StartWithContext(ctx context.Context) error {
	var err error
	s.startOnce.Do(func() {
		// Start main HTTP server
		go func() {
			if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("HTTP server error: %v", err)
			}
		}()

		// Start admin server if configured
		if s.adminServer != nil {
			go func() {
				if err := s.adminServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Printf("Admin server error: %v", err)
				}
			}()
		}

		// Start HTTPS server if configured
		if s.httpsServer != nil {
			go func() {
				if err := s.httpsServer.ListenAndServeTLS(s.cfg.TLSCert, s.cfg.TLSKey); err != nil && err != http.ErrServerClosed {
					log.Printf("HTTPS server error: %v", err)
				}
			}()
		}

		log.Printf("Server started on port %d", s.cfg.Port)
		if s.adminServer != nil {
			log.Printf("Admin server started on port %d", s.cfg.AdminPort)
		}
	})

	return err
}

// Start begins listening for HTTP requests
//
// This method starts the HTTP server using a background context.
// It's a convenience wrapper around StartWithContext.
//
// Returns:
// - Error if server startup fails
func (s *Server) Start() error {
	return s.StartWithContext(context.Background())
}

// Shutdown safely shuts down the server and all components
//
// This method performs an orderly shutdown of all server components,
// including HTTP servers, memory monitor, and backend connections.
// It ensures resources are properly released and pending operations
// are completed or cancelled.
//
// Returns:
// - Error if shutdown encounters issues
func (s *Server) Shutdown() error {
	var shutdownErr error

	s.shutdownOnce.Do(func() {
		log.Println("Shutting down server...")

		// Create context with timeout for shutdown
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Shutdown HTTP servers
		if s.httpServer != nil {
			if err := s.httpServer.Shutdown(ctx); err != nil {
				shutdownErr = err
				log.Printf("HTTP server shutdown error: %v", err)
			}
		}

		if s.adminServer != nil {
			if err := s.adminServer.Shutdown(ctx); err != nil {
				shutdownErr = err
				log.Printf("Admin server shutdown error: %v", err)
			}
		}

		if s.httpsServer != nil {
			if err := s.httpsServer.Shutdown(ctx); err != nil {
				shutdownErr = err
				log.Printf("HTTPS server shutdown error: %v", err)
			}
		}

		// Stop memory monitor
		if s.memoryMonitor != nil {
			s.memoryMonitor.Stop()
		}

		// Shutdown backend
		if s.backend != nil {
			if err := s.backend.Shutdown(); err != nil {
				log.Printf("Backend shutdown error: %v", err)
				if shutdownErr == nil {
					shutdownErr = err
				}
			}
		}

		// Close cache connections if needed
		if closer, ok := s.cache.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				log.Printf("Cache close error: %v", err)
				if shutdownErr == nil {
					shutdownErr = err
				}
			}
		}

		// Close shutdown channel
		close(s.shutdownCh)
		log.Println("Server shutdown complete.")
	})

	return shutdownErr
}

// wrapWithMetrics wraps a handler with metrics collection.
//
// This method provides instrumentation for HTTP handlers by collecting metrics
// about request duration, bytes served, and cache hit/miss information.
// It uses a custom response writer wrapper to capture the response status code
// and number of bytes written.
//
// Parameters:
// - next: The HTTP handler function to wrap with metrics collection
//
// Returns:
// - An HTTP handler function that collects metrics and delegates to the original handler
func (s *Server) wrapWithMetrics(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		clientIP := extractClientIP(r)

		// Extract package name from URL if possible
		packageName := s.packageMapper.GetPackageNameForHash(r.URL.Path)

		// Set metrics for the current request
		s.metrics.SetLastClientIP(clientIP)

		// Create wrapped response writer to capture status and bytes
		wrapped := newResponseWriter(w)

		// Process the request
		next(wrapped, r)

		// Calculate duration
		duration := time.Since(start)

		// Record basic request metrics
		s.metrics.RecordRequest(r.URL.Path, duration, clientIP, packageName)

		// Record cache hit/miss based on status code
		switch wrapped.statusCode {
		case http.StatusOK:
			s.metrics.RecordCacheHit(r.URL.Path, wrapped.bytesWritten)
		case http.StatusNotFound:
			s.metrics.RecordCacheMiss(r.URL.Path, 0)
		default:
			if wrapped.statusCode >= 400 {
				s.metrics.RecordError(r.URL.Path)
			}
		}

		// Record bytes served
		if wrapped.bytesWritten > 0 {
			s.metrics.RecordBytesServed(wrapped.bytesWritten)
			s.metrics.SetLastFileSize(wrapped.bytesWritten)
		}

		// Log request details if enabled
		if s.cfg.Log.Debug.TraceHTTPRequests {
			log.Printf("[HTTP TRACE] %s %s - %d (%s, %s)",
				r.Method, r.URL.Path, wrapped.statusCode,
				byteCountSI(wrapped.bytesWritten), duration)
		}
	}
}

// Helper function to format byte sizes
func byteCountSI(b int64) string {
	const unit = 1000
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "kMGTPE"[exp])
}

// responseWriter wraps http.ResponseWriter to capture status code and bytes written
type responseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

// Write captures the number of bytes written
func (rw *responseWriter) Write(b []byte) (int, error) {
	// If WriteHeader was not called, we need to set the default
	if rw.statusCode == 0 {
		rw.statusCode = http.StatusOK
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += int64(n)
	return n, err
}

// newResponseWriter creates a new response writer wrapper
func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{
		ResponseWriter: w,
		statusCode:     0, // Will be set on first Write or WriteHeader call
	}
}

// WriteHeader captures the status code
func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// handlePackageRequest handles package download requests
//
// This method processes incoming requests for packages, handling cache lookups,
// conditional requests, backend fetching, and error conditions. It also supports
// specialized handling for repository index files and GPG keys.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request to process
func (s *Server) handlePackageRequest(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	clientIP := extractClientIP(r)

	// Set metrics for the current request
	if s.metrics != nil {
		s.metrics.SetLastClientIP(clientIP)
	}

	// Handle conditional requests
	path := strings.TrimPrefix(r.URL.Path, "/")
	if s.handleConditionalRequest(w, r, path) {
		return
	}

	// Try to fetch package data
	data, err := s.backend.Fetch(path)

	// Handle fetch error
	if err != nil {
		http.Error(w, fmt.Sprintf("Error fetching %s: %v", path, err), http.StatusInternalServerError)
		if s.metrics != nil {
			s.metrics.RecordError(path)
		}
		return
	}

	// If we got empty data, return 404
	if len(data) == 0 {
		http.NotFound(w, r)
		if s.metrics != nil {
			s.metrics.RecordCacheMiss(path, 0)
		}
		return
	}

	// Handle Release/InRelease files - check for key errors
	if strings.HasSuffix(path, "/Release") || strings.HasSuffix(path, "/InRelease") {
		if s.localKeyManager != nil {
			keyID, isKeyError := s.localKeyManager.DetectKeyError(data)
			if isKeyError {
				// Try to fetch the key
				err := s.localKeyManager.FetchKey(keyID)
				if err == nil {
					// Key fetched successfully, refresh the release file
					s.backend.RefreshReleaseData(path)
					// Now try again
					data, err = s.backend.Fetch(path)
					if err != nil {
						http.Error(w, fmt.Sprintf("Error fetching %s after key retrieval: %v", path, err), http.StatusInternalServerError)
						return
					}
				}
			}
		}
	}

	// Set content type based on file extension
	contentType := getContentType(path)
	w.Header().Set("Content-Type", contentType)

	// Set content length
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))

	// Write response with error checking
	if _, err := w.Write(data); err != nil {
		log.Printf("Error writing response: %v", err)
		return
	}

	// Record metrics
	if s.metrics != nil {
		duration := time.Since(startTime)
		// Get package name from path if possible
		packageName := s.packageMapper.GetPackageNameForHash(path)
		s.metrics.RecordRequest(path, duration, clientIP, packageName)
		s.metrics.RecordCacheHit(path, int64(len(data)))
		s.metrics.RecordBytesServed(int64(len(data)))
		s.metrics.SetLastFileSize(int64(len(data)))
	}
}

// handleConditionalRequest handles If-Modified-Since requests
//
// This method checks if a requested resource has been modified since the time
// specified in the If-Modified-Since header, returning a 304 Not Modified
// response if appropriate.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request containing conditional headers
// - path: Path of the requested resource
//
// Returns:
// - boolean indicating whether the request was handled (true) or should continue processing (false)
func (s *Server) handleConditionalRequest(w http.ResponseWriter, r *http.Request, path string) bool {
	// Check if we have If-Modified-Since header
	ifModifiedSince := r.Header.Get("If-Modified-Since")
	if ifModifiedSince == "" {
		return false
	}

	// Parse the header
	modifiedSince, err := time.Parse(time.RFC1123, ifModifiedSince)
	if err != nil {
		return false
	}

	// Get last modified time from cache
	lastModified := s.cache.GetLastModified(path)

	// If not modified, return 304
	if !lastModified.IsZero() && !lastModified.After(modifiedSince) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}

	return false
}

// getContentType determines the content type based on the file extension
//
// This function returns the appropriate MIME type for a given file path
// based on its extension. It handles common package and repository file types.
//
// Parameters:
// - path: The file path to analyze
//
// Returns:
// - The MIME content type as a string
func getContentType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".deb":
		return "application/vnd.debian.binary-package"
	case ".rpm":
		return "application/x-rpm"
	case ".gz":
		return "application/gzip"
	case ".xz":
		return "application/x-xz"
	case ".bz2":
		return "application/x-bzip2"
	case ".json":
		return "application/json"
	case ".asc", ".gpg":
		return "application/pgp-signature"
	default:
		if strings.HasSuffix(path, "Release") || strings.HasSuffix(path, "Packages") {
			return "text/plain"
		}
		return "application/octet-stream"
	}
}

// isIndexFile checks if the path is pointing to a repository index file
//
// This function determines whether a given path represents a repository
// index file (such as Release, Packages, or Sources files).
//
// Parameters:
// - path: The file path to analyze
//
// Returns:
// - Boolean indicating whether the path is an index file
func isIndexFile(path string) bool {
	indexPatterns := []string{
		"Release$", "Release.gpg$", "InRelease$",
		"Packages(.gz|.bz2|.xz)?$",
		"Sources(.gz|.bz2|.xz)?$",
		"Contents-.*(.gz|.bz2|.xz)?$",
	}

	for _, pattern := range indexPatterns {
		matched, _ := regexp.MatchString(pattern, path)
		if matched {
			return true
		}
	}

	return false
}

// handleReportRequest serves the apt-cacher-ng status report
//
// This method generates an HTML report showing cache statistics,
// recent requests, and system information, similar to the
// acng-report page in apt-cacher-ng.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
func (s *Server) handleReportRequest(w http.ResponseWriter, r *http.Request) {
	// Get cache statistics
	stats := s.cache.GetStats()

	// Get metrics statistics
	metricStats := s.metrics.GetStatistics()

	// Get memory statistics
	memStats := s.memoryMonitor.GetMemoryUsage()

	// Generate HTML report
	w.Header().Set("Content-Type", "text/html")
	html := fmt.Sprintf(`
	<!DOCTYPE html>
	<html>
	<head>
		<title>Apt-Cacher-Go Status Report</title>
		<style>
			body { font-family: sans-serif; margin: 20px; }
			table { border-collapse: collapse; width: 100%%; }
			th, td { text-align: left; padding: 8px; border: 1px solid #ddd; }
			th { background-color: #f2f2f2; }
			.stats { margin-bottom: 20px; }
		</style>
	</head>
	<body>
		<h1>Apt-Cacher-Go Status Report</h1>
		<div class="stats">
			<h2>Cache Statistics</h2>
			<table>
				<tr><th>Total Size</th><td>%s</td></tr>
				<tr><th>Items</th><td>%d</td></tr>
				<tr><th>Hit Rate</th><td>%.1f%%</td></tr>
				<tr><th>Hits</th><td>%d</td></tr>
				<tr><th>Misses</th><td>%d</td></tr>
			</table>
		</div>
		<div class="stats">
			<h2>Memory Statistics</h2>
			<table>
				<tr><th>Allocated</th><td>%.1f MB</td></tr>
				<tr><th>System</th><td>%.1f MB</td></tr>
				<tr><th>Goroutines</th><td>%d</td></tr>
				<tr><th>Memory Pressure</th><td>%.1f%%</td></tr>
			</table>
		</div>
		<div class="stats">
			<h2>Recent Requests</h2>
			<table>
				<tr><th>Path</th><th>Result</th><th>Time</th><th>Size</th></tr>
	`,
		byteCountSI(stats.CurrentSize),
		stats.Items,
		stats.HitRate*100,
		stats.Hits,
		stats.Misses,
		memStats["allocated_mb"].(float64),
		memStats["system_mb"].(float64),
		memStats["goroutines"].(int),
		memStats["memory_pressure"].(float64)*100,
	)

	// Add recent requests
	for i, req := range metricStats.RecentRequests {
		if i >= 10 {
			break // Limit to 10 recent requests
		}
		result := req.Result
		html += fmt.Sprintf(`
			<tr>
				<td>%s</td>
				<td>%s</td>
				<td>%s</td>
				<td>%s</td>
			</tr>
		`,
			req.Path,
			result,
			req.Time.Format(time.RFC3339),
			byteCountSI(req.Bytes),
		)
	}

	// Close HTML
	html += `
			</table>
		</div>
		<p>Server uptime: %s</p>
		<p>Version: %s</p>
	</body>
	</html>
	`

	// Write the response
	fmt.Fprintf(w, html, time.Since(s.startTime).String(), s.version)
}

// extractClientIP extracts the client IP from a request
//
// This function gets the client IP address from an HTTP request,
// considering common proxy headers and fallbacks to RemoteAddr.
//
// Parameters:
// - r: The HTTP request to extract IP from
//
// Returns:
// - The client IP address as a string
func extractClientIP(r *http.Request) string {
	// Check for X-Forwarded-For header
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		return strings.TrimSpace(ips[0])
	}

	// Check for X-Real-IP header
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return xrip
	}

	// Use RemoteAddr
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// If no port in the address, use as is
		return r.RemoteAddr
	}

	return ip
}

// handleHighMemoryPressure is called when memory usage is high
//
// This method takes actions to reduce memory usage when the system
// is under memory pressure, such as forcing garbage collection,
// cleaning up prefetch operations, and clearing caches.
//
// Parameters:
// - pressure: Current memory pressure as a float (0.0-1.0)
func (s *Server) handleHighMemoryPressure(pressure float64) {
	log.Printf("High memory pressure detected (%.2f%%), taking action", pressure*100)

	// Force garbage collection
	runtime.GC()

	// Clear unnecessary caches
	if s.backend != nil {
		cleaned := s.backend.ForceCleanupPrefetcher()
		log.Printf("Cleaned up %d prefetch operations", cleaned)
	}

	// Package mapper cache clearing
	if s.packageMapper != nil {
		s.packageMapper.ClearCache()
		log.Printf("Cleared package mapper cache due to memory pressure")
	}
}

// ServeKey serves GPG keys from the key directory
//
// This method handles requests for GPG keys by extracting the key ID
// from the URL path and serving the corresponding key file.
//
// Parameters:
// - w: HTTP response writer
// - r: HTTP request
func (s *Server) ServeKey(w http.ResponseWriter, r *http.Request) {
	// Extract key ID from URL path
	path := strings.TrimPrefix(r.URL.Path, "/gpg/")
	keyID := strings.TrimSuffix(path, ".gpg")

	// Check if key exists
	if !s.localKeyManager.HasKey(keyID) {
		http.NotFound(w, r)
		return
	}

	// Get key path
	keyPath := s.localKeyManager.GetKeyPath(keyID)

	// Serve the key file
	http.ServeFile(w, r, keyPath)
}

// HandlePackageRequest is the exported version of handlePackageRequest
// Exported for testing and for use in https.go
func (s *Server) HandlePackageRequest(w http.ResponseWriter, r *http.Request) {
	s.handlePackageRequest(w, r)
}

// GetContentType is the exported version of getContentType
// Exported for testing
func GetContentType(path string) string {
	return getContentType(path)
}

// IsIndexFile is the exported version of isIndexFile
// Exported for testing
func IsIndexFile(path string) bool {
	return isIndexFile(path)
}

// HandleReportRequest is the exported version of handleReportRequest
// Exported for testing
func (s *Server) HandleReportRequest(w http.ResponseWriter, r *http.Request) {
	s.handleReportRequest(w, r)
}

// HandleHighMemoryPressure is the exported version of handleHighMemoryPressure
func (s *Server) HandleHighMemoryPressure(pressure float64) {
	s.handleHighMemoryPressure(pressure)
}
