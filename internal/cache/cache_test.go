package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockCache implements mock functionality for testing
type MockCache struct {
	mock.Mock
}

func (m *MockCache) Get(path string) ([]byte, error) {
	args := m.Called(path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockCache) Put(path string, data []byte) error {
	args := m.Called(path, data)
	return args.Error(0)
}

func (m *MockCache) PutWithExpiration(path string, data []byte, ttl time.Duration) error {
	args := m.Called(path, data, ttl)
	return args.Error(0)
}

func (m *MockCache) SearchByPackageName(name string) ([]CacheSearchResult, error) {
	args := m.Called(name)
	return args.Get(0).([]CacheSearchResult), args.Error(1)
}

func (m *MockCache) UpdatePackageIndex(packages []parser.PackageInfo) error {
	args := m.Called(packages)
	return args.Error(0)
}

func (m *MockCache) IsFresh(path string) bool {
	args := m.Called(path)
	return args.Bool(0)
}

func (m *MockCache) Exists(path string) bool {
	args := m.Called(path)
	return args.Bool(0)
}

func (m *MockCache) GetStats() CacheStats {
	args := m.Called()
	return args.Get(0).(CacheStats)
}

func (m *MockCache) Clear() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockCache) FlushExpired() (int, error) {
	args := m.Called()
	return args.Int(0), args.Error(1)
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestCacheOperations tests the basic cache operations including:
// - Adding files to the cache
// - Retrieving files from the cache
// - Checking file existence
// - Cache expiration functionality
//
// The test verifies:
// - Files can be successfully stored and retrieved
// - Cache metadata is properly maintained
// - Expiration works as expected
//
// Approach:
// 1. Creates a temporary directory for test cache storage
// 2. Initializes a cache with specified size limit
// 3. Tests basic cache operations with real file I/O
//
// Note: Uses temporary directories that are cleaned up after tests
func TestCacheOperations(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "cache-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a cache with 10MB max size
	cache, err := New(tempDir, 10*1024*1024) // 10MB
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}
	defer cache.Close()

	// Test putting data in the cache
	testData := []byte("This is test data")
	testKey := "file.deb" // Remove the "test/path/" prefix
	require.NoError(t, cache.Put(testKey, testData))

	// Test getting data from the cache
	data, err := cache.Get(testKey)
	require.NoError(t, err)
	assert.Equal(t, testData, data)

	// Test that the file was created on disk
	filePath := filepath.Join(tempDir, testKey)
	_, err = os.Stat(filePath)
	assert.False(t, os.IsNotExist(err))

	// Test expiration - use a name that doesn't create a directory
	shortKey := "short-lived.data" // Remove the "temp/" prefix
	require.NoError(t, cache.PutWithExpiration(shortKey, []byte("Short lived"), 10*time.Millisecond))

	// Verify it exists initially
	_, err = cache.Get(shortKey)
	require.NoError(t, err)

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Should report not fresh now
	assert.False(t, cache.IsFresh(shortKey))
}

func TestCacheWithMock(t *testing.T) {
	// Setup mock
	mockCache := new(MockCache)

	// Test data
	testData := []byte("Mock test data")
	testKey := "mock/path/file.deb"

	// Setup expectations
	mockCache.On("Get", testKey).Return(testData, nil)
	mockCache.On("Put", testKey, testData).Return(nil)
	mockCache.On("IsFresh", testKey).Return(true)

	// Use the mock
	err := mockCache.Put(testKey, testData)
	require.NoError(t, err)

	data, err := mockCache.Get(testKey)
	require.NoError(t, err)
	assert.Equal(t, testData, data)

	fresh := mockCache.IsFresh(testKey)
	assert.True(t, fresh)

	// Verify all expectations were met
	mockCache.AssertExpectations(t)
}

func TestCacheConcurrency(t *testing.T) {
	// For a real concurrency test, use a real cache instance
	tempDir, err := os.MkdirTemp("", "cache-concurrency-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	realCache, err := New(tempDir, 10*1024*1024)
	require.NoError(t, err)

	// Test concurrent operations with real cache
	numOperations := 100
	done := make(chan bool, numOperations*2)

	// Add concurrent operations
	for i := range numOperations {
		go func(idx int) {
			key := fmt.Sprintf("concurrent-file-%d.data", idx) // No directory path
			var buf []byte
			buf = fmt.Appendf(buf, "Data for file %d", idx)
			_ = realCache.Put(key, buf) // Ignore the error, just using _ to acknowledge it
			done <- true
		}(i)

		go func(idx int) {
			key := fmt.Sprintf("concurrent-file-%d.data", idx) // No directory path
			_, _ = realCache.Get(key)                          // Ignore both return values
			done <- true
		}(i)
	}

	// Wait for all operations to complete
	for range numOperations * 2 {
		<-done
	}
}

func TestCacheCleanup(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "cache-cleanup-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a cache with very small max size (1KB)
	cache, err := New(tempDir, 1024) // 1KB
	require.NoError(t, err)
	defer cache.Close()

	// Put some data that exceeds the cache size
	testData := make([]byte, 1024) // 1KB
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	// Put the data twice, which should exceed the cache limit
	// Use simple filenames without directory paths
	require.NoError(t, cache.Put("file1.deb", testData))
	require.NoError(t, cache.Put("file2.deb", testData))

	// Wait a moment for cleanup to occur
	time.Sleep(100 * time.Millisecond)

	// Verify that only one file is still in cache - due to LRU eviction
	stats := cache.GetStats()
	assert.LessOrEqual(t, stats.CurrentSize, int64(1024),
		"Cache size should be limited to 1KB")

	// Either file1 or file2 should exist, but not both
	exists1 := cache.Exists("file1.deb")
	exists2 := cache.Exists("file2.deb")
	assert.True(t, exists1 || exists2, "At least one file should exist")
	assert.False(t, exists1 && exists2, "Both files should not exist")
}
