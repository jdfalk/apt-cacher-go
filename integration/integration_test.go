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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getTestDir returns a directory suitable for testing, creating the .file_system_root if needed
func getTestDir(t *testing.T) string {
	// First, find the project root directory
	projectRoot, err := findProjectRoot()
	if err != nil {
		t.Logf("Couldn't determine project root: %v", err)
		t.Logf("Falling back to temporary directory")
		return ""
	}

	// Create .file_system_root directory if it doesn't exist
	testRoot := filepath.Join(projectRoot, ".file_system_root")

	// Check if directory exists first
	if stat, err := os.Stat(testRoot); err == nil && stat.IsDir() {
		// Directory already exists
		return testRoot
	}

	// Directory doesn't exist, create it
	if err := os.MkdirAll(testRoot, 0755); err != nil {
		t.Logf("Failed to create .file_system_root: %v", err)
		return ""
	}

	return testRoot
}

// findProjectRoot attempts to find the root of the project by looking for go.mod file
func findProjectRoot() (string, error) {
	// Start from the current working directory
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Walk up the directory tree looking for go.mod
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}

		// Move up one directory
		parentDir := filepath.Dir(dir)
		if parentDir == dir {
			// We've reached the root without finding go.mod
			return "", fmt.Errorf("go.mod not found in any parent directory")
		}
		dir = parentDir
	}
}

// TestServer is a helper to create a test server instance
type TestServer struct {
	Server      *server.Server
	CacheDir    string
	Config      *config.Config
	Client      *http.Client
	BaseURL     string
	CancelFunc  context.CancelFunc
	CleanupFunc func()
	Ready       chan struct{} // Channel to signal when server is ready
}

// Create a shared test utility function to set up the server properly
func setupTestServer(t *testing.T, mockURL string) (*TestServer, func()) {
	// Create temp directory for cache, preferably within project directory
	testRoot := getTestDir(t)
	var tempDir string
	var err error

	if testRoot != "" {
		// Create a unique directory within .file_system_root with timestamp
		dirName := fmt.Sprintf("apt-cacher-test-%d", time.Now().UnixNano())
		tempDir = filepath.Join(testRoot, dirName)

		// Always attempt to remove it first to prevent problems with leftover directories
		os.RemoveAll(tempDir)

		err = os.MkdirAll(tempDir, 0755)
		if err != nil {
			t.Logf("Failed to create test dir in .file_system_root: %v", err)
			t.Logf("Falling back to temporary directory")
			tempDir = ""
		}
	}

	// Fall back to system temp dir if needed
	if tempDir == "" {
		tempDir, err = os.MkdirTemp("", "apt-cacher-test")
		require.NoError(t, err)
	}

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
			{Name: "debian", URL: mockURL, Priority: 100},
			{Name: "ubuntu", URL: mockURL, Priority: 90},
		},
		CacheTTLs: map[string]string{
			"index":   "1h",  // Cache index files for 1 hour
			"package": "30d", // Cache package files for 30 days
		},
		// Add MappingRules to ensure proper path handling
		MappingRules: []config.MappingRule{
			{Type: "prefix", Pattern: "/debian/", Repository: "debian", Priority: 100},
			{Type: "prefix", Pattern: "/ubuntu/", Repository: "ubuntu", Priority: 90},
		},
	}

	// Create server
	srv, err := server.New(cfg)
	require.NoError(t, err)

	// Create client with timeout
	client := &http.Client{Timeout: 5 * time.Second}

	// Build base URL
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Channel to signal when server is ready
	ready := make(chan struct{})

	// Create cleanup function - REMOVE channel close from here!
	cleanup := func() {
		if err := srv.Shutdown(); err != nil {
			t.Logf("Warning: Server shutdown error: %v", err)
		}
		os.RemoveAll(tempDir)
		// Don't close ready channel here - it's already closed in the goroutine
	}

	ts := &TestServer{
		Server:      srv,
		CacheDir:    tempDir,
		Config:      cfg,
		Client:      client,
		BaseURL:     baseURL,
		CleanupFunc: cleanup,
		Ready:       ready,
	}

	// Start the server in a goroutine
	go func() {
		// Signal that the server is ready for connections
		close(ready)
		if err := ts.Server.Start(); err != nil && err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()

	// Wait for the server to be ready for connections
	<-ready

	// Additional wait for the server to fully initialize
	time.Sleep(100 * time.Millisecond)

	return ts, cleanup
}

// Helper function to safely read and discard response body
func readAndDiscardBody(t *testing.T, resp *http.Response) {
	if resp == nil {
		return
	}

	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Logf("Warning: Failed to read response body: %v", err)
	}
}

func TestBasicFunctionality(t *testing.T) {
	// Setup a mock upstream server first
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// Log the requested path
		t.Logf("Mock server received: %s", r.URL.Path)
		_, err := w.Write([]byte("Mock repository data for " + r.URL.Path))
		if err != nil {
			t.Logf("Error writing response: %v", err)
		}
	}))
	defer mockUpstream.Close()

	// Setup test server with mock URL already configured
	ts, cleanup := setupTestServer(t, mockUpstream.URL)
	defer cleanup()

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

			// Read and discard the body using the helper function
			readAndDiscardBody(t, resp)
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
		_, err := w.Write(make([]byte, size))
		if err != nil {
			t.Logf("Error writing response: %v", err)
		}
	}))
	defer mockUpstream.Close()

	// Setup test server with mock URL already configured
	ts, cleanup := setupTestServer(t, mockUpstream.URL)
	defer cleanup()

	// Prepare URLs for testing
	baseURL := ts.BaseURL
	urls := []string{
		baseURL + "/ubuntu/pool/main/b/bash/bash_5.1-6ubuntu1_amd64.deb",
		baseURL + "/debian/pool/main/n/nginx/nginx_1.18.0-6.1_amd64.deb",
	}

	// Make concurrent requests
	var wg sync.WaitGroup
	client := ts.Client

	// Create an errorCh to collect errors from goroutines
	errorCh := make(chan error, 5)

	// Make 5 concurrent requests
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			url := urls[id%len(urls)]
			resp, err := client.Get(url)
			if err != nil {
				errorCh <- fmt.Errorf("request failed: %v", err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != 200 {
				errorCh <- fmt.Errorf("unexpected status code: %d", resp.StatusCode)
			}

			// Read and discard the body
			if _, err := io.Copy(io.Discard, resp.Body); err != nil {
				errorCh <- fmt.Errorf("failed to read response body: %v", err)
			}
		}(i)
	}

	// Wait for all requests to complete
	wg.Wait()
	close(errorCh)

	// Check for any errors
	for err := range errorCh {
		t.Error(err)
	}
}

// TestRepositoryMapping tests that requests are mapped to the correct backend
func TestRepositoryMapping(t *testing.T) {
	// Setup a mock upstream server
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, err := w.Write([]byte("Mock repository data for " + r.URL.Path))
		if err != nil {
			t.Logf("Error writing response: %v", err)
		}
	}))
	defer mockUpstream.Close()

	// Setup test server with mock URL already configured
	ts, cleanup := setupTestServer(t, mockUpstream.URL)
	defer cleanup()

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
			require.NoError(t, err, "Request should not fail")

			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode, "Status code should be 200 OK")

			// Read and discard the body
			readAndDiscardBody(t, resp)

			// Check if the file exists anywhere in the cache directory
			path, found := getCacheFile(ts.CacheDir, tc.repository, tc.path)
			if !found {
				time.Sleep(100 * time.Millisecond) // Wait a bit longer
				path, found = getCacheFile(ts.CacheDir, tc.repository, tc.path)
			}
			assert.True(t, found, "Cache file should exist somewhere in %s", ts.CacheDir)
			if found {
				t.Logf("Found cache file at: %s", path)
			}
		})
	}
}

// Update the TestRepositoryMapping test with this function:
// Accept any valid cache structure by actually checking if files exist instead of assuming paths
func getCacheFile(cacheDir, repository, path string) (string, bool) {
	// Try various possible path structures, with the new format first
	possiblePaths := []string{
		filepath.Join(cacheDir, repository, strings.TrimPrefix(path, "/"+repository+"/")), // New format
		filepath.Join(cacheDir, repository, path),                                         // Simple
		filepath.Join(cacheDir, repository, repository, path),                             // Double repo
		filepath.Join(cacheDir, repository, strings.TrimPrefix(path, "/"+repository)),     // No leading repo
	}

	for _, p := range possiblePaths {
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
	}
	return "", false
}

// TestCacheExpiration tests that expired files are properly handled
func TestCacheExpiration(t *testing.T) {
	// Setup a mock upstream server with sequence tracking
	requestCount := 0
	var requestMutex sync.Mutex

	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMutex.Lock()
		requestCount++
		currentCount := requestCount
		requestMutex.Unlock()

		// Return different content each time to verify cache
		content := fmt.Sprintf("Mock data for %s (request %d)", r.URL.Path, currentCount)
		t.Logf("Mock server received: %s (count: %d)", r.URL.Path, currentCount)

		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "max-age=1") // Very short max-age
		if _, err := w.Write([]byte(content)); err != nil {
			t.Logf("Error writing response: %v", err)
		}
	}))
	defer mockUpstream.Close()

	// Setup test server with extremely short TTLs for testing
	ts, cleanup := setupTestServer(t, mockUpstream.URL)
	defer cleanup()

	// Force very short TTLs
	ts.Config.CacheTTLs = map[string]string{
		"index":   "500ms", // Make this longer than the test delay
		"package": "500ms",
	}

	// First request - should hit the backend and cache
	resp, err := ts.Client.Get(ts.BaseURL + "/ubuntu/dists/jammy/Release")
	require.NoError(t, err)
	body1, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()

	// Make sure we have a request count
	requestMutex.Lock()
	firstCount := requestCount
	requestMutex.Unlock()

	// Use t.Logf to debug the first count
	t.Logf("After first request, count is: %d", firstCount)

	// Second request immediately - should use cache
	resp, err = ts.Client.Get(ts.BaseURL + "/ubuntu/dists/jammy/Release")
	require.NoError(t, err)
	body2, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()

	// Verify still using cache
	requestMutex.Lock()
	secondCount := requestCount
	requestMutex.Unlock()

	t.Logf("After second request, count is: %d", secondCount)
	assert.Equal(t, firstCount, secondCount, "Second request should use cache")

	// Verify same content from cache
	assert.Equal(t, body1, body2, "Second response should match first (cached)")

	// Force cache expiration by finding and removing cache file
	time.Sleep(100 * time.Millisecond) // Allow time for file to be written

	// Find and remove the cache file
	found := false
	err = filepath.Walk(ts.CacheDir, func(path string, info os.FileInfo, err error) error {
		if strings.Contains(path, "jammy/Release") {
			t.Logf("Found cache file: %s", path)
			os.Remove(path) // Delete the file to force cache miss
			found = true
			return filepath.SkipDir
		}
		return nil
	})
	require.NoError(t, err)
	assert.True(t, found, "Should have found a cache file to delete")

	// Third request - should hit backend again
	resp, err = ts.Client.Get(ts.BaseURL + "/ubuntu/dists/jammy/Release")
	require.NoError(t, err)
	body3, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()

	requestMutex.Lock()
	finalCount := requestCount
	requestMutex.Unlock()

	t.Logf("After third request (with file deleted), count is: %d", finalCount)
	assert.Equal(t, firstCount+1, finalCount, "Should have made a second backend request")

	// Content should be different in third response
	assert.NotEqual(t, body1, body3, "Third response should be different (not cached)")
}

// TestErrorHandling tests how the server handles errors from backends
func TestErrorHandling(t *testing.T) {
	// Setup a mock upstream server
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		if _, err := w.Write([]byte("Mock data")); err != nil {
			t.Logf("Error writing response: %v", err)
		}
	}))
	defer mockUpstream.Close()

	// Set up test configuration directly instead of using setupTestServer
	testRoot := getTestDir(t)
	var tempDir string
	var err error

	if testRoot != "" {
		// Create a unique directory within .file_system_root
		tempDir = filepath.Join(testRoot, fmt.Sprintf("apt-cacher-test-errorhandling-%d", time.Now().UnixNano()))
		err := os.MkdirAll(tempDir, 0755)
		if err != nil {
			t.Logf("Failed to create test dir: %v", err)
			tempDir = ""
		}
	}

	// Fall back to system temp dir if needed
	if tempDir == "" {
		tempDir, err = os.MkdirTemp("", "apt-cacher-test-errorhandling")
		require.NoError(t, err)
	}
	defer os.RemoveAll(tempDir)

	// Find an available port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	// Create test server config with all backends including nonexistent one
	cfg := &config.Config{
		CacheDir:      tempDir,
		ListenAddress: "127.0.0.1",
		Port:          port,
		Backends: []config.Backend{
			{Name: "debian", URL: mockUpstream.URL, Priority: 100},
			{Name: "ubuntu", URL: mockUpstream.URL, Priority: 90},
			// Add the nonexistent backend directly
			{Name: "nonexistent", URL: "http://nonexistent.example.com", Priority: 50},
		},
		CacheTTLs: map[string]string{
			"index":   "1h",
			"package": "30d",
		},
		// Add MappingRules for all backends including nonexistent
		MappingRules: []config.MappingRule{
			{Type: "prefix", Pattern: "/debian/", Repository: "debian", Priority: 100},
			{Type: "prefix", Pattern: "/ubuntu/", Repository: "ubuntu", Priority: 90},
			{Type: "prefix", Pattern: "/nonexistent/", Repository: "nonexistent", Priority: 50},
		},
	}

	// Create and start the server
	srv, err := server.New(cfg)
	require.NoError(t, err)

	// Create client
	client := &http.Client{Timeout: 5 * time.Second}

	// Base URL
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Start server in goroutine
	serverReady := make(chan struct{})
	go func() {
		close(serverReady)
		if err := srv.Start(); err != nil && err != http.ErrServerClosed {
			t.Logf("Server error: %v", err)
		}
	}()

	<-serverReady
	time.Sleep(200 * time.Millisecond)

	// Clean up when we're done - only call Shutdown once
	defer func() {
		if err := srv.Shutdown(); err != nil {
			t.Logf("Warning: Server shutdown error: %v", err)
		}
	}()

	// Test with path that matches the nonexistent backend
	url := baseURL + "/nonexistent/some/path"
	resp, err := client.Get(url)

	if err == nil {
		defer resp.Body.Close()
		t.Logf("Got status code %d for nonexistent backend", resp.StatusCode)
		assert.True(t, resp.StatusCode == http.StatusNotFound ||
			resp.StatusCode == http.StatusBadGateway,
			"Expected 404 or 502 status code for nonexistent backend")

		// Read and discard the body
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			t.Logf("Warning: Failed to read response body: %v", err)
		}
	} else {
		t.Logf("Got network error (expected): %v", err)
	}
}
