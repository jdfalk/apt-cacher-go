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
		t.Fatalf("Data mismatch: expected %s, got %s", testData, data)
	}

	// Test that the file was created on disk
	filePath := filepath.Join(tempDir, testKey)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Fatalf("File was not created on disk: %v", err)
	}

	// Test expiration
	shortKey := "temp/short-lived.data" // Added directory structure
	err = cache.PutWithExpiration(shortKey, []byte("Short lived"), 10*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to put data with expiration: %v", err)
	}

	// Verify it exists initially
	_, err = cache.Get(shortKey)
	if err != nil {
		t.Fatalf("Couldn't get short-lived data: %v", err)
	}

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Should report not fresh now
	if cache.IsFresh(shortKey) {
		t.Fatalf("Data should have expired but reported as fresh")
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
	cache, err := New(tempDir, 1024) // 1KB
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}

	// Put some data that exceeds the cache size
	testData := make([]byte, 1024) // 1KB
	// Use range-based loop instead of traditional for loop
	for i := range 1024 {
		testData[i] = byte(i % 256)
	}

	// Put the data twice, which should exceed the cache limit
	if err := cache.Put("test/file1.deb", testData); err != nil { // Added directory structure
		t.Fatalf("Failed to put first file: %v", err)
	}

	// Sleep a bit to ensure different timestamps
	time.Sleep(10 * time.Millisecond)

	if err := cache.Put("test/file2.deb", testData); err != nil { // Added directory structure
		t.Fatalf("Failed to put second file: %v", err)
	}

	// The cleanup should have been triggered, and the current size should be
	// less than or equal to the max size (1KB)
	if cache.Size() > 1024 {
		t.Fatalf("Cache size is %d, expected <= 1024", cache.Size())
	}
}
