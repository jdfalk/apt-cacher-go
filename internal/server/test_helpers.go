package server

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/cache"
	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/mapper"
	"github.com/jdfalk/apt-cacher-go/internal/metrics"
	"github.com/jdfalk/apt-cacher-go/internal/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestServerFixture contains all components needed for testing the server
type TestServerFixture struct {
	Server        *Server
	Config        *config.Config
	Backend       *mocks.MockBackendManager
	Cache         *mocks.MockCache
	Mapper        *mocks.MockPathMapper
	PackageMapper *mocks.MockPackageMapper
	Metrics       *mocks.MockMetricsCollector
	KeyManager    *mocks.MockKeyManager
	MemoryMonitor *mocks.MockMemoryMonitor
	TempDir       string
	Cleanup       func()
}

// NewTestServerFixture creates a new test fixture with mocked components
func NewTestServerFixture(t *testing.T) *TestServerFixture {
	// Create temporary directory for testing
	tempDir, err := os.MkdirTemp("", "apt-cacher-test")
	require.NoError(t, err)

	// Create mock components
	mockBackend := new(mocks.MockBackendManager)
	mockCache := new(mocks.MockCache)
	mockMapper := new(mocks.MockPathMapper)
	mockPkgMapper := new(mocks.MockPackageMapper)
	mockMetrics := new(mocks.MockMetricsCollector)
	mockKeyManager := new(mocks.MockKeyManager)
	mockMemMonitor := new(mocks.MockMemoryMonitor)

	// Create configuration
	cfg := &config.Config{
		CacheDir:      tempDir,
		ListenAddress: "127.0.0.1",
		Port:          8080,
		AdminPort:     8081,
		AdminAuth:     false,
		Backends: []config.Backend{
			{Name: "test-repo", URL: "http://example.com/debian", Priority: 100},
		},
	}

	// Set up basic mock behavior
	mockBackend.On("KeyManager").Return(mockKeyManager).Maybe()
	mockBackend.On("ForceCleanupPrefetcher").Return(0).Maybe()
	mockBackend.On("PrefetchOnStartup", mock.Anything).Return(nil).Maybe()
	mockCache.On("GetStats").Return(cache.CacheStats{}).Maybe()
	// ADD THIS: Mock expectation for Close() which is called during server shutdown
	mockCache.On("Close").Return(nil).Maybe()
	mockMapper.On("MapPath", mock.Anything).Return(mapper.MappingResult{
		Repository: "test-repo",
		RemotePath: "path/to/file",
		CachePath:  "test-repo/path/to/file",
		IsIndex:    false,
	}, nil).Maybe()
	mockMetrics.On("GetStatistics").Return(metrics.Statistics{}).Maybe()
	mockMemMonitor.On("GetMemoryUsage").Return(map[string]any{
		"allocated_mb":    float64(100),
		"system_mb":       float64(200),
		"pressure":        0,
		"memory_pressure": 0.75, // Add this field to match test expectations
	}).Maybe()

	// ADD THIS: Mock expectation for Start() method which is being called in New()
	mockMemMonitor.On("Start").Return().Maybe()
	// ADD THIS: Mock expectation for Stop() which may be called during cleanup
	mockMemMonitor.On("Stop").Return().Maybe()

	// Create server with mocks
	server, err := New(cfg, ServerOptions{
		Version:          "test-version",
		Logger:           log.New(os.Stdout, "TEST: ", log.LstdFlags).Writer(),
		BackendManager:   mockBackend,
		Cache:            mockCache,
		PathMapper:       mockMapper,
		PackageMapper:    mockPkgMapper,
		MetricsCollector: mockMetrics,
		MemoryMonitor:    mockMemMonitor,
	})
	require.NoError(t, err)

	// Create cleanup function
	cleanup := func() {
		// Add proper shutdown with timeout to prevent deadlocks
		doneChan := make(chan struct{})
		go func() {
			defer close(doneChan)
			if err := server.Shutdown(); err != nil {
				t.Logf("Error shutting down server: %v", err)
			}
		}()

		// Add timeout to prevent test hanging
		select {
		case <-doneChan:
			// Shutdown completed successfully
		case <-time.After(2 * time.Second):
			t.Log("Warning: Server shutdown timed out")
		}

		os.RemoveAll(tempDir)
	}

	return &TestServerFixture{
		Server:        server,
		Config:        cfg,
		Backend:       mockBackend,
		Cache:         mockCache,
		Mapper:        mockMapper,
		PackageMapper: mockPkgMapper,
		Metrics:       mockMetrics,
		KeyManager:    mockKeyManager,
		MemoryMonitor: mockMemMonitor,
		TempDir:       tempDir,
		Cleanup:       cleanup,
	}
}

// ExecuteRequest is a helper to execute a request against a handler
func ExecuteRequest(handler http.HandlerFunc, method, url string, body io.Reader) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest(method, url, body)
	w := httptest.NewRecorder()
	handler(w, req)
	return w, req
}

// ExecuteRequestWithAuth is a helper for requests with authentication
func ExecuteRequestWithAuth(handler http.HandlerFunc, method, url string, body io.Reader, username, password string) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest(method, url, body)
	req.SetBasicAuth(username, password)
	w := httptest.NewRecorder()
	handler(w, req)
	return w, req
}

// wrapTestHandler wraps a handler with middleware for testing
func wrapTestHandler(metrics *mocks.MockMetricsCollector, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		handler(w, r)
		duration := time.Since(start)
		metrics.RecordRequest(r.URL.Path, duration, "127.0.0.1", "test-package")
	}
}

// CreateTestFile creates a test file in the specified path
func CreateTestFile(t *testing.T, baseDir, relativePath string, content []byte) {
	fullPath := filepath.Join(baseDir, relativePath)
	dir := filepath.Dir(fullPath)

	// Create directories if they don't exist
	err := os.MkdirAll(dir, 0755)
	require.NoError(t, err)

	// Write the file
	err = os.WriteFile(fullPath, content, 0644)
	require.NoError(t, err)
}

// ResponseBodyToString reads the response body into a string
func ResponseBodyToString(resp *http.Response) (string, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	return string(body), nil
}

// CreateTestRequest creates an http.Request for testing
func CreateTestRequest(method, url string, body []byte) *http.Request {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, url, bodyReader)
	return req
}

// CreateTestServer creates a basic test server with minimal configuration
func CreateTestServer(t *testing.T) (*Server, string, func()) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "apt-cacher-test")
	require.NoError(t, err)

	// Create minimal config
	cfg := &config.Config{
		CacheDir:      tempDir,
		ListenAddress: "127.0.0.1",
		Port:          8080,
	}

	// Create logger
	logger := log.New(os.Stdout, "TEST: ", log.LstdFlags)

	// Create server with minimal options
	server, err := New(cfg, ServerOptions{
		Version: "test-version",
		Logger:  logger.Writer(),
	})
	require.NoError(t, err)

	// Cleanup function with timeout to prevent deadlocks
	cleanup := func() {
		doneChan := make(chan struct{})

		go func() {
			defer close(doneChan)
			if err := server.Shutdown(); err != nil {
				t.Logf("Error shutting down server: %v", err)
			}
		}()

		// Add timeout to prevent test hanging
		select {
		case <-doneChan:
			// Shutdown completed successfully
		case <-time.After(2 * time.Second):
			t.Log("Warning: Server shutdown timed out")
		}

		os.RemoveAll(tempDir)
	}

	return server, tempDir, cleanup
}
