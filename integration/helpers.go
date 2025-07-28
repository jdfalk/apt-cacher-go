package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/mocks"
	"github.com/jdfalk/apt-cacher-go/internal/server"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// createTestServer creates a server instance with mock backend for integration testing
//
// This function creates a server instance with all necessary mock components for testing:
// - Sets up a mock backend manager with appropriate method expectations
// - Configures mock responses for backend requests
// - Creates a server with the provided configuration
// - Returns a cleanup function that properly shuts down the server
//
// Parameters:
//   - t: The testing.T instance for assertions and logging
//   - cfg: The server configuration to use
//
// Returns:
//   - A fully initialized server instance
//   - A context cancel function to stop the server
//   - A cleanup function that properly shuts down the server
func createTestServer(t *testing.T, cfg *config.Config) (*server.Server, context.CancelFunc, func()) {
	// Create a mock backend manager
	mockBackend := new(mocks.MockBackendManager)

	// Set up basic expectations for methods that will be called
	mockBackend.On("KeyManager").Return(nil).Maybe()
	mockBackend.On("ForceCleanupPrefetcher").Return(0).Maybe()
	mockBackend.On("RefreshReleaseData", mock.Anything).Return(nil).Maybe()
	mockBackend.On("PrefetchOnStartup", mock.Anything).Return().Maybe()

	// CRITICAL FIX: Setup ProcessReleaseFile expectation
	mockBackend.On("ProcessReleaseFile",
		mock.AnythingOfType("string"),
		mock.AnythingOfType("string"),
		mock.AnythingOfType("[]uint8")).Return().Maybe()

	// CRITICAL FIX: Setup ProcessPackagesFile expectation
	mockBackend.On("ProcessPackagesFile",
		mock.AnythingOfType("string"),
		mock.AnythingOfType("string"),
		mock.AnythingOfType("[]uint8")).Return().Maybe()

	// Setup repository-specific fetch responses
	for _, repo := range []string{"ubuntu", "debian", "docker"} {
		// FIX #1: Format content strings to match what the test is expecting
		mockBackend.On("Fetch", mock.MatchedBy(func(path string) bool {
			return strings.HasPrefix(path, repo+"/")
		})).Return(
			[]byte(fmt.Sprintf("Mock repository data for %s (timestamp: %d)",
				path, time.Now().UnixNano())),
			nil,
		).Maybe()
	}

	// FIX #2: Set up special handling for nonexistent repository
	mockBackend.On("Fetch", mock.MatchedBy(func(p string) bool {
		return strings.HasPrefix(p, "nonexistent/")
	})).Return(
		nil,
		errors.New("404 not found: nonexistent repository"),
	).Maybe()

	// Create the server with mock backend
	srv, err := server.New(cfg, server.ServerOptions{
		Version:        "integration-test",
		BackendManager: mockBackend,
	})
	require.NoError(t, err)

	// Create context for the server - don't cancel it immediately!
	ctx, cancelFunc := context.WithCancel(context.Background())

	// Start the server in a goroutine with the context
	go func() {
		if err := srv.StartWithContext(ctx); err != nil && err != context.Canceled {
			t.Logf("Server error: %v", err)
		}
	}()

	// Wait a moment for the server to actually start - increased from 300ms to 500ms
	time.Sleep(500 * time.Millisecond)

	// Prepare cleanup function that ensures proper server shutdown
	cleanup := func() {
		// First cancel the context
		cancelFunc()

		// Then explicitly shut down the server
		doneChan := make(chan struct{})
		go func() {
			defer close(doneChan)
			if err := srv.Shutdown(); err != nil {
				t.Logf("Error shutting down server: %v", err)
			}
		}()

		// Add timeout to prevent test hanging
		select {
		case <-doneChan:
			t.Log("Server cleanup completed successfully")
		case <-time.After(5 * time.Second):
			t.Log("Warning: Server shutdown timed out after 5 seconds")
		}
	}

	return srv, cancelFunc, cleanup
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// createServerConfig creates a server configuration for testing
//
// This function creates a temporary directory for cache files and initializes
// a server configuration with appropriate test settings, including backends
// for common repositories and mapping rules.
//
// Parameters:
//   - t: The testing.T instance for cleanup registration
//
// Returns:
//   - A fully initialized config.Config structure
//   - The path to the temporary cache directory
func createServerConfig(t *testing.T) (*config.Config, string) {
	// Create a temporary directory for the cache
	tempDir, err := os.MkdirTemp("", "apt-cacher-integration")
	require.NoError(t, err)

	// Register cleanup function to remove temp directory after test
	t.Cleanup(func() {
		os.RemoveAll(tempDir)
	})

	// Create a unique port based on process ID to avoid conflicts
	port := 8300 + (os.Getpid() % 1000)
	adminPort := 9310 + (os.Getpid() % 1000)

	// Create configuration with test-specific settings
	cfg := &config.Config{
		CacheDir:      tempDir,
		ListenAddress: "127.0.0.1",
		Port:          port,
		AdminPort:     adminPort,
		AdminAuth:     false,
		Backends: []config.Backend{
			{Name: "debian", URL: "http://deb.debian.org/debian", Priority: 100},
			{Name: "ubuntu", URL: "http://archive.ubuntu.com/ubuntu", Priority: 90},
		},
		MappingRules: []config.MappingRule{
			{Type: "prefix", Pattern: "/debian/", Repository: "debian", Priority: 100},
			{Type: "prefix", Pattern: "/ubuntu/", Repository: "ubuntu", Priority: 90},
		},
		CacheTTLs: map[string]string{
			"index":   "1h",
			"package": "30d",
		},
		Prefetch: config.PrefetchConfig{
			Enabled:   false, // Disable prefetching for tests
			BatchSize: 1,
		},
		MaxConcurrentDownloads: 4,
		CacheSize:              "10G",
		DatabaseMemoryMB:       1024,
		MaxCacheEntries:        10000,
		Log: config.LogConfig{
			Debug: config.DebugLog{
				TraceHTTPRequests: true,
			},
		},
	}

	return cfg, tempDir
}

// // startServerWithTimeout starts a server with a timeout context
// func startServerWithTimeout(t *testing.T, srv *server.Server, timeout time.Duration) {
// 	ctx, cancel := context.WithTimeout(context.Background(), timeout)
// 	defer cancel()

// 	go func() {
// 		if err := srv.StartWithContext(ctx); err != nil && err != context.DeadlineExceeded {
// 			log.Printf("Server error: %v", err)
// 		}
// 	}()

// 	// Wait a moment for the server to start
// 	time.Sleep(100 * time.Millisecond)
// }
