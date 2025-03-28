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
	adminServer   *http.Server    // New field for admin server
	cache         *cachelib.Cache // Use the alias here too
	backend       *backend.Manager
	metrics       *metrics.Collector
	prometheus    *metrics.PrometheusCollector
	acl           *security.ACL
	mapper        *mapper.PathMapper
	packageMapper *mapper.PackageMapper // Add packageMapper field
	startTime     time.Time
	version       string
	logger        *log.Logger // Add logger field
	memoryMonitor *MemoryMonitor
	mutex         sync.Mutex
	startOnce     sync.Once
	shutdownOnce  sync.Once
	shutdownCh    chan struct{} // Add shutdown channel
}

// New creates a new Server instance
func New(cfg *config.Config, backendManager *backend.Manager, cache *cachelib.Cache, packageMapper *mapper.PackageMapper) (*Server, error) {
	// If we only received the config, initialize the other components
	if backendManager == nil && cache == nil {
		// Create a new cache - using the imported package, not the parameter
		cacheInstance, err := cachelib.New(cfg.CacheDir, 10*1024) // 10GB default
		if err != nil {
			return nil, fmt.Errorf("failed to create cache: %w", err)
		}

		// Create a package mapper
		pm := mapper.NewPackageMapper()

		// Create the path mapper
		pathMapper := mapper.New()

		// Create the backend manager
		manager, err := backend.New(cfg, cacheInstance, pathMapper, pm)
		if err != nil {
			return nil, fmt.Errorf("failed to create backend manager: %w", err)
		}

		// Now call ourselves with all components
		return New(cfg, manager, cacheInstance, pm)
	}

	// Create the server with the provided components
	s := &Server{
		cfg:           cfg,
		backend:       backendManager,
		cache:         cache,
		metrics:       metrics.New(), // Using the correct constructor
		packageMapper: packageMapper, // Add this line
		startTime:     time.Now(),
		version:       "1.0.0",
		logger:        log.New(os.Stdout, "apt-cacher-go: ", log.LstdFlags),
		shutdownCh:    make(chan struct{}), // Initialize shutdown channel
	}

	// Output configuration being used
	fmt.Printf("Creating server with: Cache Directory: %s\n", cfg.CacheDir)

	_, err := cfg.ParseCacheSize()
	if err != nil {
		return nil, fmt.Errorf("failed to parse cache size: %v", err)
	}
	// Using s.cache instead of creating a new instance
	s.cache = cache
	if s.cache == nil {
		return nil, fmt.Errorf("cache instance is nil")
	}

	// Create advanced path mapper
	m := mapper.New()

	// Add default repositories if not disabled
	addDefaultRepositories(cfg, m)

	// Register custom mapping rules from config
	for _, rule := range cfg.MappingRules {
		fmt.Printf("Adding mapping rule: %s %s -> %s (priority: %d)\n",
			rule.Type, rule.Pattern, rule.Repository, rule.Priority)

		switch rule.Type {
		case "regex":
			if err := m.AddRegexRule(rule.Pattern, rule.Repository, rule.Priority); err != nil {
				log.Printf("Warning: Invalid regex rule %q: %v", rule.Pattern, err)
			}
		case "prefix":
			m.AddPrefixRule(rule.Pattern, rule.Repository, rule.Priority)
		case "exact":
			m.AddExactRule(rule.Pattern, rule.Repository, rule.Priority)
		case "rewrite":
			if err := m.AddRewriteRule(rule.Pattern, rule.Repository, rule.RewriteRule, rule.Priority); err != nil {
				log.Printf("Warning: Invalid rewrite rule %q: %v", rule.Pattern, err)
			}
		}
	}
	// Create backend
	backendManager, err = backend.New(cfg, s.cache, m, s.packageMapper)
	if err != nil {
		return nil, fmt.Errorf("failed to create backend manager: %w", err)
	}
	s.backend = backendManager

	metricsCollector := metrics.New()
	prometheusCollector := metrics.NewPrometheusCollector()

	// Initialize ACL
	acl, err := security.New(cfg.AllowedIPs)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize ACL: %w", err)
	}

	// Create main HTTP handler
	mainMux := http.NewServeMux()

	// Create admin HTTP handler
	adminMux := http.NewServeMux()

	// Set server properties
	s.mapper = m
	s.metrics = metricsCollector
	s.prometheus = prometheusCollector
	s.acl = acl
	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.ListenAddress, cfg.Port),
		Handler: acl.Middleware(mainMux),
	}

	// Register main handlers (non-admin routes)
	mainMux.HandleFunc("/", s.wrapWithMetrics(s.handlePackageRequest))
	mainMux.HandleFunc("/acng-report.html", s.wrapWithMetrics(s.handleReportRequest))

	// Monitoring and health endpoints on main server
	mainMux.HandleFunc("/metrics", s.handleMetrics)
	mainMux.HandleFunc("/health", s.handleHealth)
	mainMux.HandleFunc("/ready", s.handleReady)

	// Add HTTPS handler for CONNECT method
	mainMux.HandleFunc("/https/", s.wrapWithMetrics(s.handleHTTPSRequest))

	// Add GPG key handler
	mainMux.HandleFunc("/gpg/", s.ServeKey)

	// Create and set up admin server if port is different from main port
	if cfg.AdminPort > 0 && cfg.AdminPort != cfg.Port {
		// Register admin routes on admin server
		adminMux.HandleFunc("/", s.handleAdminAuth(s.adminDashboard))
		adminMux.HandleFunc("/admin", s.handleAdminAuth(s.adminDashboard))
		adminMux.HandleFunc("/admin/", s.handleAdminAuth(s.adminDashboard))
		adminMux.HandleFunc("/admin/clearcache", s.handleAdminAuth(s.adminClearCache))
		adminMux.HandleFunc("/admin/flushexpired", s.handleAdminAuth(s.adminFlushExpired))
		adminMux.HandleFunc("/admin/stats", s.handleAdminAuth(s.adminGetStats))
		adminMux.HandleFunc("/admin/search", s.handleAdminAuth(s.adminSearchCache))
		adminMux.HandleFunc("/admin/cache", s.handleAdminAuth(s.adminCachePackage))
		adminMux.HandleFunc("/admin/cleanup-memory", s.handleAdminAuth(s.adminForceMemoryCleanup))
		adminMux.HandleFunc("/admin/cleanup-prefetcher", s.handleAdminAuth(s.adminCleanupPrefetcher))

		// Create admin server
		s.adminServer = &http.Server{
			Addr:    fmt.Sprintf("%s:%d", cfg.ListenAddress, cfg.AdminPort),
			Handler: acl.Middleware(adminMux),
		}

		log.Printf("Admin interface will be available at http://%s:%d/",
			cfg.ListenAddress, cfg.AdminPort)
	} else {
		// If admin port is same as main port or not configured,
		// add admin routes to main server
		mainMux.HandleFunc("/admin", s.handleAdminAuth(s.adminDashboard))
		mainMux.HandleFunc("/admin/", s.handleAdminAuth(s.adminDashboard))
		mainMux.HandleFunc("/admin/clearcache", s.handleAdminAuth(s.adminClearCache))
		mainMux.HandleFunc("/admin/flushexpired", s.handleAdminAuth(s.adminFlushExpired))
		mainMux.HandleFunc("/admin/stats", s.handleAdminAuth(s.adminGetStats))
		mainMux.HandleFunc("/admin/search", s.handleAdminAuth(s.adminSearchCache))
		mainMux.HandleFunc("/admin/cleanup-memory", s.handleAdminAuth(s.adminForceMemoryCleanup))

		log.Printf("Admin interface will be available at http://%s:%d/admin",
			cfg.ListenAddress, cfg.Port)
	}

	// If TLS is configured, set up HTTPS server
	if cfg.TLSEnabled && cfg.TLSCert != "" && cfg.TLSKey != "" {
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
			Addr:      fmt.Sprintf("%s:%d", cfg.ListenAddress, cfg.TLSPort),
			Handler:   acl.Middleware(mainMux),
			TLSConfig: tlsConfig,
		}
	}

	// Create memory monitor
	memoryMonitor := NewMemoryMonitor(1024, 2048, func(pressure int) {
		// Take action when memory pressure is high
		if pressure > 95 {
			// Clear caches to reduce memory pressure
			log.Printf("Critical memory pressure (%d%%), clearing caches", pressure)
			if packageMapper != nil {
				packageMapper.ClearCache()
			}

			// Force prefetcher cleanup
			if s.backend != nil {
				s.backend.ForceCleanupPrefetcher()
			}

			// Force garbage collection
			runtime.GC()
		}
	})
	memoryMonitor.Start()

	// Store in server instance
	s.memoryMonitor = memoryMonitor

	return s, nil
}

// Start begins listening for HTTP requests
func (s *Server) Start() error {
	var startErr error

	s.startOnce.Do(func() {
		// Initialize components in the main thread before starting server
		if s.memoryMonitor == nil {
			s.memoryMonitor = NewMemoryMonitor(1024, 2048, func(pressure int) {
				s.handleHighMemoryPressure(float64(pressure) / 100)
			})
			s.memoryMonitor.Start()
		}

		// Now start HTTP servers in goroutines
		go func() {
			log.Printf("Starting HTTP server on %s", s.httpServer.Addr)
			if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("HTTP server error: %v", err)
				startErr = err
			}
		}()

		// Start HTTPS server if configured
		if s.httpsServer != nil {
			go func() {
				log.Printf("Starting HTTPS server on %s", s.httpsServer.Addr)
				if err := s.httpsServer.ListenAndServeTLS(s.cfg.TLSCert, s.cfg.TLSKey); err != nil && err != http.ErrServerClosed {
					log.Printf("HTTPS server error: %v", err)
				}
			}()
		}

		// Start the admin server if configured separately
		if s.adminServer != nil && s.cfg.AdminPort != s.cfg.Port {
			go func() {
				log.Printf("Admin interface will be available at http://%s/admin", s.adminServer.Addr)
				if err := s.adminServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					log.Printf("Admin server error: %v", err)
				}
			}()
		}

		// Warm up the cache in the background
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel() // Make sure to call cancel to avoid context leak
		s.backend.PrefetchOnStartup(ctx)
	})

	return startErr
}

// Shutdown gracefully stops the server
func (s *Server) Shutdown() error {
	var err error

	// Use shutdownOnce with a timeout to avoid deadlocks
	s.shutdownOnce.Do(func() {
		log.Printf("Shutting down server...")

		// Create context with timeout for shutdown
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Close the shutdown channel first to signal other goroutines
		close(s.shutdownCh)

		// Shutdown HTTP server with timeout
		if s.httpServer != nil {
			if err := s.httpServer.Shutdown(ctx); err != nil {
				log.Printf("HTTP server shutdown error: %v", err)
			}
		}

		// Shutdown HTTPS server with timeout
		if s.httpsServer != nil {
			if err := s.httpsServer.Shutdown(ctx); err != nil {
				log.Printf("HTTPS server shutdown error: %v", err)
			}
		}

		// Shutdown admin server with timeout
		if s.adminServer != nil {
			if err := s.adminServer.Shutdown(ctx); err != nil {
				log.Printf("Admin server shutdown error: %v", err)
			}
		}

		// Shutdown the cache with timeout
		if s.cache != nil {
			// Use a timeout for cache close to avoid deadlocks
			cacheDone := make(chan struct{})
			go func() {
				s.cache.Close()
				close(cacheDone)
			}()

			select {
			case <-cacheDone:
				// Cache closed successfully
			case <-time.After(2 * time.Second):
				log.Printf("Warning: Cache close timed out")
			}
		}

		log.Printf("Server shutdown complete")
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

	requestPath := r.URL.Path

	// Start timing the request
	start := time.Now()
	clientIP := extractClientIP(r)

	// Use mutex when accessing packageMapper
	s.mutex.Lock()
	packageName := ""
	if s.packageMapper != nil {
		packageName = s.packageMapper.GetPackageNameForHash(requestPath)
	}
	s.mutex.Unlock()

	defer func() {
		// Use mutex when updating metrics
		s.mutex.Lock()
		s.metrics.RecordRequest(requestPath, time.Since(start), clientIP, packageName)
		s.mutex.Unlock()
	}()

	log.Printf("Request: %s %s", r.Method, requestPath)

	// Check for special APT-specific request headers
	if r.Header.Get("If-Modified-Since") != "" {
		// Fixed: Use correct Cache.Get() method parameters and return values
		data, err := s.cache.Get(requestPath)
		if err == nil && len(data) > 0 {
			// We have the file in cache, parse the If-Modified-Since header
			modTime, err := time.Parse(http.TimeFormat, r.Header.Get("If-Modified-Since"))
			if err == nil {
				// Get metadata information about the cached file
				// Fixed: Don't use os.Stat directly on cache data
				fileModTime := s.cache.GetLastModified(requestPath)
				if !fileModTime.IsZero() && !fileModTime.After(modTime) {
					// File hasn't been modified since the specified time
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}
		// If we don't have the file or it's modified, continue with normal flow
	}

	// Get client IP
	clientIP = r.RemoteAddr
	if idx := strings.LastIndex(clientIP, ":"); idx != -1 {
		clientIP = clientIP[:idx] // Strip port number if present
	}

	// Update last client IP with mutex protection
	s.mutex.Lock()
	s.metrics.SetLastClientIP(clientIP)
	s.mutex.Unlock()

	// Fetch the package
	data, err := s.backend.Fetch(requestPath)
	if err != nil {
		log.Printf("Error fetching %s: %v", requestPath, err)
		http.Error(w, "Package not found", http.StatusNotFound)
		return
	}

	// Update last file size with mutex protection
	s.mutex.Lock()
	s.metrics.SetLastFileSize(int64(len(data)))
	s.mutex.Unlock()

	// Determine content type
	contentType := getContentType(requestPath)

	// Set headers
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))

	// Add cache control headers for browsers/proxies
	if isIndexFile(requestPath) {
		// Index files expire sooner
		w.Header().Set("Cache-Control", "max-age=1800") // 30 minutes
	} else {
		// Package files can be cached longer
		w.Header().Set("Cache-Control", "max-age=2592000") // 30 days
	}

	// Send the response
	if _, err := w.Write(data); err != nil {
		log.Printf("Error writing package data: %v", err)
	}
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
