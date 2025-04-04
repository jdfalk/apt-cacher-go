package integration

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/mocks"
	"github.com/jdfalk/apt-cacher-go/internal/server"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// createTestServer creates a server instance with mock backend for integration testing
func createTestServer(t *testing.T, cfg *config.Config) (*server.Server, func()) {
	// Create a mock backend manager
	mockBackend := new(mocks.MockBackendManager)
	mockBackend.On("KeyManager").Return(nil).Maybe()
	mockBackend.On("ForceCleanupPrefetcher").Return(0).Maybe()
	mockBackend.On("RefreshReleaseData", mock.Anything).Return(nil).Maybe()
	mockBackend.On("PrefetchOnStartup", mock.Anything).Return().Maybe()

	// Setup Fetch mock to handle actual file requests
	mockBackend.On("Fetch", mock.AnythingOfType("string")).Return(
		func(path string) []byte {
			// For testing, just return dummy content
			return []byte("dummy content for " + path)
		},
		func(path string) error {
			return nil
		},
	).Maybe()

	// Create the server with mock backend
	srv, err := server.New(cfg, server.ServerOptions{
		Version:        "integration-test",
		BackendManager: mockBackend,
	})
	require.NoError(t, err)

	// Prepare cleanup function that ensures proper server shutdown
	cleanup := func() {
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
			// Shutdown completed successfully
		case <-time.After(5 * time.Second):
			t.Log("Warning: Server shutdown timed out")
		}
	}

	return srv, cleanup
}

// createServerConfig creates a config suitable for integration testing
func createServerConfig(t *testing.T) (*config.Config, string) {
	// Create temporary directory for cache
	tempDir, err := os.MkdirTemp("", "apt-cacher-integration")
	require.NoError(t, err)

	// Create base config
	cfg := &config.Config{
		CacheDir:      tempDir,
		ListenAddress: "127.0.0.1",
		Port:          8080 + (os.Getpid() % 1000), // Use unique port based on PID
		AdminPort:     9090 + (os.Getpid() % 1000), // Use unique admin port
		CacheSize:     "256M",                      // Small cache size for testing
		LogLevel:      "debug",
		Backends: []config.Backend{
			{
				Name: "debian",
				URL:  "http://deb.debian.org/debian",
			},
			{
				Name: "ubuntu",
				URL:  "http://archive.ubuntu.com/ubuntu",
			},
		},
		MappingRules: []config.MappingRule{
			{
				Type:       "prefix",
				Pattern:    "/debian",
				Repository: "debian",
				Priority:   100,
			},
			{
				Type:       "prefix",
				Pattern:    "/ubuntu",
				Repository: "ubuntu",
				Priority:   100,
			},
		},
	}

	return cfg, tempDir
}

// startServerWithTimeout starts a server with a timeout context
func startServerWithTimeout(t *testing.T, srv *server.Server, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	go func() {
		if err := srv.StartWithContext(ctx); err != nil && err != context.DeadlineExceeded {
			log.Printf("Server error: %v", err)
		}
	}()

	// Wait a moment for the server to start
	time.Sleep(100 * time.Millisecond)
}
