package integration

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// setupTestServer creates a test server with temporary directories
func setupTestServer(t *testing.T) *TestServer {
	// Create temp directory for cache
	cacheDir, err := os.MkdirTemp("", "apt-cacher-integration")
	require.NoError(t, err)

	// Create test config
	cfg := &config.Config{
		ListenAddress:          "127.0.0.1",
		Port:                   0, // Use a random port
		CacheDir:               cacheDir,
		MaxCacheSize:           1024, // 1GB
		MaxConcurrentDownloads: 10,
		Backends: []config.Backend{
			{
				Name:     "ubuntu-archive",
				URL:      "http://archive.ubuntu.com/ubuntu",
				Priority: 100,
			},
			{
				Name:     "debian",
				URL:      "http://deb.debian.org/debian",
				Priority: 90,
			},
			{
				Name:     "debian-security",
				URL:      "http://security.debian.org/debian-security",
				Priority: 80,
			},
		},
	}

	// Create and start server
	srv, err := server.New(cfg)
	require.NoError(t, err)

	// Create a context with cancel to stop the server
	_, cancel := context.WithCancel(context.Background())

	// Start server in a goroutine
	go func() {
		srv.Start()
	}()

	// Create cleanup function
	cleanup := func() {
		cancel()
		srv.Shutdown()
		os.RemoveAll(cacheDir)
	}

	// Wait for server to start and get the port
	time.Sleep(100 * time.Millisecond)

	// Create a client
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	ts := &TestServer{
		Server:      srv,
		CacheDir:    cacheDir,
		Config:      cfg,
		Client:      client,
		BaseURL:     fmt.Sprintf("http://127.0.0.1:%d", srv.Port()),
		CancelFunc:  cancel,
		CleanupFunc: cleanup,
	}

	return ts
}

// TestBasicFunctionality tests the basic cache functionality
func TestBasicFunctionality(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.CleanupFunc()

	// Test package paths to fetch
	testPaths := []struct {
		path       string
		repository string
		statusCode int
	}{
		{"/ubuntu/dists/jammy/Release", "ubuntu-archive", http.StatusOK},
		{"/debian/dists/bookworm/Release", "debian", http.StatusOK},
		{"/ubuntu/pool/main/b/bash/bash_5.1-6ubuntu1_amd64.deb", "ubuntu-archive", http.StatusOK},
		{"/nonexistent/path", "", http.StatusNotFound},
	}

	for _, tp := range testPaths {
		t.Run(tp.path, func(t *testing.T) {
			// First request (cache miss)
			url := ts.BaseURL + tp.path
			resp, err := ts.Client.Get(url)

			if tp.statusCode == http.StatusOK {
				assert.NoError(t, err)
				assert.Equal(t, tp.statusCode, resp.StatusCode)

				// Read and discard the body
				_, err = io.Copy(io.Discard, resp.Body)
				assert.NoError(t, err)
				resp.Body.Close()

				// Verify the file exists in cache
				cachePath := filepath.Join(ts.CacheDir, tp.repository, tp.path)
				_, err = os.Stat(cachePath)
				assert.NoError(t, err, "Cache file should exist")

				// Second request (cache hit)
				resp2, err := ts.Client.Get(url)
				assert.NoError(t, err)
				assert.Equal(t, tp.statusCode, resp2.StatusCode)
				resp2.Body.Close()
			} else {
				if err == nil {
					assert.Equal(t, tp.statusCode, resp.StatusCode)
					resp.Body.Close()
				}
			}
		})
	}
}

// TestConcurrentRequests tests that the server handles concurrent requests properly
func TestConcurrentRequests(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.CleanupFunc()

	// Common package path that should be in Ubuntu repositories
	path := "/ubuntu/pool/main/b/bash/bash_5.1-6ubuntu1_amd64.deb"
	url := ts.BaseURL + path

	// Do an initial request to ensure it's cached
	resp, err := ts.Client.Get(url)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// Make concurrent requests
	concurrency := 10
	errChan := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			resp, err := ts.Client.Get(url)
			if err != nil {
				errChan <- err
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				errChan <- fmt.Errorf("unexpected status: %d", resp.StatusCode)
				return
			}

			_, err = io.Copy(io.Discard, resp.Body)
			errChan <- err
		}()
	}

	// Collect results
	for i := 0; i < concurrency; i++ {
		err := <-errChan
		assert.NoError(t, err)
	}
}

// TestRepositoryMapping tests that requests are mapped to the correct backend
func TestRepositoryMapping(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.CleanupFunc()

	testCases := []struct {
		path       string
		repository string
		isIndex    bool
	}{
		{"/ubuntu/dists/jammy/Release", "ubuntu-archive", true},
		{"/ubuntu/pool/main/b/bash/bash_5.1-6ubuntu1_amd64.deb", "ubuntu-archive", false},
		{"/debian/dists/bookworm/Release", "debian", true},
		{"/debian/pool/main/p/python3.9/python3.9_3.9.2-1_amd64.deb", "debian", false},
		{"/debian-security/dists/bookworm-security/Release", "debian-security", true},
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
	ts := setupTestServer(t)
	defer ts.CleanupFunc()

	// Patch the cache to use a short expiration for testing
	path := "/ubuntu/dists/jammy/Release"
	url := ts.BaseURL + path

	// First request to cache the file
	resp, err := ts.Client.Get(url)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Find the cached file
	cachePath := filepath.Join(ts.CacheDir, "ubuntu-archive", path)

	// Modify the file's timestamp to make it appear old
	now := time.Now().Add(-24 * time.Hour)
	err = os.Chtimes(cachePath, now, now)
	require.NoError(t, err)

	// Request again - should trigger a refresh
	resp, err = ts.Client.Get(url)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Check that file was updated
	fileInfo, err := os.Stat(cachePath)
	require.NoError(t, err)
	assert.True(t, fileInfo.ModTime().After(now), "File should have been refreshed")
}

// TestErrorHandling tests how the server handles errors from backends
func TestErrorHandling(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.CleanupFunc()

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
