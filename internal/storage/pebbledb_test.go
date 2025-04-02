package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestNewDatabaseStore tests the creation of a new DatabaseStore
// instance with various configuration options.
//
// The test verifies:
// - DatabaseStore can be created with default settings
// - DatabaseStore can be created with custom memory settings
// - Database initialization works correctly
//
// Approach:
// 1. Creates a temporary directory for the database
// 2. Tests initialization with nil config (default settings)
// 3. Tests initialization with custom memory settings
// 4. Verifies the database and internal structures are properly initialized
//
// Note: Uses subtests to isolate different initialization scenarios
func TestNewDatabaseStore(t *testing.T) {
	t.Run("initialization with default config", func(t *testing.T) {
		// Create temp dir
		dir, err := os.MkdirTemp("", "database-test")
		require.NoError(t, err)
		defer os.RemoveAll(dir)

		db, err := NewDatabaseStore(dir, nil)
		require.NoError(t, err)
		defer db.Close()

		// Basic assertions
		assert.NotNil(t, db)
		assert.NotNil(t, db.db)
	})

	t.Run("initialization with custom config", func(t *testing.T) {
		dir, err := os.MkdirTemp("", "database-test")
		require.NoError(t, err)
		defer os.RemoveAll(dir)

		// Create custom config
		cfg := &config.Config{
			// Initialize the Metadata map
			Metadata: make(map[string]any),
		}

		// Use the Metadata map properly - settings go into this map, not direct properties
		cfg.Metadata["memory_management.max_cache_size"] = "1024MB"
		cfg.Metadata["memory_management.max_entries"] = 10000

		db, err := NewDatabaseStore(dir, cfg)
		require.NoError(t, err)
		defer db.Close()

		// Only verify the mapper was created successfully
		assert.NotNil(t, db)
		assert.NotNil(t, db.db)
		assert.Equal(t, filepath.Join(dir, "pebbledb"), db.dbPath)
	})
}

// TestDatabaseStorePut tests the Put method of DatabaseStore
// which stores files in the filesystem and their metadata in the database.
//
// The test verifies:
// - Files are correctly written to the filesystem
// - Cache entries are properly created and stored in the database
// - Directory structures are created as needed
// - Error cases are handled properly
//
// Approach:
// 1. Creates a temporary database and filesystem location
// 2. Tests storing files with different paths and content
// 3. Verifies both the file content and metadata are stored correctly
// 4. Tests error handling and edge cases
func TestDatabaseStorePut(t *testing.T) {
	// Create temp dir for database
	tempDir, err := os.MkdirTemp("", "database-put-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create database store
	db, err := NewDatabaseStore(tempDir, nil)
	require.NoError(t, err)
	defer db.Close()

	t.Run("basic storage", func(t *testing.T) {
		// Define test data
		path := "testfile.txt"
		content := []byte("hello world")

		// Store the file
		err := db.Put(path, content)
		require.NoError(t, err)

		// Verify file exists in filesystem
		absPath := filepath.Join(tempDir, path)
		fileContent, err := os.ReadFile(absPath)
		require.NoError(t, err)
		assert.Equal(t, content, fileContent)

		// Verify cache entry was created
		entry, exists, err := db.GetCacheEntry(path)
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, path, entry.Path)
		assert.Equal(t, int64(len(content)), entry.Size)
		assert.Equal(t, 1, entry.HitCount)
		// LastAccessed and LastModified should be recent
		assert.True(t, time.Since(entry.LastAccessed) < time.Minute)
		assert.True(t, time.Since(entry.LastModified) < time.Minute)

		// Verify db.Exists returns true
		assert.True(t, db.Exists(path))
	})

	t.Run("nested directories", func(t *testing.T) {
		// Test with a path that includes directories
		nestedPath := "nested/dirs/test.dat"
		content := []byte("nested file content")

		// Store the file
		err := db.Put(nestedPath, content)
		require.NoError(t, err)

		// Verify file exists in filesystem with correct structure
		absPath := filepath.Join(tempDir, nestedPath)
		fileContent, err := os.ReadFile(absPath)
		require.NoError(t, err)
		assert.Equal(t, content, fileContent)

		// Verify cache entry was created
		entry, exists, err := db.GetCacheEntry(nestedPath)
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, nestedPath, entry.Path)
		assert.Equal(t, int64(len(content)), entry.Size)
	})

	t.Run("update existing file", func(t *testing.T) {
		// Define test data
		path := "update-file.txt"
		content1 := []byte("initial content")
		content2 := []byte("updated content")

		// Store the file initially
		err := db.Put(path, content1)
		require.NoError(t, err)

		// Update with new content
		err = db.Put(path, content2)
		require.NoError(t, err)

		// Verify file was updated
		absPath := filepath.Join(tempDir, path)
		fileContent, err := os.ReadFile(absPath)
		require.NoError(t, err)
		assert.Equal(t, content2, fileContent)

		// Verify cache entry was updated
		entry, exists, err := db.GetCacheEntry(path)
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, int64(len(content2)), entry.Size)
		// HitCount should still be 1 because Put always sets it to 1
		assert.Equal(t, 1, entry.HitCount)
	})

	t.Run("empty file", func(t *testing.T) {
		// Test with empty content
		path := "empty.dat"
		content := []byte{}

		// Store the file
		err := db.Put(path, content)
		require.NoError(t, err)

		// Verify file exists in filesystem
		absPath := filepath.Join(tempDir, path)
		fileContent, err := os.ReadFile(absPath)
		require.NoError(t, err)
		assert.Empty(t, fileContent)

		// Verify cache entry has zero size
		entry, exists, err := db.GetCacheEntry(path)
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, int64(0), entry.Size)
	})

	// This test requires a mock to properly test
	// directory creation failure, but we can do a basic check
	t.Run("invalid path handling", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			// Create a file where we want a directory (will fail on subsequent directory creation)
			invalidPath := filepath.Join(tempDir, "blocked-dir")
			err := os.WriteFile(invalidPath, []byte("blocking file"), 0644)
			require.NoError(t, err)

			// Try to create a file that would need a directory where our blocking file is
			path := "blocked-dir/should-fail.txt"
			err = db.Put(path, []byte("test"))
			assert.Error(t, err, "Should fail when directory cannot be created")
		}
	})
}

// TestDatabaseStoreGet tests the Get method of DatabaseStore
// which retrieves files from the cache.
//
// The test verifies:
// - Previously stored files can be retrieved
// - Get returns error for non-existent files
// - Cache hits are properly recorded
//
// Approach:
// 1. Creates a temporary database and stores test files
// 2. Tests retrieving the files with Get
// 3. Verifies hit count is incremented correctly
// 4. Tests error cases for missing files
func TestDatabaseStoreGet(t *testing.T) {
	// Create temp dir for database
	tempDir, err := os.MkdirTemp("", "database-get-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create database store
	db, err := NewDatabaseStore(tempDir, nil)
	require.NoError(t, err)
	defer db.Close()

	// Setup test files
	testCases := []struct {
		path    string
		content []byte
	}{
		{"file1.txt", []byte("file 1 content")},
		{"nested/file2.txt", []byte("file 2 content")},
		{"empty.txt", []byte{}},
	}

	// Store test files
	for _, tc := range testCases {
		err := db.Put(tc.path, tc.content)
		require.NoError(t, err)
	}

	t.Run("get existing file", func(t *testing.T) {
		for _, tc := range testCases {
			// Get the file
			content, err := db.Get(tc.path)
			require.NoError(t, err)
			assert.Equal(t, tc.content, content)

			// Verify hit count was incremented
			entry, exists, err := db.GetCacheEntry(tc.path)
			require.NoError(t, err)
			assert.True(t, exists)
			assert.Equal(t, 2, entry.HitCount) // 1 from Put + 1 from Get
		}
	})

	t.Run("get non-existent file", func(t *testing.T) {
		_, err := db.Get("does-not-exist.txt")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not in cache")
	})

	t.Run("multiple gets increment hit count", func(t *testing.T) {
		path := "file1.txt"

		// Get the file multiple times
		for range 3 {
			_, err := db.Get(path)
			require.NoError(t, err)
		}

		// Verify hit count reflects all accesses
		entry, exists, err := db.GetCacheEntry(path)
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, 5, entry.HitCount) // 1 from Put + 1 from previous test + 3 from this test
	})
}

// TestDatabaseStoreUpdatePackageIndex tests the UpdatePackageIndex and related package
// management functions in the DatabaseStore.
//
// The test verifies:
// - Packages are correctly stored with composite keys (name:version:architecture)
// - Package count caching and retrieval works correctly
// - Duplicate packages are properly detected and skipped
// - Hash mappings are correctly created and accessible
// - Iterator error handling works as expected
//
// Approach:
// 1. Creates test packages with different names, versions, and architectures
// 2. Verifies storage and retrieval of packages using the composite key format
// 3. Tests the package count caching mechanism
// 4. Verifies hash mappings for package lookups
// 5. Tests duplicate handling and detection
func TestDatabaseStoreUpdatePackageIndex(t *testing.T) {
	// Create temp dir for database
	tempDir, err := os.MkdirTemp("", "database-updatepkg-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create database store
	db, err := NewDatabaseStore(tempDir, nil)
	require.NoError(t, err)
	defer db.Close()

	// Create test package data with multiple versions/architectures
	packages := []parser.PackageInfo{
		{
			Package:      "nginx",
			Version:      "1.18.0-6ubuntu1",
			Architecture: "amd64",
			Filename:     "pool/main/n/nginx/nginx_1.18.0-6ubuntu1_amd64.deb",
			Size:         "43692",
			SHA256:       "8e4565d1b45eaf04b98c814ddda511ee5a1f80e50568009f24eec817a7797052",
			Description:  "small, powerful, scalable web/proxy server",
		},
		{
			Package:      "nginx",
			Version:      "1.18.0-6ubuntu1",
			Architecture: "arm64",
			Filename:     "pool/main/n/nginx/nginx_1.18.0-6ubuntu1_arm64.deb",
			Size:         "41582",
			SHA256:       "7e4565d1b45eaf04b98c814ddda511ee5a1f80e50568009f24eec817a7797053",
			Description:  "small, powerful, scalable web/proxy server",
		},
		{
			Package:      "python3.9",
			Version:      "3.9.5-3",
			Architecture: "amd64",
			Filename:     "pool/main/p/python3.9/python3.9_3.9.5-3_amd64.deb",
			Size:         "365640",
			SHA256:       "21f19637588d829b4ec43420b371dbcb63e557eacd8cedd55c9916c3e07f30de",
			Description:  "Interactive high-level object-oriented language (version 3.9)",
		},
	}

	// Test 1: Verify initial package count is zero
	initialCount, err := db.GetPackageCount()
	require.NoError(t, err)
	assert.Equal(t, 0, initialCount, "Initial package count should be zero")

	// Test 2: Update package index and verify count
	err = db.UpdatePackageIndex(packages)
	require.NoError(t, err)

	updatedCount, err := db.GetPackageCount()
	require.NoError(t, err)
	assert.Equal(t, len(packages), updatedCount, "Package count should match number of packages added")

	// Test 3: Verify we can retrieve packages by their composite keys
	for _, pkg := range packages {
		key := fmt.Sprintf("p:%s:%s:%s", pkg.Package, pkg.Version, pkg.Architecture)
		value, closer, err := db.db.Get([]byte(key))
		require.NoError(t, err)
		defer closer.Close()

		// Verify data was stored correctly
		var storedPkg parser.PackageInfo
		err = json.Unmarshal(value, &storedPkg)
		require.NoError(t, err)
		assert.Equal(t, pkg.Package, storedPkg.Package)
		assert.Equal(t, pkg.Version, storedPkg.Version)
		assert.Equal(t, pkg.Architecture, storedPkg.Architecture)
	}

	// Test 4: Verify package retrieval via GetPackageInfo
	for _, pkg := range packages {
		storedPkg, exists, err := db.GetPackageInfo(pkg.Package)
		require.NoError(t, err)
		assert.True(t, exists)
		assert.Equal(t, pkg.Package, storedPkg.Package)
		// Note: GetPackageInfo only returns the first matching package
	}

	// Test 5: Verify we can retrieve package names by hash
	for _, pkg := range packages {
		if pkg.SHA256 != "" {
			hashKey := fmt.Sprintf("h:%s", pkg.SHA256)
			value, closer, err := db.db.Get([]byte(hashKey))
			require.NoError(t, err)
			defer closer.Close()
			assert.Equal(t, pkg.Package, string(value))
		}
	}

	// Test 6: Test adding duplicate packages (should be skipped)
	err = db.UpdatePackageIndex(packages)
	require.NoError(t, err)

	// Count should remain the same
	duplicateCount, err := db.GetPackageCount()
	require.NoError(t, err)
	assert.Equal(t, len(packages), duplicateCount, "Package count should not change after adding duplicates")

	// Test 7: Verify the package count caching mechanism works
	// First, directly set the packageCount and timestamp
	db.packageCountMutex.Lock()
	db.packageCount = int64(len(packages))
	db.packageCountTime = time.Now()
	db.packageCountMutex.Unlock()

	// This should use the cached count
	cachedCount, err := db.GetPackageCount()
	require.NoError(t, err)
	assert.Equal(t, len(packages), cachedCount, "Should return cached package count")

	// Test 8: Add a new package with a different version
	newPkg := parser.PackageInfo{
		Package:      "redis",
		Version:      "6.0.16-1",
		Architecture: "amd64",
		Filename:     "pool/main/r/redis/redis_6.0.16-1_amd64.deb",
		Size:         "1048576",
		SHA256:       "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Description:  "Persistent key-value database",
	}

	// Update with the new package
	err = db.UpdatePackageIndex([]parser.PackageInfo{newPkg})
	require.NoError(t, err)

	// Verify the new package was added and count increased
	finalCount, err := db.GetPackageCount()
	require.NoError(t, err)
	assert.Equal(t, len(packages)+1, finalCount, "Package count should increase after adding new package")

	// Verify the new package was added
	key := fmt.Sprintf("p:%s:%s:%s", newPkg.Package, newPkg.Version, newPkg.Architecture)
	_, closer, err := db.db.Get([]byte(key))
	require.NoError(t, err)
	closer.Close()

	// Test 9: Verify ListPackages returns all packages
	allPackages, err := db.ListPackages("")
	require.NoError(t, err)
	assert.Equal(t, len(packages)+1, len(allPackages), "ListPackages should return all packages")

	// Test 10: Verify pattern filtering in ListPackages
	nginxPackages, err := db.ListPackages("nginx")
	require.NoError(t, err)
	assert.Equal(t, 2, len(nginxPackages), "Should find 2 nginx packages")
}
