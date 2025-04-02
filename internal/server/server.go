package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/backend"
	cachelib "github.com/jdfalk/apt-cacher-go/internal/cache" // Use alias
	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/mapper"
	"github.com/jdfalk/apt-cacher-go/internal/metrics"
	"github.com/jdfalk/apt-cacher-go/internal/security"
)

// Server represents the apt-cacher HTTP server
type Server struct {
	cfg         *config.Config
	httpServer  *http.Server
	httpsServer *http.Server
	adminServer *http.Server // New field for admin server
	cache       Cache        // Use the alias here too
	backend     BackendManager
	metrics     MetricsCollector
	// Removed unused prometheus field
	acl           *security.ACL
	mapper        PathMapper
	packageMapper PackageMapper // Add packageMapper field
	startTime     time.Time
	version       string
	logger        *log.Logger // Add logger field
	memoryMonitor MemoryMonitorInterface
	mutex         sync.Mutex
	startOnce     sync.Once
	shutdownOnce  sync.Once
	shutdownCh    chan struct{} // Add shutdown channel
}

// New creates a new Server instance with the provided options
func New(cfg *config.Config, opts ServerOptions) (*Server, error) {
	var cache Cache
	var pathMapper PathMapper
	var packageMapper PackageMapper
	var backendManager BackendManager
	var metricsCollector MetricsCollector
	var memoryMonitor MemoryMonitorInterface

	// Create cache or use provided
	cache = opts.Cache
	if cache == nil {
		cacheInstance, err := cachelib.New(cfg.CacheDir, 10*1024*1024*1024) // 10GB default
		if err != nil {
			return nil, fmt.Errorf("failed to create cache: %w", err)
		}
		cache = &CacheAdapter{Cache: cacheInstance}
	}

	// Create path mapper or use provided
	pathMapper = opts.PathMapper
	if pathMapper == nil {
		mapperInstance := mapper.New()
		// Add default repositories
		for _, rule := range cfg.MappingRules {
			switch rule.Type {
			case "prefix":
				// Fix: Call AddRule with appropriate parameters instead
				if err := mapperInstance.AddRule("prefix", rule.Pattern, rule.Repository, rule.Priority); err != nil {
					return nil, fmt.Errorf("failed to add prefix rule: %w", err)
				}
			case "regex":
				if err := mapperInstance.AddRule("regex", rule.Pattern, rule.Repository, rule.Priority); err != nil {
					return nil, fmt.Errorf("failed to add regex rule: %w", err)
				}
			case "exact":
				if err := mapperInstance.AddRule("exact", rule.Pattern, rule.Repository, rule.Priority); err != nil {
					return nil, fmt.Errorf("failed to add exact rule: %w", err)
				}
			case "rewrite":
				// Handle rewrite case with additional rewrite rule parameter
				if rule.RewriteRule != "" {
					if err := mapperInstance.AddRewriteRule(rule.Pattern, rule.Repository, rule.RewriteRule, rule.Priority); err != nil {
						return nil, fmt.Errorf("failed to add rewrite rule: %w", err)
					}
				} else {
					if err := mapperInstance.AddRule("rewrite", rule.Pattern, rule.Repository, rule.Priority); err != nil {
						return nil, fmt.Errorf("failed to add rewrite rule: %w", err)
					}
				}
			}
		}
		// ADD THIS: Call addDefaultRepositories here
		addDefaultRepositories(cfg, mapperInstance)
		pathMapper = &MapperAdapter{PathMapper: mapperInstance}
	}

	// Create package mapper or use provided
	packageMapper = opts.PackageMapper
	if packageMapper == nil {
		packageMapper = mapper.NewPackageMapper()
	}

	// Create backend manager or use provided
	backendManager = opts.BackendManager
	if backendManager == nil {
		manager, err := backend.New(cfg, cache.(backend.CacheProvider),
			pathMapper.(backend.PathMapperProvider),
			packageMapper.(backend.PackageMapperProvider))
		if err != nil {
			return nil, fmt.Errorf("failed to create backend manager: %w", err)
		}
		backendManager = &BackendManagerAdapter{Manager: manager}
	}

	// Create metrics collector or use provided
	metricsCollector = opts.MetricsCollector
	if metricsCollector == nil {
		metricsCollector = &MetricsAdapter{Collector: metrics.New()}
	}

	// Create memory monitor or use provided
	memoryMonitor = opts.MemoryMonitor
	if memoryMonitor == nil {
		highWatermark := 1024     // Default 1GB in MB
		criticalWatermark := 2048 // Default 2GB in MB

		// Try to read from config metadata if available
		if val, ok := cfg.GetMetadata("memory_management.high_watermark_mb"); ok {
			if v, ok := val.(int); ok {
				highWatermark = v
			}
		}
		if val, ok := cfg.GetMetadata("memory_management.critical_watermark_mb"); ok {
			if v, ok := val.(int); ok {
				criticalWatermark = v
			}
		}

		// Create the monitor but store the pressure handler as a variable for later
		pressureHandler := func(pressure int) {
			// Will be assigned to the server instance later
		}

		monitor := NewMemoryMonitor(
			highWatermark,
			criticalWatermark,
			pressureHandler)
		memoryMonitor = &MemoryMonitorAdapter{MemoryMonitor: monitor}
	}

	// Create the server instance
	s := &Server{
		cfg:           cfg,
		cache:         cache,
		backend:       backendManager,
		mapper:        pathMapper,
		packageMapper: packageMapper,
		metrics:       metricsCollector,
		startTime:     time.Now(),
		version:       opts.Version,
		memoryMonitor: memoryMonitor,
		shutdownCh:    make(chan struct{}),
	}

	// Now we can update the memory pressure handler to use the server instance
	if monitor, ok := memoryMonitor.(*MemoryMonitorAdapter); ok {
		monitor.MemoryMonitor.SetPressureHandler(func(pressure int) {
			s.handleHighMemoryPressure(float64(pressure) / 100)
		})
	}

	// Configure logger
	var logWriter io.Writer = os.Stdout

	// Check if log file is specified in config
	if cfg.LogFile != "" {
		// Open log file in append mode, create if it doesn't exist
		logFile, err := os.OpenFile(cfg.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			// Log warning but continue with stdout only
			log.Printf("Warning: Could not open log file %s: %v, using stdout only", cfg.LogFile, err)
		} else {
			// Use MultiWriter to log to both stdout and file
			logWriter = io.MultiWriter(os.Stdout, logFile)
			log.Printf("Logging to %s and stdout", cfg.LogFile)
		}
	}

	// Use the configured writer
	if opts.Logger != nil {
		// If a specific logger is provided, use it instead
		s.logger = log.New(opts.Logger, "apt-cacher-go: ", log.LstdFlags)
	} else {
		// Use our configured writer (stdout or stdout+file)
		s.logger = log.New(logWriter, "apt-cacher-go: ", log.LstdFlags)
	}

	// Also redirect standard logger to our writer to capture all log messages
	log.SetOutput(logWriter)

	// Set up HTTP handlers
	s.setupHTTPHandlers()

	// Start the memory monitor
	s.memoryMonitor.Start()

	return s, nil
}

// setupHTTPHandlers creates and configures all HTTP handlers
func (s *Server) setupHTTPHandlers() {
	// Create separate ServeMux instances for main server and admin
	mainMux := http.NewServeMux()
	adminMux := http.NewServeMux()

	// Set up main server handlers
	mainMux.HandleFunc("/", s.handlePackageRequest)
	mainMux.HandleFunc("/acng-report.html", s.handleReportRequest)
	mainMux.HandleFunc("/health", s.handleHealth)
	mainMux.HandleFunc("/ready", s.handleReady)
	mainMux.HandleFunc("/metrics", s.handleMetrics)

	// Add key endpoint - NEW
	mainMux.HandleFunc("/gpg-key/", s.handleKeyRequest)

	// Fix the CONNECT handler by using method checking in the default handler
	// instead of direct method registration which causes the panic
	mainMux.HandleFunc("/connect-tunnel", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			s.handleConnectRequest(w, r)
			return
		}
		http.NotFound(w, r)
	})

	// Set up admin handlers on the adminMux - adminHome is used for the root path
	s.setupAdminHandlers(adminMux)

	// Configure the HTTP server to use mainMux
	s.httpServer = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", s.cfg.ListenAddress, s.cfg.Port),
		Handler:      mainMux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Configure the admin server to use adminMux
	if s.cfg.AdminPort > 0 {
		adminAddr := fmt.Sprintf("%s:%d", s.cfg.ListenAddress, s.cfg.AdminPort)
		s.adminServer = &http.Server{
			Addr:    adminAddr,
			Handler: adminMux,
		}
		s.logger.Printf("Admin interface will be available at http://%s/\n", adminAddr)
	} else {
		// If admin port not configured, use a path-based approach on the main server
		// FIX: Use "/admin/" path prefix to avoid conflict with root handler
		mainMux.Handle("/admin/", http.StripPrefix("/admin", adminMux))
		s.logger.Printf("Admin interface will be available at http://%s:%d/admin/\n",
			s.cfg.ListenAddress, s.cfg.Port)
	}

	// Set up HTTPS server if enabled (function is now in https.go)
	s.setupHTTPSServer(mainMux)
}

// setupAdminHandlers configures admin-related HTTP handlers
func (s *Server) setupAdminHandlers(mux *http.ServeMux) {
	// FIX: These are the handler methods being called from the admin mux
	// Using adminDashboard as the root handler instead of undefined adminHome
	mux.HandleFunc("/", s.adminDashboard) // Changed from adminHome
	mux.HandleFunc("/stats", s.adminGetStats)
	mux.HandleFunc("/flush", s.adminFlushCache)
	mux.HandleFunc("/clear", s.adminClearCache)
	mux.HandleFunc("/search", s.adminSearchCache)
	mux.HandleFunc("/memory", s.adminMemoryStats)
	mux.HandleFunc("/report", s.handleDirectoryRequest)

	// Add any missing handlers that were called
	mux.HandleFunc("/cleanup-prefetcher", s.adminCleanupPrefetcher)
	mux.HandleFunc("/cleanup-memory", s.adminForceMemoryCleanup)
}

// Add a simple directory handler implementation
func (s *Server) handleDirectoryRequest(w http.ResponseWriter, r *http.Request) {
	// Implement basic directory listing or redirect to dashboard
	http.Redirect(w, r, "/report", http.StatusFound)
}

// StartWithContext begins listening for HTTP requests with the provided context
func (s *Server) StartWithContext(ctx context.Context) error {
	var startErr error

	s.startOnce.Do(func() {
		// Create context for server operations
		serverCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Start the HTTP server
		go func() {
			s.logger.Printf("Starting HTTP server on %s", s.httpServer.Addr)
			if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				s.logger.Printf("HTTP server error: %v", err)
				startErr = err
			}
		}()

		// Start HTTPS server if configured
		if s.httpsServer != nil {
			go func() {
				s.logger.Printf("Starting HTTPS server on %s", s.httpsServer.Addr)
				if err := s.httpsServer.ListenAndServeTLS(s.cfg.TLSCert, s.cfg.TLSKey); err != nil && err != http.ErrServerClosed {
					s.logger.Printf("HTTPS server error: %v", err)
				}
			}()
		}

		// Start the admin server if configured separately
		if s.adminServer != nil && s.cfg.AdminPort != s.cfg.Port {
			go func() {
				s.logger.Printf("Starting admin server on %s", s.adminServer.Addr)
				if err := s.adminServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					s.logger.Printf("Admin server error: %v", err)
				}
			}()
		}

		// Warm up the cache in the background
		s.backend.PrefetchOnStartup(serverCtx)

		// Wait for shutdown signal
		<-serverCtx.Done()
		if err := s.Shutdown(); err != nil {
			s.logger.Printf("Error during shutdown: %v", err)
		}
	})

	return startErr
}

// Start begins listening for HTTP requests
func (s *Server) Start() error {
	return s.StartWithContext(context.Background())
}

// Shutdown safely shuts down the server and all components
func (s *Server) Shutdown() error {
	var err error

	// Use sync.Once to ensure we only shutdown once
	s.shutdownOnce.Do(func() {
		s.logger.Println("Shutting down server...")

		// Signal all goroutines to stop
		close(s.shutdownCh)

		// Create context with timeout for HTTP servers
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Shutdown HTTP servers first
		var httpErr, httpsErr, adminErr error
		if s.httpServer != nil {
			httpErr = s.httpServer.Shutdown(ctx)
		}
		if s.httpsServer != nil {
			httpsErr = s.httpsServer.Shutdown(ctx)
		}
		if s.adminServer != nil {
			adminErr = s.adminServer.Shutdown(ctx)
		}

		// Shutdown backend components
		if s.backend != nil {
			s.backend.ForceCleanupPrefetcher()
		}

		// Stop memory monitor
		if s.memoryMonitor != nil {
			s.memoryMonitor.Stop()
		}

		// Safely close the cache with timeout to prevent deadlocks
		if s.cache != nil {
			// See if cache implements a Close method
			if closer, ok := s.cache.(interface{ Close() error }); ok {
				// Create a timeout context for cache closing
				closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer closeCancel()

				// Channel to signal when cache is closed
				done := make(chan struct{})
				var closeErr error

				// Close cache in a goroutine
				go func() {
					defer close(done)
					closeErr = closer.Close()
				}()

				// Wait for either closure or timeout
				select {
				case <-done:
					// Cache closed successfully
					if closeErr != nil {
						s.logger.Printf("Error closing cache: %v", closeErr)
					}
				case <-closeCtx.Done():
					s.logger.Println("Warning: Cache close operation timed out")
				}
			}
		}

		// Combine errors
		if httpErr != nil {
			err = httpErr
		} else if httpsErr != nil {
			err = httpsErr
		} else if adminErr != nil {
			err = adminErr
		}

		s.logger.Println("Server shutdown complete")
	})

	return err
}

// wrapWithMetrics wraps a handler with metrics collection
func (s *Server) wrapWithMetrics(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract client IP
		clientIP := extractClientIP(r)

		// Start timing
		start := time.Now()

		// Wrap response writer to capture status
		rw := newResponseWriter(w)

		// Get associated package name if available
		packageName := ""
		if s.packageMapper != nil {
			s.mutex.Lock()
			packageName = s.packageMapper.GetPackageNameForHash(r.URL.Path)
			s.mutex.Unlock()

			// Add debug logging
			if packageName != "" {
				log.Printf("Found package name for %s: %s", r.URL.Path, packageName)
			}
		}

		// Process the request
		next(rw, r)

		// Record timing and status
		duration := time.Since(start)
		status := "hit"
		if rw.statusCode >= 400 {
			status = "error"
		} else if rw.statusCode == 307 || rw.statusCode == 302 {
			status = "redirect"
		} else if rw.statusCode == 200 && strings.Contains(r.Header.Get("X-Cache"), "MISS") {
			status = "miss"
		}

		// Log with enhanced information
		packageInfo := ""
		if packageName != "" {
			packageInfo = fmt.Sprintf(" [%s]", packageName)
		}

		log.Printf("%s %s%s\t%.2f\t%s\t%s",
			clientIP, r.URL.Path, packageInfo,
			float64(duration.Milliseconds())/1000.0, status,
			byteCountSI(rw.bytesWritten))

		// Record metrics with full information including client IP and package name
		s.mutex.Lock()
		s.metrics.RecordRequest(r.URL.Path, duration, clientIP, packageName)
		s.metrics.RecordBytesServed(rw.bytesWritten)

		// Update hit/miss stats based on status
		if status == "hit" {
			s.metrics.RecordCacheHit(r.URL.Path, rw.bytesWritten)
		} else if status == "miss" {
			s.metrics.RecordCacheMiss(r.URL.Path, rw.bytesWritten)
		} else if status == "error" {
			s.metrics.RecordError(r.URL.Path)
		}
		s.mutex.Unlock()
	}
}

// Helper function to format byte sizes
func byteCountSI(b int64) string {
	const unit = 1000
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}

	div := int64(1)
	exp := 0
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
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += int64(n)
	return n, err
}

// newResponseWriter creates a new response writer wrapper
func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK, // Default status code
	}
}

// WriteHeader captures the status code
func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// handlePackageRequest handles package download requests
func (s *Server) handlePackageRequest(w http.ResponseWriter, r *http.Request) {
	// Start timing for this request
	start := time.Now()

	// Check if this is an HTTPS request
	if r.URL.Scheme == "https" || strings.HasPrefix(r.URL.Path, "/https/") {
		s.handleHTTPSRequest(w, r)
		return
	}

	// Extract request information
	requestPath := r.URL.Path
	clientIP := extractClientIP(r)

	// Check for conditional request
	if r.Header.Get("If-Modified-Since") != "" {
		if s.handleConditionalRequest(w, r, requestPath) {
			return // Request was handled conditionally
		}
	}

	// Record client information
	s.mutex.Lock()
	s.metrics.SetLastClientIP(clientIP)
	// Get package name for metrics and logging
	packageName := ""
	if s.packageMapper != nil {
		packageName = s.packageMapper.GetPackageNameForHash(requestPath)
		// Send package name to metrics if available
		if packageName != "" {
			s.metrics.RecordRequest(requestPath, time.Since(start), clientIP, packageName)
		}
	}
	s.mutex.Unlock()

	// Fetch the package data
	data, err := s.backend.Fetch(requestPath)
	if err != nil {
		s.logger.Printf("Error fetching %s: %v", requestPath, err)
		http.Error(w, "Package not found", http.StatusNotFound)
		return
	}

	// NEW: Check for GPG key errors before sending response
	s.mutex.Lock()
	keyManager := s.backend.KeyManager()
	s.mutex.Unlock()

	// Type assertion to check if it's our KeyManager interface
	if keyM, ok := keyManager.(KeyManager); ok {
		keyID, hasKeyError := keyM.DetectKeyError(data)
		if hasKeyError {
			log.Printf("Detected missing GPG key: %s in response, attempting to fetch", keyID)

			// Try to fetch the key
			err := keyM.FetchKey(keyID)
			if err != nil {
				log.Printf("Failed to fetch key %s: %v", keyID, err)
			} else {
				log.Printf("Successfully fetched key %s", keyID)

				// Check if this is a direct key request
				if strings.Contains(requestPath, "/keys/") || strings.Contains(requestPath, ".gpg") {
					// If client is requesting a key directly, serve it
					keyPath := keyM.GetKeyPath(keyID)
					if keyPath != "" {
						keyData, err := os.ReadFile(keyPath)
						if err == nil {
							// Serve the key directly
							w.Header().Set("Content-Type", "application/pgp-keys")
							w.Write(keyData)
							return
						}
					}
				}

				// For non-key requests with key errors, we'll still send the original response
				// but the next client request should succeed with the key now available
			}
		}
	}

	// Update last file size
	s.mutex.Lock()
	s.metrics.SetLastFileSize(int64(len(data)))
	s.mutex.Unlock()

	// Set response headers
	w.Header().Set("Content-Type", getContentType(requestPath))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))

	// Add cache control headers
	if isIndexFile(requestPath) {
		w.Header().Set("Cache-Control", "max-age=1800") // 30 minutes
	} else {
		w.Header().Set("Cache-Control", "max-age=2592000") // 30 days
	}

	// Send the response
	if _, err := w.Write(data); err != nil {
		s.logger.Printf("Error writing package data: %v", err)
	}
}

// handleConditionalRequest handles If-Modified-Since requests
func (s *Server) handleConditionalRequest(w http.ResponseWriter, r *http.Request, path string) bool {
	data, err := s.cache.Get(path)
	if err == nil && len(data) > 0 {
		modTime, err := time.Parse(http.TimeFormat, r.Header.Get("If-Modified-Since"))
		if err == nil {
			fileModTime := s.cache.GetLastModified(path)
			if !fileModTime.IsZero() && !fileModTime.After(modTime) {
				w.WriteHeader(http.StatusNotModified)
				return true
			}
		}
	}
	return false
}

// getContentType determines the content type based on the file extension
func getContentType(path string) string {
	switch {
	case strings.HasSuffix(path, ".deb"):
		return "application/vnd.debian.binary-package"
	case strings.HasSuffix(path, ".gz"):
		return "application/gzip"
	case strings.HasSuffix(path, ".bz2"):
		return "application/x-bzip2"
	case strings.HasSuffix(path, ".xz"):
		return "application/x-xz"
	case strings.HasSuffix(path, ".lz4"):
		return "application/x-lz4"
	case strings.HasSuffix(path, ".Release"), strings.HasSuffix(path, "Packages"), strings.HasSuffix(path, "Sources"):
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}

// isIndexFile checks if the path is pointing to a repository index file
func isIndexFile(path string) bool {
	indexPatterns := []string{
		"Release", "InRelease", "Release.gpg",
		"Packages", "Sources", "Contents",
	}

	for _, pattern := range indexPatterns {
		if strings.Contains(path, pattern) {
			return true
		}
	}

	return false
}

// handleReportRequest serves the apt-cacher-ng status report
func (s *Server) handleReportRequest(w http.ResponseWriter, r *http.Request) {
	// Generate a simple HTML report with cache statistics
	stats := s.metrics.GetStatistics()

	html := fmt.Sprintf(`
    <!DOCTYPE html>
    <html>
    <head>
        <title>apt-cacher-go Report</title>
    </head>
    <body>
        <h1>apt-cacher-go Statistics</h1>
        <p>Total requests: %d</p>
        <p>Cache hits: %d (%.1f%%)</p>
        <p>Cache misses: %d</p>
        <p>Average response time: %.2f ms</p>
        <p>Total bytes served: %d</p>
        <h2>Recent Requests</h2>
        <table border="1">
            <tr><th>Path</th><th>Time</th><th>Result</th></tr>
    `, stats.TotalRequests, stats.CacheHits, stats.HitRate*100, stats.CacheMisses, stats.AvgResponseTime, stats.BytesServed)

	for _, req := range stats.RecentRequests {
		html += fmt.Sprintf("<tr><td>%s</td><td>%.2f ms</td><td>%s</td></tr>",
			req.Path, float64(req.Duration.Milliseconds()), req.Result)
	}

	html += `
        </table>
    </body>
    </html>
    `

	w.Header().Set("Content-Type", "text/html")
	if _, err := w.Write([]byte(html)); err != nil {
		log.Printf("Error writing report HTML: %v", err)
	}
}

// handleAdminAuth wraps a handler function with admin authentication
func (s *Server) handleAdminAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// If admin auth is disabled, skip authentication entirely
		if !s.cfg.AdminAuth {
			handler(w, r)
			return
		}

		// Otherwise require auth
		username, password, ok := r.BasicAuth()
		if !ok || !s.validateAdminAuth(username, password) {
			w.Header().Set("WWW-Authenticate", `Basic realm="apt-cacher-go Admin"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		handler(w, r)
	}
}

// validateAdminAuth checks if the provided credentials match the configured admin auth
func (s *Server) validateAdminAuth(username, password string) bool {
	// If admin auth is disabled, all auth requests should pass
	if !s.cfg.AdminAuth {
		return true
	}

	// Otherwise, check credentials
	if s.cfg.AdminUser == "" || s.cfg.AdminPassword == "" {
		// Missing credentials, deny access
		log.Printf("Admin auth enabled but credentials not configured")
		return false
	}

	// Check the provided credentials
	return username == s.cfg.AdminUser && password == s.cfg.AdminPassword
}

// Port returns the HTTP port the server is listening on
func (s *Server) Port() int {
	return s.cfg.Port
}

// TLSPort returns the HTTPS port if TLS is enabled
func (s *Server) TLSPort() int {
	if s.cfg.TLSEnabled {
		return s.cfg.TLSPort
	}
	return 0
}

// addDefaultRepositories adds default repository backends and mappings if enabled
func addDefaultRepositories(cfg *config.Config, m *mapper.PathMapper) {
	// Skip if default repos are disabled
	if cfg.DisableDefaultRepos {
		log.Printf("Default repositories disabled via configuration")
		return
	}

	// Check for standard repositories and add any missing ones
	standardRepos := []struct {
		name     string
		url      string
		pattern  string // Added pattern field for mapping
		priority int
	}{
		{"ubuntu-archive", "http://archive.ubuntu.com/ubuntu", "archive.ubuntu.com/ubuntu", 100},
		{"ubuntu-security", "http://security.ubuntu.com/ubuntu", "security.ubuntu.com/ubuntu", 95},
		{"debian", "http://deb.debian.org/debian", "deb.debian.org/debian", 90},
		{"debian-security", "http://security.debian.org/debian-security", "security.debian.org/debian-security", 85},
		{"debian-backports", "http://deb.debian.org/debian-backports", "deb.debian.org/debian-backports", 80},
		{"ubuntu-ports", "http://ports.ubuntu.com/ubuntu-ports", "ports.ubuntu.com/ubuntu-ports", 75},
		{"kali", "http://http.kali.org/kali", "http.kali.org/kali", 70},
	}

	// Track which standard repos exist
	existingRepos := make(map[string]bool)
	for _, backend := range cfg.Backends {
		existingRepos[backend.Name] = true
	}

	// Add missing standard repositories
	reposAdded := 0
	for _, repo := range standardRepos {
		if !existingRepos[repo.name] {
			cfg.Backends = append(cfg.Backends, config.Backend{
				Name:     repo.name,
				URL:      repo.url,
				Priority: repo.priority,
			})
			reposAdded++
		}
	}

	if reposAdded > 0 {
		log.Printf("Added %d missing standard repository backends", reposAdded)
	}

	// Track which mapping rules exist for both standard and third-party repos
	existingMappings := make(map[string]bool)
	for _, rule := range cfg.MappingRules {
		existingMappings[rule.Pattern] = true
	}

	// Add mapping rules for standard repositories if they don't exist
	standardMappingsAdded := 0
	for _, repo := range standardRepos {
		if !existingMappings[repo.pattern] {
			m.AddPrefixRule(repo.pattern, repo.name, repo.priority)

			// Add to MappingRules list for config consistency
			cfg.MappingRules = append(cfg.MappingRules, config.MappingRule{
				Type:       "prefix",
				Pattern:    repo.pattern,
				Repository: repo.name,
				Priority:   repo.priority,
			})
			standardMappingsAdded++
			existingMappings[repo.pattern] = true // Mark as existing
		}
	}

	if standardMappingsAdded > 0 {
		log.Printf("Added %d standard repository mapping rules", standardMappingsAdded)
	}

	// Check for third-party repository mappings and add if missing
	thirdPartyMappings := []struct {
		repoName string
		pattern  string
		priority int
	}{
		{"docker", "download.docker.com/linux/ubuntu", 60},
		{"grafana", "packages.grafana.com/oss/deb", 55},
		{"plex", "downloads.plex.tv/repo/deb", 50},
		{"postgresql", "apt.postgresql.org/pub/repos/apt", 45},
		{"hwraid", "hwraid.le-vert.net/ubuntu", 40},
	}

	// Add third-party mappings if they don't exist
	thirdPartyMappingsAdded := 0
	for _, mapping := range thirdPartyMappings {
		if !existingMappings[mapping.pattern] {
			m.AddPrefixRule(mapping.pattern, mapping.repoName, mapping.priority)

			// Add to MappingRules list for config consistency
			cfg.MappingRules = append(cfg.MappingRules, config.MappingRule{
				Type:       "prefix",
				Pattern:    mapping.pattern,
				Repository: mapping.repoName,
				Priority:   mapping.priority,
			})
			thirdPartyMappingsAdded++
		}
	}

	if thirdPartyMappingsAdded > 0 {
		log.Printf("Added %d third-party repository mapping rules", thirdPartyMappingsAdded)
	}

	// Add development release handling configuration if it doesn't exist
	if cfg.Metadata == nil {
		cfg.Metadata = make(map[string]interface{})
	}

	if _, exists := cfg.Metadata["dev_release"]; !exists {
		cfg.Metadata["dev_release"] = map[string]interface{}{
			"enabled": true,
			"codenames": []string{
				"oracular",
				"noble",
				"devel",
				"experimental",
			},
			"skip_missing_components": []string{
				"-security",
				"-updates",
				"-backports",
			},
			"suppress_errors": true,
		}
		log.Printf("Added development release handling configuration")
	}
}

// extractClientIP extracts the client IP from a request
func extractClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first (for proxies)
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		// X-Forwarded-For can contain multiple IPs, take the first one
		ips := strings.Split(forwarded, ",")
		return strings.TrimSpace(ips[0])
	}

	// Otherwise use RemoteAddr
	ip := r.RemoteAddr
	// Remove port if present
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}

// ServeKey serves GPG keys from the key directory
func (s *Server) ServeKey(w http.ResponseWriter, r *http.Request) {
	keyID := path.Base(r.URL.Path)
	keyID = strings.TrimSuffix(keyID, ".gpg")

	s.mutex.Lock()
	keyManager := s.backend.KeyManager()
	s.mutex.Unlock()

	// Type assertion to check if it's our KeyManager interface
	if keyManager == nil {
		http.Error(w, "Key management not available", http.StatusNotFound)
		return
	}

	// Use type assertion to access methods
	if km, ok := keyManager.(KeyManager); ok {
		if !km.HasKey(keyID) {
			http.Error(w, "Key not found", http.StatusNotFound)
			return
		}

		keyPath := km.GetKeyPath(keyID)
		if keyPath == "" {
			http.Error(w, "Key not available", http.StatusNotFound)
			return
		}

		http.ServeFile(w, r, keyPath)
	} else {
		http.Error(w, "Key management not properly configured", http.StatusInternalServerError)
	}
}

// handleHighMemoryPressure is called when memory usage is high
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
		// Just call ClearCache directly since we know it exists
		s.packageMapper.ClearCache()
		log.Printf("Cleared package mapper cache due to memory pressure")
	}
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

// Add missing admin handler methods
func (s *Server) adminFlushCache(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Call the existing flush expired method which does what we need
	s.adminFlushExpired(w, r)
}

func (s *Server) adminMemoryStats(w http.ResponseWriter, r *http.Request) {
	// Get memory statistics
	s.mutex.Lock()
	memStats := s.memoryMonitor.GetMemoryUsage()
	s.mutex.Unlock()

	// Return as JSON
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(memStats); err != nil {
		http.Error(w, "Error encoding memory stats", http.StatusInternalServerError)
	}
}

// Add this new handler function
func (s *Server) handleKeyRequest(w http.ResponseWriter, r *http.Request) {
	// Extract key ID from URL path
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 3 {
		http.Error(w, "Invalid key request", http.StatusBadRequest)
		return
	}

	keyID := pathParts[len(pathParts)-1]
	// Remove .gpg extension if present
	keyID = strings.TrimSuffix(keyID, ".gpg")

	s.mutex.Lock()
	keyManager := s.backend.KeyManager()
	s.mutex.Unlock()

	// Type assertion to check if it's our KeyManager interface
	if keyM, ok := keyManager.(KeyManager); ok {
		// Check if we have the key
		if !keyM.HasKey(keyID) {
			// Try to fetch it
			err := keyM.FetchKey(keyID)
			if err != nil {
				log.Printf("Failed to fetch key %s: %v", keyID, err)
				http.Error(w, "Key not found", http.StatusNotFound)
				return
			}
		}

		// Get key path
		keyPath := keyM.GetKeyPath(keyID)
		if keyPath == "" {
			http.Error(w, "Key not available", http.StatusNotFound)
			return
		}

		// Read and serve the key
		keyData, err := os.ReadFile(keyPath)
		if err != nil {
			log.Printf("Error reading key file: %v", err)
			http.Error(w, "Error reading key", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/pgp-keys")
		w.Write(keyData)
	} else {
		http.Error(w, "Key management not available", http.StatusServiceUnavailable)
	}
}
