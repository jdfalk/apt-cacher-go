package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/cache"
	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/mapper"
	"github.com/jdfalk/apt-cacher-go/internal/metrics"
	"github.com/jdfalk/apt-cacher-go/internal/security"
)

// Server represents the apt-cacher-go server instance
//
// This struct contains all dependencies required for server operation, including
// configuration, cache, backend management, mapping services, and HTTP servers.
// It provides methods for starting/stopping the server and processing requests.
type Server struct {
	cfg            *config.Config
	mutex          sync.RWMutex
	httpServer     *http.Server
	httpsServer    *http.Server
	adminServer    *http.Server
	backend        BackendManager
	cache          Cache
	mapper         PathMapper
	packageMapper  PackageMapper
	metrics        MetricsCollector
	memoryMonitor  MemoryMonitorInterface
	logger         *log.Logger
	version        string
	startTime      time.Time
	acl            *security.ACL
	prefetchCtx    context.Context
	prefetchCancel context.CancelFunc
	shutdownOnce   sync.Once
}

// ResponseRecorder wraps an http.ResponseWriter to record response information
//
// This wrapper captures key metrics about the HTTP response including status code,
// bytes written, and timing information for performance monitoring and metrics collection.
type ResponseRecorder struct {
	http.ResponseWriter
	status        int
	bytesWritten  int64
	wroteHeader   bool
	startTime     time.Time
	requestPath   string
	clientIP      string
	packageName   string
	metrics       MetricsCollector
	packageMapper PackageMapper
}

// New creates a new server instance
//
// This function initializes a new server with the provided configuration and options.
// It sets up HTTP(S) servers, initializes dependencies, configures access control,
// and prepares the server for startup.
//
// Parameters:
//   - cfg: Configuration for the server
//   - opts: Options for customizing the server initialization
//
// Returns:
//   - A fully initialized Server instance
//   - An error if initialization fails
func New(cfg *config.Config, opts ServerOptions) (*Server, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration cannot be nil")
	}

	// Create logger
	var logWriter io.Writer
	if opts.Logger != nil {
		logWriter = opts.Logger
	} else {
		logWriter = os.Stdout
		if cfg.LogFile != "" {
			f, err := os.OpenFile(cfg.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				log.Printf("Failed to open log file: %v, using stdout instead", err)
			} else {
				logWriter = io.MultiWriter(os.Stdout, f)
			}
		}
	}
	logger := log.New(logWriter, "", log.LstdFlags)

	// Create memory monitor if not provided
	var memoryMonitor MemoryMonitorInterface
	if opts.MemoryMonitor != nil {
		memoryMonitor = opts.MemoryMonitor
	} else {
		// Default memory limits
		highWatermark := 1024     // 1GB
		criticalWatermark := 2048 // 2GB
		if cfg.ApplicationMemoryMB > 0 {
			highWatermark = cfg.ApplicationMemoryMB
			criticalWatermark = highWatermark * 2
		}
		mon := NewMemoryMonitor(highWatermark, criticalWatermark, nil)
		memoryMonitor = &MemoryMonitorAdapter{mon}
	}

	// Create ACL from allowed IPs
	acl, err := security.New(cfg.AllowedIPs)
	if err != nil {
		return nil, fmt.Errorf("failed to create ACL: %w", err)
	}

	// Create context for prefetching
	prefetchCtx, prefetchCancel := context.WithCancel(context.Background())

	// Create the server instance
	server := &Server{
		cfg:            cfg,
		logger:         logger,
		version:        opts.Version,
		startTime:      time.Now(),
		acl:            acl,
		prefetchCtx:    prefetchCtx,
		prefetchCancel: prefetchCancel,
		memoryMonitor:  memoryMonitor,
	}

	// Set up memory pressure handler
	if mon, ok := memoryMonitor.(*MemoryMonitorAdapter); ok {
		mon.SetPressureHandler(func(pressure int) {
			server.handleHighMemoryPressure(float64(pressure) / 100.0)
		})
	}

	// Initialize dependencies if not provided
	if err := server.initializeDependencies(opts); err != nil {
		return nil, err
	}

	// Set up HTTP routes
	mainMux := server.setupRoutes()

	// Create HTTP server
	server.httpServer = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.ListenAddress, cfg.Port),
		Handler: acl.Middleware(mainMux),
	}

	// Set up HTTPS server if enabled
	server.setupHTTPSServer(mainMux)

	// Set up admin server if enabled
	if cfg.AdminPort > 0 {
		adminMux := http.NewServeMux()
		server.setupAdminRoutes(adminMux)
		server.adminServer = &http.Server{
			Addr:    fmt.Sprintf("%s:%d", cfg.ListenAddress, cfg.AdminPort),
			Handler: adminMux,
		}
	}

	// Start memory monitor
	memoryMonitor.Start()

	return server, nil
}

// initializeDependencies initializes server dependencies if not provided in options
//
// This method creates and initializes the required components for server operation
// including cache, backend manager, mappers, and metrics collectors.
//
// Parameters:
//   - opts: Server options containing optional dependency implementations
//
// Returns:
//   - Error if initialization fails
func (s *Server) initializeDependencies(opts ServerOptions) error {
	// Set backend manager
	if opts.BackendManager != nil {
		s.backend = opts.BackendManager
	} else {
		// Would create a real backend manager here with the configuration
		return fmt.Errorf("backend manager is required")
	}

	// Set cache
	if opts.Cache != nil {
		s.cache = opts.Cache
	} else {
		// Create cache with configuration
		maxSize, err := s.cfg.ParseCacheSize()
		if err != nil {
			return fmt.Errorf("invalid cache size: %w", err)
		}
		cacheInstance, err := cache.New(s.cfg.CacheDir, maxSize)
		if err != nil {
			return fmt.Errorf("failed to create cache: %w", err)
		}
		s.cache = &CacheAdapter{Cache: cacheInstance}
	}

	// Set path mapper
	if opts.PathMapper != nil {
		s.mapper = opts.PathMapper
	} else {
		// Create default path mapper
		pathMapper := mapper.New()
		s.mapper = &MapperAdapter{PathMapper: pathMapper}
	}

	// Set package mapper
	if opts.PackageMapper != nil {
		s.packageMapper = opts.PackageMapper
	} else {
		// Create default package mapper
		s.packageMapper = mapper.NewPackageMapper()
	}

	// Set metrics collector
	if opts.MetricsCollector != nil {
		s.metrics = opts.MetricsCollector
	} else {
		// Create default metrics collector
		s.metrics = &MetricsAdapter{Collector: metrics.New()}
	}

	return nil
}

// setupRoutes sets up the HTTP routes for the main server
//
// This method configures all HTTP routes for the main server, including
// package repositories, health checks, and metrics endpoints.
//
// Returns:
//   - Configured HTTP multiplexer with all routes
func (s *Server) setupRoutes() *http.ServeMux {
	mux := http.NewServeMux()

	// Health and metrics endpoints
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ready", s.handleReady)
	mux.HandleFunc("/metrics", s.handleMetrics)

	// HTTPS CONNECT handler for tunneling
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			s.handleConnectRequest(w, r)
			return
		}

		// Handle HTTPS requests directly
		if r.TLS != nil {
			s.handleHTTPSRequest(w, r)
			return
		}

		// Default to package handling for HTTP requests
		s.handlePackageRequest(w, r)
	})

	return mux
}

// setupAdminRoutes sets up the HTTP routes for the admin server
//
// This method configures all HTTP routes for the admin interface,
// including dashboard, cache management, and statistics endpoints.
//
// Parameters:
//   - mux: HTTP multiplexer to add routes to
func (s *Server) setupAdminRoutes(mux *http.ServeMux) {
	// Wrap handlers with admin authentication if enabled
	var wrapHandler func(http.HandlerFunc) http.HandlerFunc
	if s.cfg.AdminAuth {
		wrapHandler = s.HandleAdminAuth
	} else {
		wrapHandler = func(h http.HandlerFunc) http.HandlerFunc { return h }
	}

	// Admin dashboard
	mux.HandleFunc("/", wrapHandler(s.adminDashboard))

	// Admin API endpoints
	mux.HandleFunc("/api/stats", wrapHandler(s.adminGetStats))
	mux.HandleFunc("/api/cache/clear", wrapHandler(s.adminClearCache))
	mux.HandleFunc("/api/cache/flush-expired", wrapHandler(s.adminFlushExpired))
	mux.HandleFunc("/api/cache/search", wrapHandler(s.adminSearchCache))
	mux.HandleFunc("/api/cache/package", wrapHandler(s.adminCachePackage))
	mux.HandleFunc("/api/prefetcher/cleanup", wrapHandler(s.adminCleanupPrefetcher))
	mux.HandleFunc("/api/memory/cleanup", wrapHandler(s.adminForceMemoryCleanup))

	// Health checks (no auth required)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/ready", s.handleReady)
	mux.HandleFunc("/metrics", s.handleMetrics)
}

// Start starts the server
//
// This method starts the HTTP servers (HTTP, HTTPS, and admin if configured)
// and blocks until the server is stopped.
//
// Returns:
//   - Error if the server fails to start
func (s *Server) Start() error {
	return s.StartWithContext(context.Background())
}

// StartWithContext starts the server with a context
//
// This method starts the HTTP servers (HTTP, HTTPS, and admin if configured)
// with a provided context for cancellation. It blocks until the server is
// stopped or the context is cancelled.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - Error if the server fails to start
func (s *Server) StartWithContext(ctx context.Context) error {
	// Log server information
	s.logger.Printf("Starting apt-cacher-go %s on %s:%d", s.version, s.cfg.ListenAddress, s.cfg.Port)
	s.logger.Printf("Cache directory: %s", s.cfg.CacheDir)

	// Determine log level
	logLevel := s.cfg.LogLevel
	if logLevel == "" {
		logLevel = s.cfg.Log.Level
	}
	s.logger.Printf("Log level: %s", logLevel)

	// Start prefetching in background if enabled
	if s.cfg.Prefetch.Enabled && s.cfg.Prefetch.WarmupOnStartup {
		go s.backend.PrefetchOnStartup(s.prefetchCtx)
	}

	// Start admin server if configured
	if s.adminServer != nil {
		go func() {
			s.logger.Printf("Starting admin server on %s:%d", s.cfg.ListenAddress, s.cfg.AdminPort)
			if err := s.adminServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				s.logger.Printf("Admin server error: %v", err)
			}
		}()
	}

	// Start HTTPS server if configured
	if s.httpsServer != nil {
		go func() {
			s.logger.Printf("Starting HTTPS server on %s:%d", s.cfg.ListenAddress, s.cfg.TLSPort)
			if err := s.httpsServer.ListenAndServeTLS(s.cfg.TLSCert, s.cfg.TLSKey); err != nil && err != http.ErrServerClosed {
				s.logger.Printf("HTTPS server error: %v", err)
			}
		}()
	}

	// Create a server done channel
	serverDone := make(chan error, 1)

	// Start main HTTP server in a goroutine
	go func() {
		s.logger.Printf("Server is ready to handle requests")
		err := s.httpServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			serverDone <- fmt.Errorf("HTTP server error: %w", err)
		} else {
			serverDone <- nil
		}
	}()

	// Wait for either context cancellation or server error
	select {
	case <-ctx.Done():
		// Context was cancelled, shutdown the server
		if err := s.Shutdown(); err != nil {
			return fmt.Errorf("server shutdown error: %w", err)
		}
		return ctx.Err()
	case err := <-serverDone:
		return err
	}
}

// Shutdown gracefully stops the server
//
// This method shuts down all HTTP servers and background processes,
// waiting for ongoing requests to complete before shutting down.
//
// Returns:
//   - Error if shutdown fails
func (s *Server) Shutdown() error {
	var shutdownErr error

	s.shutdownOnce.Do(func() {
		s.logger.Printf("Shutting down server...")

		// Cancel prefetch operations
		if s.prefetchCancel != nil {
			s.prefetchCancel()
		}

		// Create shutdown context with timeout
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Shutdown HTTP server
		if s.httpServer != nil {
			if err := s.httpServer.Shutdown(ctx); err != nil {
				s.logger.Printf("HTTP server shutdown error: %v", err)
				shutdownErr = err
			}
		}

		// Shutdown HTTPS server if running
		if s.httpsServer != nil {
			if err := s.httpsServer.Shutdown(ctx); err != nil {
				s.logger.Printf("HTTPS server shutdown error: %v", err)
				if shutdownErr == nil {
					shutdownErr = err
				}
			}
		}

		// Shutdown admin server if running
		if s.adminServer != nil {
			if err := s.adminServer.Shutdown(ctx); err != nil {
				s.logger.Printf("Admin server shutdown error: %v", err)
				if shutdownErr == nil {
					shutdownErr = err
				}
			}
		}

		// Stop backend manager if it has a Shutdown method
		if s.backend != nil {
			if backendWithShutdown, ok := s.backend.(interface{ Shutdown() error }); ok {
				if err := backendWithShutdown.Shutdown(); err != nil {
					s.logger.Printf("Backend shutdown error: %v", err)
					if shutdownErr == nil {
						shutdownErr = err
					}
				}
			}
			// Always force cleanup prefetcher
			s.backend.ForceCleanupPrefetcher()
		}

		// Stop memory monitor
		if s.memoryMonitor != nil {
			s.memoryMonitor.Stop()
		}

		// Close the cache
		if s.cache != nil {
			if cacheWithClose, ok := s.cache.(interface{ Close() error }); ok {
				if err := cacheWithClose.Close(); err != nil {
					s.logger.Printf("Cache close error: %v", err)
					if shutdownErr == nil {
						shutdownErr = err
					}
				}
			}
		}

		s.logger.Printf("Server shutdown complete")
	})

	return shutdownErr
}

// handlePackageRequest processes package download requests
//
// This method handles requests for packages and repository files. It checks
// the cache first, and if not found, fetches from the backend repository.
//
// Parameters:
//   - w: HTTP response writer
//   - r: HTTP request
func (s *Server) handlePackageRequest(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	clientIP := extractClientIP(r)
	requestPath := r.URL.Path

	// Set up response recorder to track metrics
	recorder := &ResponseRecorder{
		ResponseWriter: w,
		status:         http.StatusOK, // Default status
		startTime:      startTime,
		requestPath:    requestPath,
		clientIP:       clientIP,
		metrics:        s.metrics,
		packageMapper:  s.packageMapper,
	}

	if s.cfg.Log.Debug.TraceHTTPRequests {
		s.logger.Printf("[HTTP TRACE] %s request: %s from %s", r.Method, requestPath, clientIP)
		s.logger.Printf("[HTTP TRACE] Headers: %v", r.Header)
	}

	// Only allow GET requests
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		s.metrics.RecordError(requestPath)
		return
	}

	// Map the path to a repository and backend
	mappingResult, err := s.mapper.MapPath(requestPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid path: %v", err), http.StatusBadRequest)
		s.metrics.RecordError(requestPath)
		return
	}

	// Extract useful information from the mapping result
	repo := mappingResult.Repository
	cachePath := mappingResult.CachePath
	isIndex := mappingResult.IsIndex

	// Handle conditional requests (If-Modified-Since)
	if s.handleConditionalRequest(w, r, cachePath) {
		// Request was handled by conditional logic (304 Not Modified)
		return
	}

	// Attempt to get file from cache first
	if s.cache.Exists(cachePath) && s.cache.IsFresh(cachePath) {
		cacheData, err := s.cache.Get(cachePath)
		if err == nil {
			// Cache hit - serve the file

			// Track package name for hashes
			if strings.Contains(cachePath, "/by-hash/") {
				packageName := s.packageMapper.GetPackageNameForHash(cachePath)
				if packageName != "" {
					recorder.packageName = packageName
				}
			}

			// Record metrics before serving
			s.metrics.RecordCacheHit(cachePath, int64(len(cacheData)))
			s.metrics.SetLastClientIP(clientIP)
			s.metrics.SetLastFileSize(int64(len(cacheData)))

			// Calculate modified time for headers
			lastModified := s.cache.GetLastModified(cachePath)

			// Serve file with appropriate headers
			w.Header().Set("Content-Type", GetContentType(cachePath))
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(cacheData)))
			http.ServeContent(w, r, filepath.Base(cachePath), lastModified, strings.NewReader(string(cacheData)))

			// Record final metrics
			duration := time.Since(startTime)
			s.metrics.RecordRequest(requestPath, duration, clientIP, recorder.packageName)
			return
		}
	}

	// Cache miss - fetch from backend

	// Record cache miss here for metrics
	s.metrics.RecordCacheMiss(cachePath, 0) // Size will be updated after fetch
	data, err := s.backend.Fetch(cachePath)
	if err != nil {
		s.logger.Printf("Backend fetch error for %s: %v", cachePath, err)
		http.Error(w, fmt.Sprintf("Backend error: %v", err), http.StatusNotFound)
		s.metrics.RecordError(cachePath)
		return
	}

	// Check for empty data
	if len(data) == 0 {
		s.logger.Printf("Empty data received for %s", cachePath)
		http.Error(w, "Not found", http.StatusNotFound)
		s.metrics.RecordError(cachePath)
		return
	}

	// Handle key errors for Release/InRelease files
	if (strings.Contains(cachePath, "Release") || strings.Contains(cachePath, "InRelease")) &&
		s.backend.KeyManager() != nil {
		if keyManager, ok := s.backend.KeyManager().(KeyManager); ok {
			if keyID, needsKey := keyManager.DetectKeyError(data); needsKey {
				s.logger.Printf("Missing key detected for %s: %s", cachePath, keyID)
				// Auto-fetch key if enabled
				if s.cfg.KeyManagement.AutoRetrieve {
					if err := keyManager.FetchKey(keyID); err != nil {
						s.logger.Printf("Failed to fetch key %s: %v", keyID, err)
					} else {
						// Refresh release data with new key
						if err := s.backend.RefreshReleaseData(cachePath); err != nil {
							s.logger.Printf("Failed to refresh release data for %s: %v", cachePath, err)
						}
					}
				}
			}
		}
	}

	// Store in cache with appropriate TTL
	var cacheTTL time.Duration
	if isIndex {
		cacheTTL, _ = s.cfg.GetCacheTTL("index")
	} else {
		cacheTTL, _ = s.cfg.GetCacheTTL("package")
	}

	if cacheTTL > 0 {
		err = s.cache.PutWithExpiration(cachePath, data, cacheTTL)
	} else {
		err = s.cache.Put(cachePath, data)
	}

	if err != nil {
		s.logger.Printf("Cache storage error for %s: %v", cachePath, err)
		// We still continue to serve the file even if caching fails
	}

	// Process index files for prefetching
	if isIndex {
		// Process different index file types
		if strings.Contains(cachePath, "Release") {
			s.backend.ProcessReleaseFile(repo, cachePath, data)
		} else if strings.Contains(cachePath, "Packages") {
			s.backend.ProcessPackagesFile(repo, cachePath, data)
		}
	}

	// Update metrics
	s.metrics.RecordBytesServed(int64(len(data)))
	s.metrics.SetLastClientIP(clientIP)
	s.metrics.SetLastFileSize(int64(len(data)))

	// Calculate modified time for headers (use current time for fresh content)
	lastModified := time.Now()

	// Serve the file
	w.Header().Set("Content-Type", GetContentType(cachePath))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	http.ServeContent(w, r, filepath.Base(cachePath), lastModified, strings.NewReader(string(data)))

	// Record request completion time
	duration := time.Since(startTime)
	s.metrics.RecordRequest(requestPath, duration, clientIP, recorder.packageName)
}

// handleConditionalRequest handles If-Modified-Since requests
//
// This method checks if a requested resource has been modified since
// the time specified in the If-Modified-Since header.
//
// Parameters:
//   - w: HTTP response writer
//   - r: HTTP request
//   - path: Path of the requested resource
//
// Returns:
//   - Boolean indicating whether the request was handled (true) or should continue (false)
func (s *Server) handleConditionalRequest(w http.ResponseWriter, r *http.Request, path string) bool {
	ifModifiedSince := r.Header.Get("If-Modified-Since")
	if ifModifiedSince == "" {
		return false
	}

	ifModifiedSinceTime, err := time.Parse(time.RFC1123, ifModifiedSince)
	if err != nil {
		return false
	}

	// Get last modified time from cache
	if !s.cache.Exists(path) {
		return false
	}

	lastModified := s.cache.GetLastModified(path)

	// If file hasn't been modified, return 304
	if !lastModified.After(ifModifiedSinceTime) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}

	return false
}

// handleHighMemoryPressure handles high memory pressure situations
//
// This method takes actions to reduce memory usage when the system
// is under memory pressure, such as forcing garbage collection and
// clearing caches.
//
// Parameters:
//   - pressure: Memory pressure level from 0.0 to 1.0
func (s *Server) handleHighMemoryPressure(pressure float64) {
	// Only take action if pressure is significant
	if pressure < 0.7 {
		return
	}

	s.logger.Printf("Handling high memory pressure: %.2f", pressure)

	// Force GC at high pressure
	if pressure > 0.8 {
		runtime.GC()
	}

	// Critical pressure - take more drastic actions
	if pressure > 0.9 {
		s.logger.Printf("Critical memory pressure (%.2f) - clearing caches", pressure)

		// Clear package mapper cache
		if s.packageMapper != nil {
			s.packageMapper.ClearCache()
		}

		// Force cleanup of prefetcher
		if s.backend != nil {
			s.backend.ForceCleanupPrefetcher()
		}

		// Run another GC after clearing caches
		runtime.GC()
	}
}

// Write implements http.ResponseWriter.Write for ResponseRecorder
//
// This method captures the number of bytes written to the response
// for metrics collection.
//
// Parameters:
//   - data: Bytes to write to the response
//
// Returns:
//   - Number of bytes written
//   - Any error that occurred
func (r *ResponseRecorder) Write(data []byte) (int, error) {
	// Set default status if WriteHeader wasn't called
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}

	// Write data and track bytes written
	n, err := r.ResponseWriter.Write(data)
	r.bytesWritten += int64(n)
	return n, err
}

// WriteHeader implements http.ResponseWriter.WriteHeader for ResponseRecorder
//
// This method captures the HTTP status code of the response
// for metrics collection.
//
// Parameters:
//   - statusCode: HTTP status code to set
func (r *ResponseRecorder) WriteHeader(statusCode int) {
	if r.wroteHeader {
		return
	}
	r.ResponseWriter.WriteHeader(statusCode)
	r.status = statusCode
	r.wroteHeader = true
}

// Status returns the HTTP status code
//
// This method provides access to the status code set by WriteHeader.
//
// Returns:
//   - The HTTP status code
func (r *ResponseRecorder) Status() int {
	return r.status
}

// BytesWritten returns the number of bytes written
//
// This method provides access to the count of bytes written to the response.
//
// Returns:
//   - The number of bytes written
func (r *ResponseRecorder) BytesWritten() int64 {
	return r.bytesWritten
}

// GetContentType determines the content type based on file extension
//
// This function returns the appropriate MIME type for a given file path
// based on its extension, focused on package repository file types.
//
// Parameters:
//   - path: The file path to analyze
//
// Returns:
//   - The MIME content type as a string
func GetContentType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".deb", ".ddeb":
		return "application/vnd.debian.binary-package"
	case ".gz":
		return "application/gzip"
	case ".xz":
		return "application/x-xz"
	case ".bz2":
		return "application/x-bzip2"
	case ".dsc":
		return "text/plain"
	case ".asc":
		return "text/plain"
	case ".gpg":
		return "application/pgp-signature"
	default:
		// Default to binary for unknown types
		return "application/octet-stream"
	}
}

// extractClientIP extracts the client IP from a request
//
// This function gets the client IP address from an HTTP request,
// considering common proxy headers and fallbacks to RemoteAddr.
//
// Parameters:
//   - r: The HTTP request to extract IP from
//
// Returns:
//   - The client IP address as a string
func extractClientIP(r *http.Request) string {
	// Check X-Forwarded-For header first (for proxies)
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		// X-Forwarded-For can contain multiple IPs, use the first one
		ips := strings.Split(forwarded, ",")
		return strings.TrimSpace(ips[0])
	}

	// Check X-Real-IP header
	realIP := r.Header.Get("X-Real-IP")
	if realIP != "" {
		return realIP
	}

	// Otherwise use RemoteAddr
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// If no port in the address, use as is
		return r.RemoteAddr
	}

	return ip
}

// wrapWithMetrics wraps a handler with metrics collection
//
// This method adds metrics collection to an HTTP handler, capturing
// request timing, status codes, and other metrics data.
//
// Parameters:
//   - handler: The HTTP handler function to wrap
//
// Returns:
//   - Wrapped HTTP handler function with metrics collection
func (s *Server) wrapWithMetrics(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()
		clientIP := extractClientIP(r)

		// Create response recorder to capture metrics
		recorder := &ResponseRecorder{
			ResponseWriter: w,
			startTime:      startTime,
			requestPath:    r.URL.Path,
			clientIP:       clientIP,
			metrics:        s.metrics,
			packageMapper:  s.packageMapper,
		}

		// Call the wrapped handler with our recorder
		handler(recorder, r)

		// Record metrics after handler completes
		duration := time.Since(startTime)
		s.metrics.RecordRequest(r.URL.Path, duration, clientIP, recorder.packageName)
	}
}

// IsIndexFile checks if a file path is a repository index file
//
// This function identifies common repository index files based on
// their path and name patterns.
//
// Parameters:
//   - path: The file path to check
//
// Returns:
//   - Boolean indicating if the file is an index file
func IsIndexFile(path string) bool {
	// Check for common index file patterns
	return strings.Contains(path, "/dists/") && (strings.HasSuffix(path, "Release") ||
		strings.HasSuffix(path, "Release.gpg") ||
		strings.HasSuffix(path, "InRelease") ||
		strings.Contains(path, "Packages") ||
		strings.Contains(path, "Sources") ||
		strings.Contains(path, "Contents") ||
		strings.Contains(path, "Translation-"))
}

// byteCountSI formats a byte count in SI units (KB, MB, GB)
//
// This function converts a raw byte count into a human-readable string
// with appropriate size unit suffix.
//
// Parameters:
//   - b: Number of bytes
//
// Returns:
//   - Formatted string with size units
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
