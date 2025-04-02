package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jdfalk/apt-cacher-go/internal/config"
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

// Continue updating the other test functions to use the new DatabaseStore model...
