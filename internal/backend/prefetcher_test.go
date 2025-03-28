package backend

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockManager implements mock functionality for testing
type MockManager struct {
	mock.Mock
}

func (m *MockManager) Fetch(url string) ([]byte, error) {
	args := m.Called(url)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockManager) GetAllBackends() []*Backend {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).([]*Backend)
}

// TestManager embeds Manager and provides mock implementations of necessary methods
type TestManager struct {
	Manager              // Embed the real Manager
	mock    *MockManager // Store the mock to delegate calls
}

// Override the methods we want to mock
func (tm *TestManager) Fetch(url string) ([]byte, error) {
	return tm.mock.Fetch(url)
}

func (tm *TestManager) GetAllBackends() []*Backend {
	return tm.mock.GetAllBackends()
}

// Create a wrapper for NewPrefetcher that works with our mock
func createPrefetcher(mockManager *MockManager, maxActive int, architectures []string) *Prefetcher {
	// Create a TestManager that embeds Manager but uses our mock for specific methods
	testManager := &TestManager{
		mock: mockManager,
	}

	// Create the prefetcher with our TestManager which satisfies the *Manager requirement
	return NewPrefetcher(testManager, maxActive, architectures)
}

func TestNewPrefetcher(t *testing.T) {
	mockManager := new(MockManager)
	archs := []string{"amd64", "i386"}

	// Mock GetAllBackends to return empty slice
	mockManager.On("GetAllBackends").Return([]*Backend{}, nil)

	prefetcher := createPrefetcher(mockManager, 10, archs)

	assert.NotNil(t, prefetcher)
	assert.Equal(t, 10, prefetcher.maxActive)
	assert.Equal(t, 2, len(prefetcher.architectures))
	assert.True(t, prefetcher.architectures["amd64"])
	assert.True(t, prefetcher.architectures["i386"])
	assert.NotNil(t, prefetcher.cleanupTick)
	assert.NotNil(t, prefetcher.stopCh)
}

func TestFilterByArchitecture(t *testing.T) {
	mockManager := new(MockManager)
	mockManager.On("GetAllBackends").Return([]*Backend{}, nil)

	// Test with specific architectures
	p1 := createPrefetcher(mockManager, 10, []string{"amd64", "arm64"})

	urls := []string{
		"/debian/pool/main/a/apt/apt_2.2.4_amd64.deb",
		"/debian/pool/main/a/apt/apt_2.2.4_i386.deb",
		"/debian/pool/main/a/apt/apt_2.2.4_arm64.deb",
		"/debian/dists/stable/main/binary-amd64/Packages",
		"/debian/dists/stable/main/binary-i386/Packages",
		"/debian/dists/stable/main/binary-arm64/Packages",
		"/debian/dists/stable/InRelease", // Not architecture-specific
	}

	filtered := p1.filterByArchitecture(urls)

	// Should include amd64, arm64, and non-arch-specific items
	assert.Equal(t, 5, len(filtered))
	assert.Contains(t, filtered, "/debian/pool/main/a/apt/apt_2.2.4_amd64.deb")
	assert.Contains(t, filtered, "/debian/pool/main/a/apt/apt_2.2.4_arm64.deb")
	assert.Contains(t, filtered, "/debian/dists/stable/main/binary-amd64/Packages")
	assert.Contains(t, filtered, "/debian/dists/stable/main/binary-arm64/Packages")
	assert.Contains(t, filtered, "/debian/dists/stable/InRelease")

	// Test with no architectures specified (should include all)
	mockManager.On("GetAllBackends").Return([]*Backend{}, nil).Once()
	p2 := createPrefetcher(mockManager, 10, []string{})
	allFiltered := p2.filterByArchitecture(urls)
	assert.Equal(t, len(urls), len(allFiltered))
}

func TestFilterURLsByArchitecture(t *testing.T) {
	mockManager := new(MockManager)
	mockManager.On("GetAllBackends").Return([]*Backend{}, nil)

	prefetcher := createPrefetcher(mockManager, 10, []string{"amd64"})

	urls := []string{
		"/debian/pool/main/a/apt/apt_2.2.4_amd64.deb",
		"/debian/pool/main/a/apt/apt_2.2.4_i386.deb",
	}

	filtered := prefetcher.FilterURLsByArchitecture(urls)

	assert.Equal(t, 1, len(filtered))
	assert.Contains(t, filtered, "/debian/pool/main/a/apt/apt_2.2.4_amd64.deb")
	assert.NotContains(t, filtered, "/debian/pool/main/a/apt/apt_2.2.4_i386.deb")
}

func TestExtractURLsFromIndexEfficient(t *testing.T) {
	packagesContent := `Package: apt
Version: 2.2.4
Architecture: amd64
Filename: pool/main/a/apt/apt_2.2.4_amd64.deb
Size: 1234

Package: dpkg
Version: 1.20.9
Architecture: amd64
Filename: pool/main/d/dpkg/dpkg_1.20.9_amd64.deb
Size: 2345
`

	urls := extractURLsFromIndexEfficient([]byte(packagesContent))

	assert.Equal(t, 2, len(urls))
	assert.Contains(t, urls, "pool/main/a/apt/apt_2.2.4_amd64.deb")
	assert.Contains(t, urls, "pool/main/d/dpkg/dpkg_1.20.9_amd64.deb")
}

func TestContainsAnyArch(t *testing.T) {
	assert.True(t, containsAnyArch("/debian/pool/main/a/apt/apt_2.2.4_amd64.deb"))
	assert.True(t, containsAnyArch("/debian/pool/main/a/apt/apt_2.2.4_i386.deb"))
	assert.True(t, containsAnyArch("/debian/dists/stable/main/binary-arm64/Packages"))
	assert.False(t, containsAnyArch("/debian/dists/stable/InRelease"))
	assert.False(t, containsAnyArch("/debian/pool/main/a/apt/apt_2.2.4.deb"))
}

func TestMemoryPressure(t *testing.T) {
	mockManager := new(MockManager)
	mockManager.On("GetAllBackends").Return([]*Backend{}, nil)

	prefetcher := createPrefetcher(mockManager, 10, []string{"amd64"})

	// Initially no memory pressure
	assert.Equal(t, int32(0), prefetcher.memoryPressure)

	// Set high memory pressure
	prefetcher.SetMemoryPressure(90)
	assert.Equal(t, int32(90), prefetcher.memoryPressure)

	// Test that processing is skipped when memory pressure is high
	mockManager.On("Fetch", mock.Anything).Return([]byte("test"), nil)

	// This should skip processing due to high memory pressure
	prefetcher.ProcessIndexFile("debian", "dists/stable/main/binary-amd64/Packages", []byte("Filename: test.deb"))

	// Reset memory pressure
	prefetcher.SetMemoryPressure(0)
	assert.Equal(t, int32(0), prefetcher.memoryPressure)
}

func TestForceCleanup(t *testing.T) {
	mockManager := new(MockManager)
	mockManager.On("GetAllBackends").Return([]*Backend{}, nil)

	prefetcher := createPrefetcher(mockManager, 10, []string{"amd64"})

	// Add some mock operations
	prefetcher.active.Store("url1", time.Now())
	prefetcher.active.Store("url2", time.Now())

	// Force cleanup
	cleaned := prefetcher.ForceCleanup()

	// Should have cleaned 2 items
	assert.Equal(t, 2, cleaned)

	// Verify they're gone
	var count int
	prefetcher.active.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	assert.Equal(t, 0, count)
}

func TestShutdown(t *testing.T) {
	mockManager := new(MockManager)
	mockManager.On("GetAllBackends").Return([]*Backend{}, nil)

	prefetcher := createPrefetcher(mockManager, 10, []string{"amd64"})

	// Start a long-running operation
	done := make(chan struct{})
	go func() {
		<-prefetcher.stopCh
		close(done)
	}()

	// Shutdown should signal the stop channel
	prefetcher.Shutdown()

	// Wait for the operation to complete
	select {
	case <-done:
		// Success
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Shutdown did not signal the stop channel")
	}
}

func TestSetVerboseLogging(t *testing.T) {
	mockManager := new(MockManager)
	mockManager.On("GetAllBackends").Return([]*Backend{}, nil)
	prefetcher := createPrefetcher(mockManager, 10, []string{})

	assert.False(t, prefetcher.verboseLogging)

	prefetcher.SetVerboseLogging(true)
	assert.True(t, prefetcher.verboseLogging)

	prefetcher.SetVerboseLogging(false)
	assert.False(t, prefetcher.verboseLogging)
}
func TestIsArchitectureEnabled(t *testing.T) {
	mockManager := new(MockManager)
	mockManager.On("GetAllBackends").Return([]*Backend{}, nil)
	prefetcher := createPrefetcher(mockManager, 10, []string{"amd64", "arm64"})

	assert.True(t, prefetcher.IsArchitectureEnabled("amd64"))
	assert.True(t, prefetcher.IsArchitectureEnabled("arm64"))
	assert.False(t, prefetcher.IsArchitectureEnabled("i386"))

	// Test with no architectures (should enable all)
	prefetcher2 := createPrefetcher(mockManager, 10, []string{})
	assert.True(t, prefetcher2.IsArchitectureEnabled("amd64"))
	assert.True(t, prefetcher2.IsArchitectureEnabled("i386"))
	assert.True(t, prefetcher2.IsArchitectureEnabled("any-arch"))
}

func TestAddArchitecture(t *testing.T) {
	mockManager := new(MockManager)
	mockManager.On("GetAllBackends").Return([]*Backend{}, nil)
	prefetcher := createPrefetcher(mockManager, 10, []string{"amd64"})

	assert.True(t, prefetcher.IsArchitectureEnabled("amd64"))
	assert.True(t, prefetcher.IsArchitectureEnabled("amd64"))
	assert.False(t, prefetcher.IsArchitectureEnabled("arm64"))

	prefetcher.AddArchitecture("arm64", "ppc64el")

	assert.True(t, prefetcher.IsArchitectureEnabled("amd64"))
	assert.True(t, prefetcher.IsArchitectureEnabled("arm64"))
	assert.True(t, prefetcher.IsArchitectureEnabled("ppc64el"))
}

func TestProcessIndexFileConcurrencyLimit(t *testing.T) {
	mockManager := new(MockManager)
	mockManager.On("GetAllBackends").Return([]*Backend{}, nil)
	maxActive := 2
	prefetcher := createPrefetcher(mockManager, maxActive, []string{})
	// Set in-progress to max to test throttling
	atomic.StoreInt32(&prefetcher.inProgress, int32(maxActive))

	packagesContent := `Package: apt
Filename: pool/main/a/apt/apt_2.2.4_amd64.deb
`

	// This should be skipped due to max active
	prefetcher.ProcessIndexFile("debian", "test", []byte(packagesContent))

	// Reset and test again
	atomic.StoreInt32(&prefetcher.inProgress, 0)
	mockManager.On("Fetch", mock.Anything).Return([]byte("test"), nil)

	// Now it should process
	prefetcher.ProcessIndexFile("debian", "test", []byte(packagesContent))
}
