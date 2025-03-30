package server

import (
	"context"
	"crypto/tls"
	"fmt"
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
	cfg           *config.Config
	httpServer    *http.Server
	httpsServer   *http.Server
	adminServer   *http.Server // New field for admin server
	cache         Cache        // Use the alias here too
	backend       BackendManager
	metrics       MetricsCollector
	prometheus    *metrics.PrometheusCollector
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
	var err error
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
			log.Printf("Adding mapping rule: %s %s -> %s (priority: %d)",
				rule.Type, rule.Pattern, rule.Repository, rule.Priority)
			mapperInstance.AddRule(rule.Type, rule.Pattern, rule.Repository, rule.Priority)
		}
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
		monitor := NewMemoryMonitor(
			cfg.MemoryHighWatermark,
			cfg.MemoryCriticalWatermark,
			func(pressure int) {
				log.Printf("Memory pressure: %d", pressure)
			})
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

	// Configure logger
	if opts.Logger != nil {
		s.logger = log.New(opts.Logger, "apt-cacher-go: ", log.LstdFlags)
	} else {
		s.logger = log.New(os.Stdout, "apt-cacher-go: ", log.LstdFlags)
	}

	// Set up HTTP handlers
	s.setupHTTPHandlers()

	// Start the memory monitor
	s.memoryMonitor.Start()

	return s, nil
}

// setupHTTPHandlers creates and configures all HTTP handlers
func (s *Server) setupHTTPHandlers() {
	// Create main HTTP handler
	mainMux := http.NewServeMux()

	// Register main handlers (non-admin routes)
	mainMux.HandleFunc("/", s.wrapWithMetrics(s.handlePackageRequest))
	mainMux.HandleFunc("/acng-report.html", s.wrapWithMetrics(s.handleReportRequest))

	// Monitoring and health endpoints
	mainMux.HandleFunc("/metrics", s.handleMetrics)
	mainMux.HandleFunc("/health", s.handleHealth)
	mainMux.HandleFunc("/ready", s.handleReady)

	// Add HTTPS handler
	mainMux.HandleFunc("/https/", s.wrapWithMetrics(s.handleHTTPSRequest))

	// Add GPG key handler
	mainMux.HandleFunc("/gpg/", s.ServeKey)

	// Setup main HTTP server
	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", s.cfg.ListenAddress, s.cfg.Port),
		Handler: s.acl.Middleware(mainMux),
	}

	// Setup admin routes
	s.setupAdminHandlers(mainMux)

	// Setup HTTPS server if configured
	s.setupHTTPSServer(mainMux)
}

// setupAdminHandlers configures admin-related HTTP handlers
func (s *Server) setupAdminHandlers(mainMux *http.ServeMux) {
	// Create admin HTTP handler
	adminMux := http.NewServeMux()

	// Admin dashboard and related endpoints
	adminHandlers := map[string]http.HandlerFunc{
		"/":                         s.adminDashboard,
		"/admin":                    s.adminDashboard,
		"/admin/":                   s.adminDashboard,
		"/admin/clearcache":         s.adminClearCache,
		"/admin/flushexpired":       s.adminFlushExpired,
		"/admin/stats":              s.adminGetStats,
		"/admin/search":             s.adminSearchCache,
		"/admin/cache":              s.adminCachePackage,
		"/admin/cleanup-memory":     s.adminForceMemoryCleanup,
		"/admin/cleanup-prefetcher": s.adminCleanupPrefetcher,
	}

	// Check if admin port is different
	if s.cfg.AdminPort > 0 && s.cfg.AdminPort != s.cfg.Port {
		// Register admin routes on separate admin server
		for path, handler := range adminHandlers {
			adminMux.HandleFunc(path, s.handleAdminAuth(handler))
		}

		// Create admin server
		s.adminServer = &http.Server{
			Addr:    fmt.Sprintf("%s:%d", s.cfg.ListenAddress, s.cfg.AdminPort),
			Handler: s.acl.Middleware(adminMux),
		}

		s.logger.Printf("Admin interface will be available at http://%s:%d/",
			s.cfg.ListenAddress, s.cfg.AdminPort)
	} else {
		// Register admin routes on main server
		for path, handler := range adminHandlers {
			mainMux.HandleFunc(path, s.handleAdminAuth(handler))
		}

		s.logger.Printf("Admin interface will be available at http://%s:%d/admin",
			s.cfg.ListenAddress, s.cfg.Port)
	}
}

// setupHTTPSServer configures HTTPS server if enabled
func (s *Server) setupHTTPSServer(mainMux *http.ServeMux) {
	if s.cfg.TLSEnabled && s.cfg.TLSCert != "" && s.cfg.TLSKey != "" {
		tlsConfig := &tls.Config{
			MinVersion: tls.VersionTLS12,
			CipherSuites: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
				tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			},
		}

		s.httpsServer = &http.Server{
			Addr:      fmt.Sprintf("%s:%d", s.cfg.ListenAddress, s.cfg.TLSPort),
			Handler:   s.acl.Middleware(mainMux),
			TLSConfig: tlsConfig,
		}
	}
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
		s.Shutdown()
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
			s.metrics.RecordPackageAccess(packageName)
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
	if s.httpsServer != nil {
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

	// Add standard Debian/Ubuntu repositories if none defined
	if len(cfg.Backends) == 0 {
		log.Printf("Adding default repository backends")

		cfg.Backends = append(cfg.Backends, []config.Backend{
			{Name: "ubuntu-archive", URL: "http://archive.ubuntu.com/ubuntu", Priority: 100},
			{Name: "ubuntu-security", URL: "http://security.ubuntu.com/ubuntu", Priority: 95},
			{Name: "debian", URL: "http://deb.debian.org/debian", Priority: 90},
			{Name: "debian-security", URL: "http://security.debian.org/debian-security", Priority: 85},
			{Name: "debian-backports", URL: "http://deb.debian.org/debian-backports", Priority: 80},
			{Name: "ubuntu-ports", URL: "http://ports.ubuntu.com/ubuntu-ports", Priority: 75},
			{Name: "kali", URL: "http://http.kali.org/kali", Priority: 70},
		}...)
	}

	// Add default mapping rules if none defined
	if len(cfg.MappingRules) == 0 {
		log.Printf("Adding default repository mapping rules")

		// Default handling for common third-party repositories
		defaultMappings := []struct {
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

		for _, mapping := range defaultMappings {
			m.AddPrefixRule(mapping.pattern, mapping.repoName, mapping.priority)

			// Add to MappingRules list for config consistency
			cfg.MappingRules = append(cfg.MappingRules, config.MappingRule{
				Type:       "prefix",
				Pattern:    mapping.pattern,
				Repository: mapping.repoName,
				Priority:   mapping.priority,
			})
		}
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
	hasKey := s.backend.KeyManager() == nil || !s.backend.KeyManager().HasKey(keyID)
	s.mutex.Unlock()

	if hasKey {
		http.Error(w, "Key not found", http.StatusNotFound)
		return
	}

	s.mutex.Lock()
	keyPath := s.backend.KeyManager().GetKeyPath(keyID)
	s.mutex.Unlock()

	if keyPath == "" {
		http.Error(w, "Key not available", http.StatusNotFound)
		return
	}

	http.ServeFile(w, r, keyPath)
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
