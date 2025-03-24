package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/backend"
	"github.com/jdfalk/apt-cacher-go/internal/cache"
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
	cache       *cache.Cache
	backend     *backend.Manager
	metrics     *metrics.Collector
	prometheus  *metrics.PrometheusCollector
	acl         *security.ACL
	mapper      *mapper.PathMapper
	startTime   time.Time
	version     string
	logger      *log.Logger // Add logger field
}

// New creates a new Server instance
func New(cfg *config.Config) (*Server, error) {
	maxCacheSize, err := cfg.ParseCacheSize()
	if err != nil {
		return nil, fmt.Errorf("failed to parse cache size: %v", err)
	}
	cacheInstance, err := cache.New(cfg.CacheDir, maxCacheSize)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cache: %w", err)
	}

	// Create advanced path mapper
	m := mapper.New()

	// Register custom mapping rules from config
	for _, rule := range cfg.MappingRules {
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

	b := backend.New(cfg, cacheInstance, m)
	metricsCollector := metrics.New()
	prometheusCollector := metrics.NewPrometheusCollector()

	// Initialize ACL
	acl, err := security.New(cfg.AllowedIPs)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize ACL: %w", err)
	}

	// Create HTTP handler
	mux := http.NewServeMux()

	s := &Server{
		cfg:        cfg,
		cache:      cacheInstance,
		backend:    b,
		metrics:    metricsCollector,
		prometheus: prometheusCollector,
		acl:        acl,
		mapper:     m,
		startTime:  time.Now(),
		version:    "1.0.0",                                              // Set version
		logger:     log.New(os.Stdout, "apt-cacher-go: ", log.LstdFlags), // Initialize logger
		httpServer: &http.Server{
			Addr:    fmt.Sprintf("%s:%d", cfg.ListenAddress, cfg.Port),
			Handler: acl.Middleware(mux),
		},
	}

	// Register handlers
	mux.HandleFunc("/", s.wrapWithMetrics(s.handlePackageRequest))
	mux.HandleFunc("/acng-report.html", s.wrapWithMetrics(s.handleReportRequest))

	// Admin routes
	mux.HandleFunc("/admin", s.handleAdminAuth(s.adminDashboard))
	mux.HandleFunc("/admin/", s.handleAdminAuth(s.adminDashboard))
	mux.HandleFunc("/admin/clearcache", s.handleAdminAuth(s.adminClearCache))
	mux.HandleFunc("/admin/flushexpired", s.handleAdminAuth(s.adminFlushExpired))
	mux.HandleFunc("/admin/stats", s.handleAdminAuth(s.adminGetStats))
	mux.HandleFunc("/admin/search", s.handleAdminAuth(s.adminSearchCache))

	// Monitoring and health endpoints
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ready", s.handleReady)

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
			Handler:   acl.Middleware(mux),
			TLSConfig: tlsConfig,
		}
	}

	return s, nil
}

// Start begins listening for HTTP requests
func (s *Server) Start() error {
	// Start HTTPS server if configured
	if s.httpsServer != nil {
		go func() {
			log.Printf("Starting HTTPS server on %s", s.httpsServer.Addr)
			if err := s.httpsServer.ListenAndServeTLS(
				s.cfg.TLSCert, s.cfg.TLSKey,
			); err != http.ErrServerClosed {
				log.Fatalf("HTTPS server error: %v", err)
			}
		}()
	}

	// Start HTTP server
	log.Printf("Starting HTTP server on %s", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully stops the server
func (s *Server) Shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Shutdown the backend
	s.backend.Shutdown()

	// Shutdown HTTPS server if it exists
	if s.httpsServer != nil {
		if err := s.httpsServer.Shutdown(ctx); err != nil {
			return err
		}
	}

	// Shutdown HTTP server
	return s.httpServer.Shutdown(ctx)
}

// wrapWithMetrics wraps a handler with metrics collection
func (s *Server) wrapWithMetrics(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap the response writer to capture status code
		wrapped := newResponseWriter(w)

		// Increment in-progress requests
		s.prometheus.IncRequestsInProgress()
		s.prometheus.IncConnections()

		defer func() {
			duration := time.Since(start)
			status := fmt.Sprintf("%d", wrapped.statusCode)

			// Record metrics
			s.prometheus.RecordRequest(status)

			// Determine path type for response time metrics
			pathType := "package"
			if strings.Contains(r.URL.Path, "Release") ||
				strings.Contains(r.URL.Path, "Packages") ||
				strings.Contains(r.URL.Path, "Sources") {
				pathType = "index"
			} else if strings.Contains(r.URL.Path, "admin") {
				pathType = "admin"
			}

			s.prometheus.RecordResponseTime(pathType, duration)
			s.prometheus.DecRequestsInProgress()
			s.prometheus.DecConnections()

			// Record in internal metrics
			s.metrics.RecordRequest(r.URL.Path, duration)
		}()

		// Call the original handler
		h(wrapped, r)
	}
}

// handleReady serves the readiness check endpoint
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte(`{"status":"ready"}`)); err != nil {
		log.Printf("Error writing ready response: %v", err)
	}
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

// newResponseWriter creates a new response writer wrapper
func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{w, http.StatusOK}
}

// WriteHeader captures the status code
func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// handlePackageRequest handles package download requests
func (s *Server) handlePackageRequest(w http.ResponseWriter, r *http.Request) {
	requestPath := r.URL.Path

	// Start timing the request
	start := time.Now()
	defer func() {
		s.metrics.RecordRequest(requestPath, time.Since(start))
	}()

	log.Printf("Request: %s %s", r.Method, requestPath)

	// Check for special APT-specific request headers
	if r.Header.Get("If-Modified-Since") != "" {
		// We need to use the appropriate methods to handle conditional requests

		// First check if the file is in cache - use the actual API of cache.Get
		filePath, found, err := s.cache.Get(requestPath)
		if err == nil && found {
			// Parse the If-Modified-Since header
			modTime, err := time.Parse(http.TimeFormat, r.Header.Get("If-Modified-Since"))
			if err == nil {
				// Compare with cached file's last modified time
				fileInfo, err := os.Stat(string(filePath))
				if err == nil && !fileInfo.ModTime().After(modTime) {
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}
		// If we don't have the file or it's modified, continue with normal flow
	}

	// Fetch the package
	data, err := s.backend.Fetch(requestPath)
	if err != nil {
		log.Printf("Error fetching %s: %v", requestPath, err)
		http.Error(w, "Package not found", http.StatusNotFound)
		return
	}

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
	if !s.cfg.AdminAuth || s.cfg.AdminUser == "" || s.cfg.AdminPassword == "" {
		return false
	}

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
