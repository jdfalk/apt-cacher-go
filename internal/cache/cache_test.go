package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCacheOperations test
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

	// Test putting data in the cache
	testData := []byte("This is test data")
	testKey := "test/path/file.deb"

	if err := cache.Put(testKey, testData); err != nil {
		t.Fatalf("Failed to put data in cache: %v", err)
	}

	// Test getting data from the cache
	data, err := cache.Get(testKey)
	if err != nil {
		t.Fatalf("Failed to get data from cache: %v", err)
	}
	if string(data) != string(testData) {
		t.Fatalf("Data mismatch: got %s, want %s", string(data), string(testData))
	}

	// Test that the file was created on disk
	filePath := filepath.Join(tempDir, testKey)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Fatalf("Cache file not created on disk")
	}

	// Test expiration
	shortKey := "short-lived"
	if err := cache.PutWithExpiration(shortKey, []byte("expires soon"), 10*time.Millisecond); err != nil {
		t.Fatalf("Failed to put data with expiration: %v", err)
	}

	// Verify it exists initially
	_, err = cache.Get(shortKey)
	if err != nil {
		t.Fatalf("Short-lived data not found immediately after put")
	}

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Should report not fresh now
	if cache.IsFresh(shortKey) {
		t.Fatalf("Cache item should have expired")
	}
}

func TestCacheCleanup(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "cache-cleanup-test")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a cache with very small max size (1KB)
	cache, err := New(tempDir, 1)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	// Put some data that exceeds the cache size
	testData := make([]byte, 1024) // 1KB
	for i := 0; i < 1024; i++ {
		testData[i] = byte(i % 256)
	}

	// Put the data twice, which should exceed the cache limit
	if err := cache.Put("file1.deb", testData); err != nil {
		t.Fatalf("Failed to put first file: %v", err)
	}

	// Sleep a bit to ensure different timestamps
	time.Sleep(10 * time.Millisecond)

	if err := cache.Put("file2.deb", testData); err != nil {
		t.Fatalf("Failed to put second file: %v", err)
	}

	// The cleanup should have been triggered, and the current size should be
	// less than or equal to the max size (1KB)
	if cache.currentSize > cache.maxSize {
		t.Fatalf("Cache cleanup failed: current size %d exceeds max size %d",
			cache.currentSize, cache.maxSize)
	}
}
