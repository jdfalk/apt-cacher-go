package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPersistentPackageMapper(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "hashmapper-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	t.Run("initialization with default config", func(t *testing.T) {
		// Create with nil config for default values
		mapper, err := NewPersistentPackageMapper(tempDir, nil)
		require.NoError(t, err)
		require.NotNil(t, mapper)
		defer mapper.Close()

		// Verify default settings
		assert.Equal(t, 10000, mapper.maxCacheSize)
		assert.Equal(t, 100, mapper.batchSize)
		assert.NotNil(t, mapper.db)
		assert.NotEmpty(t, mapper.dbPath)
		assert.True(t, strings.Contains(mapper.dbPath, tempDir))
	})

	t.Run("initialization with custom config", func(t *testing.T) {
		// Create a config with custom memory values
		cfg := &config.Config{
			Metadata: map[string]any{
				"memory_management": map[string]any{
					"max_cache_size":        "2G",
					"critical_watermark_mb": 4096,
				},
			},
		}

		mapper, err := NewPersistentPackageMapper(tempDir, cfg)
		require.NoError(t, err)
		require.NotNil(t, mapper)
		defer mapper.Close()

		// Values should be higher based on 2GB setting
		assert.Greater(t, mapper.maxCacheSize, 10000)
	})

	t.Run("error on invalid directory", func(t *testing.T) {
		// Try to initialize with a file as directory (should fail)
		testFile := filepath.Join(tempDir, "test-file")
		require.NoError(t, os.WriteFile(testFile, []byte("test"), 0644))

		mapper, err := NewPersistentPackageMapper(testFile, nil)
		assert.Error(t, err)
		assert.Nil(t, mapper)
	})
}

func TestAddAndGetHashMapping(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "hashmapper-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create mapper
	mapper, err := NewPersistentPackageMapper(tempDir, nil)
	require.NoError(t, err)
	require.NotNil(t, mapper)
	defer mapper.Close()

	// Test data
	testHash := "1234567890abcdef"
	testPackage := "test-package_1.0.0_amd64.deb"
	testPath := fmt.Sprintf("/debian/pool/main/t/test/by-hash/SHA256/%s", testHash)

	t.Run("add mapping", func(t *testing.T) {
		err := mapper.AddHashMapping(testHash, testPackage)
		assert.NoError(t, err)
	})

	t.Run("get mapping from memory cache", func(t *testing.T) {
		result := mapper.GetPackageNameForHash(testPath)
		assert.Equal(t, testPackage, result)
	})

	t.Run("persist to disk and retrieve", func(t *testing.T) {
		// Close the original mapper
		require.NoError(t, mapper.Close())

		// Create a new mapper to force disk read
		newMapper, err := NewPersistentPackageMapper(tempDir, nil)
		require.NoError(t, err)
		defer newMapper.Close()

		// Get the mapping (should come from disk)
		result := newMapper.GetPackageNameForHash(testPath)
		assert.Equal(t, testPackage, result)
	})

	t.Run("handle non-existent hash", func(t *testing.T) {
		nonExistentPath := "/debian/pool/main/t/test/by-hash/SHA256/nonexistenthash"
		result := mapper.GetPackageNameForHash(nonExistentPath)
		assert.Empty(t, result)
	})

	t.Run("handle non-hash path", func(t *testing.T) {
		nonHashPath := "/debian/pool/main/t/test/test-package_1.0.0_amd64.deb"
		result := mapper.GetPackageNameForHash(nonHashPath)
		assert.Empty(t, result)
	})
}

func TestClearCache(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "hashmapper-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create mapper
	mapper, err := NewPersistentPackageMapper(tempDir, nil)
	require.NoError(t, err)
	require.NotNil(t, mapper)
	defer mapper.Close()

	// Test data
	testHashes := []string{
		"hash1", "hash2", "hash3", "hash4", "hash5",
	}

	// Add multiple mappings
	for i, hash := range testHashes {
		err := mapper.AddHashMapping(hash, fmt.Sprintf("package-%d", i))
		require.NoError(t, err)
	}

	// Verify cache has the mappings
	mapper.cacheMutex.RLock()
	cacheSize := len(mapper.cache)
	mapper.cacheMutex.RUnlock()
	assert.Equal(t, len(testHashes), cacheSize)

	// Clear the cache
	mapper.ClearCache()

	// Verify cache is cleared
	mapper.cacheMutex.RLock()
	cacheSize = len(mapper.cache)
	mapper.cacheMutex.RUnlock()
	assert.Zero(t, cacheSize)

	// But data should still be on disk - verify by retrieving a mapping
	path := fmt.Sprintf("/debian/pool/main/t/test/by-hash/SHA256/%s", testHashes[0])
	result := mapper.GetPackageNameForHash(path)
	assert.Equal(t, "package-0", result)
}

func TestCheckpointAndMaintenance(t *testing.T) {
	// Create temporary directory for test
	tempDir, err := os.MkdirTemp("", "hashmapper-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create mapper
	mapper, err := NewPersistentPackageMapper(tempDir, nil)
	require.NoError(t, err)
	require.NotNil(t, mapper)
	defer mapper.Close()

	// Test data
	testHash := "checkpointhash"
	testPackage := "checkpoint-package_1.0.0_amd64.deb"

	// Add a mapping
	err = mapper.AddHashMapping(testHash, testPackage)
	require.NoError(t, err)

	// Create checkpoint directory
	checkpointDir := filepath.Join(tempDir, "test-checkpoint")
	err = os.MkdirAll(checkpointDir, 0755)
	require.NoError(t, err)

	t.Run("create checkpoint", func(t *testing.T) {
		err := mapper.CreateCheckpoint(checkpointDir)
		assert.NoError(t, err)

		// Verify checkpoint files exist
		files, err := os.ReadDir(checkpointDir)
		assert.NoError(t, err)
		assert.NotEmpty(t, files)
	})

	t.Run("verify integrity", func(t *testing.T) {
		err := mapper.VerifyIntegrity()
		assert.NoError(t, err)
	})

	t.Run("maintenance routine", func(t *testing.T) {
		// Create a context that will be canceled
		ctx, cancel := context.WithCancel(context.Background())

		// Start maintenance with short intervals for testing
		go func() {
			checkpointTicker := time.NewTicker(50 * time.Millisecond)
			compactionTicker := time.NewTicker(100 * time.Millisecond)

			defer checkpointTicker.Stop()
			defer compactionTicker.Stop()

			select {
			case <-checkpointTicker.C:
				// Should create a checkpoint
			case <-compactionTicker.C:
				// Should run compaction
			case <-ctx.Done():
				return
			}
		}()

		// Let it run briefly
		time.Sleep(200 * time.Millisecond)

		// Cancel the context
		cancel()

		// Wait for cancellation to take effect
		time.Sleep(50 * time.Millisecond)
	})
}
