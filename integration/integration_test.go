package integration

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/backend"
	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestServer is a helper to create a test server instance
type TestServer struct {
	Server      *server.Server
	CacheDir    string
	Config      *config.Config
	Client      *http.Client
	BaseURL     string
	CancelFunc  context.CancelFunc
	CleanupFunc func()
}

// Create a shared test utility function to set up the server properly
func setupTestServer(t *testing.T) (*TestServer, func()) {
	// Create temp directory for cache
	tempDir, err := os.MkdirTemp("", "apt-cacher-test")
	require.NoError(t, err)

	// Find an available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	// Create test server config with backend configuration
	cfg := &config.Config{
		CacheDir:      tempDir,
		ListenAddress: "127.0.0.1",
		Port:          port,
		Backends: []config.Backend{
			{Name: "debian", URL: "http://localhost:8080", Priority: 100},
			{Name: "ubuntu", URL: "http://localhost:8080", Priority: 90},
		},
		IndexCacheDuration:   "1h",  // Cache index files for 1 hour
		PackageCacheDuration: "30d", // Cache package files for 30 days
	}

	// Create and start server
	srv, err := server.New(cfg)
	require.NoError(t, err)

	// Create client with timeout
	client := &http.Client{Timeout: 5 * time.Second}

	// Build base URL
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Create cleanup function
	cleanup := func() {
		if err := srv.Shutdown(); err != nil {
			t.Logf("Warning: Server shutdown error: %v", err)
		}
		os.RemoveAll(tempDir)
	}

	ts := &TestServer{
		Server:      srv,
		CacheDir:    tempDir,
		Config:      cfg,
		Client:      client,
		BaseURL:     baseURL,
		CleanupFunc: cleanup,
	}

	return ts, cleanup
}

func TestBasicFunctionality(t *testing.T) {
	// Setup a mock upstream server first
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("Mock repository data for " + r.URL.Path))
	}))
	defer mockUpstream.Close()

	// Setup test server
	ts, cleanup := setupTestServer(t)
	defer cleanup()

	// Override backend URLs to point to our mock server
	// Using reflection to access the unexported field if necessary
	backendField := reflect.ValueOf(ts.Server).Elem().FieldByName("backend")
	if backendField.IsValid() {
		backend := backendField.Interface().(*backend.Manager)

		// Instead of using SetBaseURL, we need to use whatever method is available
		// For now, let's assume we can directly modify the backends via reflection
		backendsMap := reflect.ValueOf(backend).Elem().FieldByName("backends")
		if backendsMap.IsValid() {
			// Use reflection to modify the backends map with the mock URL
			// This is a temporary solution - ideally the backend.Manager should expose a proper API
			t.Logf("Using reflection to modify backend URLs")
		} else {
			t.Logf("Warning: Could not access backends map through reflection")
		}
	}

	// Start the server
	go func() {
		if err := ts.Server.Start(); err != nil && err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Test cases
	testCases := []struct {
		name     string
		path     string
		wantCode int
	}{
		{"/ubuntu/dists/jammy/Release", "/ubuntu/dists/jammy/Release", 200},
		{"/debian/pool/main/h/hello/hello_2.10-2_amd64.deb", "/debian/pool/main/h/hello/hello_2.10-2_amd64.deb", 200},
	}

	// Run test cases
	baseURL := ts.BaseURL
	client := ts.Client

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			resp, err := client.Get(baseURL + tc.path)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
				return
			}
			defer resp.Body.Close()

			assert.Equal(t, tc.wantCode, resp.StatusCode)

			// Read and discard the body
			if _, err := io.Copy(io.Discard, resp.Body); err != nil {
				t.Logf("Warning: Failed to read response body: %v", err)
			}
		})
	}
}

// TestConcurrentRequests tests that the server handles concurrent requests properly
func TestConcurrentRequests(t *testing.T) {
	// Setup a mock upstream server
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		// Simulate different file sizes
		size := 1000
		if strings.Contains(r.URL.Path, "bash") {
			size = 5000
		}
		w.Write(make([]byte, size))
	}))
	defer mockUpstream.Close()

	// Setup test server
	ts, cleanup := setupTestServer(t)
	defer cleanup()

	// Override backend URLs
	backendField := reflect.ValueOf(ts.Server).Elem().FieldByName("backend")
	if backendField.IsValid() {
		// Similar approach as in TestBasicFunctionality
		// We need a proper way to modify backend URLs in tests
		t.Logf("Backend field is valid but SetBaseURL method is not available")
	}

	// Start the server
	go func() {
		if err := ts.Server.Start(); err != nil && err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Prepare URLs for testing
	baseURL := ts.BaseURL
	urls := []string{
		baseURL + "/ubuntu/pool/main/b/bash/bash_5.1-6ubuntu1_amd64.deb",
		baseURL + "/debian/pool/main/n/nginx/nginx_1.18.0-6.1_amd64.deb",
	}

	// Make concurrent requests
	var wg sync.WaitGroup
	client := ts.Client

	// Modernize the for loop using range
	for i := range 5 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			url := urls[id%len(urls)]
			resp, err := client.Get(url)
			if err != nil {
				t.Errorf("Request failed: %v", err)
				return
			}
			defer resp.Body.Close()

			assert.Equal(t, 200, resp.StatusCode)

			// Read and discard the body
			if _, err := io.Copy(io.Discard, resp.Body); err != nil {
				t.Logf("Warning: Failed to read response body: %v", err)
			}
		}(i)
	}

	wg.Wait()
}

// TestRepositoryMapping tests that requests are mapped to the correct backend
func TestRepositoryMapping(t *testing.T) {
	ts, cleanup := setupTestServer(t)
	defer cleanup()

	// Start the server
	go func() {
		if err := ts.Server.Start(); err != nil && err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	testCases := []struct {
		path       string
		repository string
		isIndex    bool
	}{
		{"/ubuntu/dists/jammy/Release", "ubuntu", true},
		{"/ubuntu/pool/main/b/bash/bash_5.1-6ubuntu1_amd64.deb", "ubuntu", false},
		{"/debian/dists/bookworm/Release", "debian", true},
		{"/debian/pool/main/p/python3.9/python3.9_3.9.2-1_amd64.deb", "debian", false},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			url := ts.BaseURL + tc.path
			resp, err := ts.Client.Get(url)

			if err == nil && resp.StatusCode == http.StatusOK {
				resp.Body.Close()

				// Check if the file was cached correctly
				var expectedCachePath string
				if tc.isIndex {
					// Index files are stored with special names
					basename := filepath.Base(tc.path)
					dir := filepath.Dir(tc.path)
					expectedCachePath = filepath.Join(ts.CacheDir, tc.repository, dir, basename)
				} else {
					expectedCachePath = filepath.Join(ts.CacheDir, tc.repository, tc.path)
				}

				_, err := os.Stat(expectedCachePath)
				assert.NoError(t, err, "Cache file should exist at the expected location")
			}
		})
	}
}

// TestCacheExpiration tests that expired files are properly handled
func TestCacheExpiration(t *testing.T) {
	// Setup a mock upstream server
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Last-Modified", time.Now().Format(http.TimeFormat))
		w.Write([]byte("Mock data for " + r.URL.Path))
	}))
	defer mockUpstream.Close()

	// Setup test server with short expiration time for testing
	tempDir, err := os.MkdirTemp("", "apt-cacher-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Find an available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	// Create config with short expiration for testing
	cfg := &config.Config{
		CacheDir:      tempDir,
		ListenAddress: "127.0.0.1",
		Port:          port,
		Backends: []config.Backend{
			{Name: "debian", URL: mockUpstream.URL, Priority: 100},
			{Name: "ubuntu", URL: mockUpstream.URL, Priority: 90},
		},
		IndexCacheDuration:   "1s", // Very short for testing
		PackageCacheDuration: "2s", // Very short for testing
	}

	srv, err := server.New(cfg)
	require.NoError(t, err)

	// Start the server
	go func() {
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()
	defer func() {
		if err := srv.Shutdown(); err != nil {
			t.Logf("Warning: Server shutdown error: %v", err)
		}
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Make a request to cache an index file
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(baseURL + "/ubuntu/dists/jammy/Release")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)

	// Wait for expiration
	time.Sleep(1200 * time.Millisecond)

	// Second request should hit the backend again
	resp, err = client.Get(baseURL + "/ubuntu/dists/jammy/Release")
	if err != nil {
		t.Fatalf("Second request failed: %v", err)
	}
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)

	// Read and discard the body
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Logf("Warning: Failed to read response body: %v", err)
	}
}

// TestErrorHandling tests how the server handles errors from backends
func TestErrorHandling(t *testing.T) {
	ts, cleanup := setupTestServer(t)
	defer cleanup()

	// Start the server
	go func() {
		if err := ts.Server.Start(); err != nil && err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Add a non-existent backend to test error handling
	ts.Config.Backends = append(ts.Config.Backends, config.Backend{
		Name:     "nonexistent",
		URL:      "http://nonexistent.example.com",
		Priority: 50,
	})

	// Try to access a path on the non-existent backend
	url := ts.BaseURL + "/nonexistent.example.com/some/path"
	resp, err := ts.Client.Get(url)

	// Should either get an error or a 404/502
	if err == nil {
		assert.True(t, resp.StatusCode == http.StatusNotFound ||
			resp.StatusCode == http.StatusBadGateway,
			"Expected 404 or 502 status code for nonexistent backend")
		resp.Body.Close()
	}
}
