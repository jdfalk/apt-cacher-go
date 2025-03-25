package cache

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// CacheStats contains cache statistics
type CacheStats struct {
	CurrentSize int64
	MaxSize     int64
	Items       int
	HitRate     float64
	MissRate    float64
	Hits        int64
	Misses      int64
}

// Cache manages the package cache
type Cache struct {
	baseDir     string
	maxSize     int64
	currentSize int64
	mutex       sync.RWMutex
	expiration  map[string]time.Time // Tracks expiration times
	lru         *LRUCache            // Tracks LRU order
	hits        int64
	misses      int64
}

// New creates a new Cache instance
func New(baseDir string, maxSizeMB int64) (*Cache, error) {
	// Create cache directory if it doesn't exist
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	c := &Cache{
		baseDir:    baseDir,
		maxSize:    maxSizeMB * 1024 * 1024, // Convert MB to bytes
		expiration: make(map[string]time.Time),
		lru:        NewLRUCache(100000), // Track up to 100K files
	}

	// Calculate initial cache size
	if err := c.updateCacheSize(); err != nil {
		return nil, err
	}

	// Start background cleanup goroutine
	go c.backgroundCleanup()

	return c, nil
}

// Get attempts to retrieve a file from the cache
func (c *Cache) Get(key string) ([]byte, bool, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	path := c.pathForKey(key)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		c.misses++
		return nil, false, nil
	}

	// Update access tracking
	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, false, err
	}

	c.lru.Add(key, fileInfo.Size())

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}

	c.hits++
	return data, true, nil
}

// IsFresh checks if a cached item is still fresh based on its expiration time
func (c *Cache) IsFresh(key string) bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	expTime, exists := c.expiration[key]
	if !exists {
		return false // No expiration info, consider stale
	}

	return time.Now().Before(expTime)
}

// Put stores data in the cache with default expiration
func (c *Cache) Put(key string, data []byte) error {
	// Default expiration is 30 days
	return c.PutWithExpiration(key, data, 30*24*time.Hour)
}

// PutWithExpiration stores data in the cache with a specific expiration
func (c *Cache) PutWithExpiration(key string, data []byte, expiration time.Duration) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	path := c.pathForKey(key)

	// Create directories if needed
	dir := filepath.Dir(path)
	if err := ensureDirectoryExists(dir); err != nil {
		return err
	}

	// Write file
	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}

	fileSize := int64(len(data))

	// Update cache size
	c.currentSize += fileSize

	// Set expiration
	c.expiration[key] = time.Now().Add(expiration)

	// Update LRU
	c.lru.Add(key, fileSize)

	// Check if cleanup is needed
	if c.currentSize > c.maxSize {
		if err := c.cleanup(); err != nil {
			return err
		}
	}

	return nil
}

// GetStats returns statistics about the cache
func (c *Cache) GetStats() (*CacheStats, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	// Count files
	var itemCount int
	err := filepath.Walk(c.baseDir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			itemCount++
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Calculate hit rate
	total := c.hits + c.misses
	hitRate := float64(0)
	missRate := float64(0)
	if total > 0 {
		hitRate = float64(c.hits) / float64(total)
		missRate = float64(c.misses) / float64(total)
	}

	return &CacheStats{
		CurrentSize: c.currentSize,
		MaxSize:     c.maxSize,
		Items:       itemCount,
		HitRate:     hitRate,
		MissRate:    missRate,
		Hits:        c.hits,
		Misses:      c.misses,
	}, nil
}

// Clear removes all items from the cache
func (c *Cache) Clear() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Remove all files
	entries, err := os.ReadDir(c.baseDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		path := filepath.Join(c.baseDir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}

	// Reset state
	c.currentSize = 0
	c.expiration = make(map[string]time.Time)
	c.lru = NewLRUCache(100000)

	return nil
}

// FlushExpired removes expired items from the cache
func (c *Cache) FlushExpired() (int, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	count := 0
	now := time.Now()

	// Find expired items
	expired := make([]string, 0)
	for key, expTime := range c.expiration {
		if now.After(expTime) {
			expired = append(expired, key)
		}
	}

	// Remove expired items
	for _, key := range expired {
		path := c.pathForKey(key)
		info, err := os.Stat(path)
		if err == nil {
			if err := os.Remove(path); err == nil {
				c.currentSize -= info.Size()
				delete(c.expiration, key)
				c.lru.Remove(key)
				count++
			}
		}
	}

	return count, nil
}

// Search finds cache items matching a pattern
func (c *Cache) Search(pattern string) ([]string, error) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	var matches []string

	err := filepath.Walk(c.baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && filepath.Base(path) != "" {
			// Convert to cache key
			relPath, err := filepath.Rel(c.baseDir, path)
			if err != nil {
				return nil // Skip this file
			}

			// Simple substring match for now
			if pattern == "" || containsIgnoreCase(relPath, pattern) {
				matches = append(matches, relPath)
			}
		}
		return nil
	})

	return matches, err
}

// cleanup removes least recently used files when cache is too large
func (c *Cache) cleanup() error {
	// If we're not over the limit, no need to clean
	if c.currentSize <= c.maxSize {
		return nil
	}

	// Calculate how much we need to remove
	toRemove := c.currentSize - (c.maxSize * 9 / 10) // Remove enough to get to 90% of max

	// Get LRU items
	count := 1000 // Try to remove up to 1000 items at once
	lruItems := c.lru.GetLRUItems(count)

	var removed int64
	for _, item := range lruItems {
		path := c.pathForKey(item.key)

		// Skip files that don't exist or are in use
		info, err := os.Stat(path)
		if err != nil {
			c.lru.Remove(item.key)
			continue
		}

		// Skip files that are still fresh (not expired)
		if expTime, exists := c.expiration[item.key]; exists && time.Now().Before(expTime) {
			continue
		}

		// Remove the file
		if err := os.Remove(path); err == nil {
			fileSize := info.Size()
			removed += fileSize
			c.currentSize -= fileSize
			delete(c.expiration, item.key)
			c.lru.Remove(item.key)

			// Check if we've removed enough
			if removed >= toRemove {
				break
			}
		}
	}

	return nil
}

// backgroundCleanup periodically cleans up the cache
func (c *Cache) backgroundCleanup() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		if _, err := c.FlushExpired(); err != nil {
			log.Printf("Error flushing expired cache entries: %v", err)
		}

		c.mutex.Lock()
		if c.currentSize > c.maxSize {
			if err := c.cleanup(); err != nil {
				log.Printf("Error cleaning up cache: %v", err)
			}
		}
		c.mutex.Unlock()
	}
}

// updateCacheSize recalculates the total size of the cache directory
func (c *Cache) updateCacheSize() error {
	var size int64
	err := filepath.Walk(c.baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()

			// Also add to LRU tracking
			relPath, err := filepath.Rel(c.baseDir, path)
			if err == nil {
				c.lru.Add(relPath, info.Size())
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	c.currentSize = size
	return nil
}

// pathForKey converts a cache key to a filesystem path
func (c *Cache) pathForKey(key string) string {
	return filepath.Join(c.baseDir, key)
}

// containsIgnoreCase checks if a string contains a substring (case insensitive)
func containsIgnoreCase(s, substr string) bool {
	s, substr = strings.ToLower(s), strings.ToLower(substr)
	return strings.Contains(s, substr)
}

// ensureDirectoryExists ensures a directory exists for cached files
func ensureDirectoryExists(path string) error {
	// Check if the directory already exists first
	if stat, err := os.Stat(path); err == nil && stat.IsDir() {
		// Directory already exists, no need to create it
		return nil
	}

	// Directory doesn't exist or there was an error checking, try to create it
	if err := os.MkdirAll(path, 0755); err != nil {
		// Check if the directory was created by another process in the meantime
		if stat, statErr := os.Stat(path); statErr == nil && stat.IsDir() {
			// Directory now exists, someone else created it
			return nil
		}
		return fmt.Errorf("failed to create directory %s: %w", path, err)
	}

	return nil
}

// UpdateExpiration updates the expiration time for an existing cache entry
func (c *Cache) UpdateExpiration(key string, expiration time.Duration) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Check if entry exists in the expiration map
	_, exists := c.expiration[key]
	if !exists {
		// Create new expiration entry if it doesn't exist but file does
		path := c.pathForKey(key)
		if _, err := os.Stat(path); err == nil {
			// File exists, create expiration for it
			c.expiration[key] = time.Now().Add(expiration)

			// Update LRU - get file size and add to LRU
			fileInfo, err := os.Stat(path)
			if err == nil {
				c.lru.Add(key, fileInfo.Size())
			}
			return nil
		}
		return fmt.Errorf("cannot update expiration for non-existent cache entry: %s", key)
	}

	// Update expiration time
	c.expiration[key] = time.Now().Add(expiration)
	return nil
}
