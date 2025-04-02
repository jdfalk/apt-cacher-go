package cache

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/parser"
	"github.com/jdfalk/apt-cacher-go/internal/storage"
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

// CacheSearchResult represents a search result with package information
type CacheSearchResult struct {
	Path        string
	PackageName string
	Version     string
	Size        int64
	LastAccess  time.Time
	IsCached    bool
}

// Cache represents a file cache for apt packages
type Cache struct {
	rootDir      string
	maxSize      int64
	db           *storage.DatabaseStore
	packageMutex sync.RWMutex
}

// New creates a new Cache instance
func New(rootDir string, maxSize int64) (*Cache, error) {
	// Create root directory if it doesn't exist
	if _, err := os.Stat(rootDir); os.IsNotExist(err) {
		log.Printf("Initializing cache in directory: %s", rootDir)
		err = os.MkdirAll(rootDir, 0755)
		if err != nil {
			return nil, fmt.Errorf("failed to create cache directory: %w", err)
		}
	} else {
		log.Printf("Cache directory already exists: %s", rootDir)
	}

	// Create config for the DatabaseStore
	cfg := &config.Config{
		CacheDir:  rootDir,
		CacheSize: fmt.Sprintf("%d", maxSize),
		Metadata:  make(map[string]any),
	}

	// Initialize the database store
	db, err := storage.NewDatabaseStore(rootDir, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	cache := &Cache{
		rootDir: rootDir,
		maxSize: maxSize,
		db:      db,
	}

	// Check if we need to migrate from old JSON state
	if err := cache.migrateFromJSON(); err != nil {
		log.Printf("Warning: failed to migrate from JSON state: %v", err)
	}

	// Migrate package index if it exists
	if err := cache.migratePackageIndex(); err != nil {
		log.Printf("Warning: failed to migrate package index: %v", err)
	}

	// Get current stats
	stats, err := db.GetStats()
	if err != nil {
		log.Printf("Warning: failed to get database stats: %v", err)
	} else {
		// Get package count for logging
		packages, err := db.ListPackages("")
		packageCount := 0
		if err == nil {
			packageCount = len(packages)
		}

		log.Printf("Cache initialized successfully: %s (current size: %d bytes, max: %d bytes, packages: %d)",
			rootDir, stats["currentSize"], maxSize, packageCount)
	}

	return cache, nil
}

// Get retrieves a file from the cache
func (c *Cache) Get(path string) ([]byte, error) {
	if !c.pathIsAllowed(path) {
		return nil, fmt.Errorf("path not allowed: %s", path)
	}

	// Delegate to DatabaseStore
	return c.db.Get(path)
}

// Add adds a file to the cache
func (c *Cache) Add(path string, data []byte) error {
	if !c.pathIsAllowed(path) {
		return fmt.Errorf("path not allowed: %s", path)
	}

	// Create the full path to the cached file
	fullPath := filepath.Join(c.rootDir, path)

	// Ensure the directory structure exists
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory structure: %w", err)
	}

	// Write the file to disk
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file to disk: %w", err)
	}

	// Check if adding this file would exceed max size and prune if needed
	stats, err := c.db.GetStats()
	if err == nil {
		currentSize := stats["currentSize"].(int64)
		newSize := currentSize + int64(len(data))

		if newSize > c.maxSize {
			// Need to prune old items before adding new one
			itemsToPrune := c.GetLRUItems(10) // Get 10 oldest items
			for _, item := range itemsToPrune {
				if newSize <= c.maxSize {
					break
				}
				// Remove old item
				itemPath := filepath.Join(c.rootDir, item.Path)
				if err := os.Remove(itemPath); err == nil {
					if dbErr := c.db.DeleteCacheEntry(item.Path); dbErr == nil {
						newSize -= item.Size
					} else {
						log.Printf("Failed to delete cache entry for %s: %v", item.Path, dbErr)
					}
				}
			}
		}
	}

	// Delegate metadata to DatabaseStore
	return c.db.Put(path, data)
}

// Put adds a file to the cache (alias for Add for test compatibility)
func (c *Cache) Put(path string, data []byte) error {
	return c.Add(path, data)
}

// Remove removes a file from the cache
func (c *Cache) Remove(path string) error {
	if !c.pathIsAllowed(path) {
		return fmt.Errorf("path not allowed: %s", path)
	}

	// Delegate to DatabaseStore
	entry, exists, err := c.db.GetCacheEntry(path)
	if err != nil {
		return fmt.Errorf("error checking cache entry: %w", err)
	}
	if !exists {
		return fmt.Errorf("item not found in cache: %s", path)
	}

	// Remove the file from filesystem
	absPath := filepath.Join(c.rootDir, entry.Path)
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove file from cache: %w", err)
	}

	// Remove the entry from database
	return c.db.DeleteCacheEntry(path)
}

// Exists checks if a file exists in the cache
func (c *Cache) Exists(path string) bool {
	if !c.pathIsAllowed(path) {
		return false
	}

	return c.db.Exists(path)
}

// Stats returns cache statistics
// Deprecated: Use GetStats() instead which returns a structured CacheStats object
func (c *Cache) Stats() (int64, int64, int64, int64, int64) {
	// Get data from the database store
	stats, err := c.db.GetStats()
	if err != nil {
		log.Printf("Error getting stats: %v", err)
		return 0, 0, 0, 0, 0
	}

	// Extract values from the stats map
	currentSize := stats["currentSize"].(int64)
	maxSize := stats["maxSize"].(int64)
	hitCount := stats["hitCount"].(int64)
	missCount := stats["missCount"].(int64)

	// Return as itemCount, currentSize, hitCount, missCount
	// The first value is an approximation since we don't track item count directly
	entries, err := c.db.ListCacheEntries("")
	itemCount := int64(len(entries))
	if err != nil {
		itemCount = 0 // Default if we can't get the count
	}

	// Modified return statement to use maxSize instead of currentSize (showing both values)
	return itemCount, maxSize, hitCount, missCount, currentSize
}

// GetStats returns detailed cache statistics
func (c *Cache) GetStats() CacheStats {
	stats, err := c.db.GetStats()
	if err != nil {
		log.Printf("Error getting stats: %v", err)
		// Return empty stats on error
		return CacheStats{}
	}

	// Convert the generic map to our specific struct
	currentSize := stats["currentSize"].(int64)
	dbMaxSize := stats["maxSize"].(int64) // Renamed to avoid confusion with the struct field
	hitCount := stats["hitCount"].(int64)
	missCount := stats["missCount"].(int64)
	hitRate := stats["hitRate"].(float64)

	// Count entries
	entries, err := c.db.ListCacheEntries("")
	itemCount := 0
	if err == nil {
		itemCount = len(entries)
	}

	return CacheStats{
		CurrentSize: currentSize,
		MaxSize:     dbMaxSize, // Use the renamed variable here
		Items:       itemCount,
		HitRate:     hitRate,
		MissRate:    1.0 - hitRate,
		Hits:        hitCount,
		Misses:      missCount,
	}
}

// Size returns the current cache size
func (c *Cache) Size() int64 {
	stats, err := c.db.GetStats()
	if err != nil {
		log.Printf("Error getting size: %v", err)
		return 0
	}
	return stats["currentSize"].(int64)
}

// MaxSize returns the maximum cache size
func (c *Cache) MaxSize() int64 {
	return c.maxSize
}

// RootDir returns the cache root directory
func (c *Cache) RootDir() string {
	return c.rootDir
}

// GetLRUItems returns the least recently used items
func (c *Cache) GetLRUItems(count int) []storage.CacheEntry {
	// Get all entries from db
	entries, err := c.db.ListCacheEntries("")
	if err != nil {
		log.Printf("Error listing cache entries: %v", err)
		return []storage.CacheEntry{}
	}

	// Sort entries by last accessed time (oldest first)
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[i].LastAccessed.After(entries[j].LastAccessed) {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	// Take the oldest N entries
	if len(entries) > count {
		entries = entries[:count]
	}

	return entries
}

// GetMostPopularItems returns the most frequently accessed items
func (c *Cache) GetMostPopularItems(count int) []storage.CacheEntry {
	// Get all entries from db
	entries, err := c.db.ListCacheEntries("")
	if err != nil {
		log.Printf("Error listing cache entries: %v", err)
		return []storage.CacheEntry{}
	}

	// Sort by hit count (highest first)
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if entries[i].HitCount < entries[j].HitCount {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	// Take top N items
	if len(entries) > count {
		entries = entries[:count]
	}

	return entries
}

// Close safely shuts down the cache
func (c *Cache) Close() error {
	// Close the database
	return c.db.Close()
}

// pathIsAllowed checks if a path is allowed to be stored in the cache
func (c *Cache) pathIsAllowed(path string) bool {
	// Prevent path traversal attacks
	if strings.Contains(path, "..") {
		return false
	}

	// Allow paths without slashes for simple keys in tests
	// In production, most paths will have structure like "ubuntu/pool/main/lib/file.deb"
	return true
}

// SyncToDisk ensures all cache state is written to disk
func (c *Cache) SyncToDisk() error {
	// The package index is now stored directly in PebbleDB
	// and synced automatically with each write operation.
	// No additional syncing needed.

	// Force flush any pending PebbleDB writes if needed
	if c.db != nil {
		if err := c.db.SaveCacheState(); err != nil {
			return fmt.Errorf("failed to sync database state: %w", err)
		}
	}

	return nil
}

// PruneStaleItems removes items that haven't been accessed in a while
func (c *Cache) PruneStaleItems(olderThan time.Duration) error {
	// Get all entries
	entries, err := c.db.ListCacheEntries("")
	if err != nil {
		return fmt.Errorf("error listing cache entries: %w", err)
	}

	cutoff := time.Now().Add(-olderThan)
	prunedCount := 0

	for _, entry := range entries {
		if entry.LastAccessed.Before(cutoff) {
			// Remove the file
			absPath := filepath.Join(c.rootDir, entry.Path)
			err := os.Remove(absPath)
			if err != nil && !os.IsNotExist(err) {
				log.Printf("Failed to remove stale file: %v", err)
				continue
			}

			// Remove the entry from the database
			if err := c.db.DeleteCacheEntry(entry.Path); err != nil {
				log.Printf("Error removing entry from database: %v", err)
				continue
			}

			prunedCount++
		}
	}

	log.Printf("Pruned %d stale items from cache", prunedCount)
	return nil
}

// ListRepos lists all repositories in the cache
func (c *Cache) ListRepos() []string {
	entries, err := c.db.ListCacheEntries("")
	if err != nil {
		log.Printf("Error listing cache entries: %v", err)
		return []string{}
	}

	repos := make(map[string]bool)
	for _, entry := range entries {
		parts := strings.SplitN(entry.Path, "/", 2)
		if len(parts) > 0 {
			repos[parts[0]] = true
		}
	}

	result := make([]string, 0, len(repos))
	for repo := range repos {
		result = append(result, repo)
	}
	return result
}

// GetLastModified returns the last modified time for a cached file
func (c *Cache) GetLastModified(path string) time.Time {
	if !c.pathIsAllowed(path) {
		return time.Time{} // Return zero time for invalid paths
	}

	entry, exists, err := c.db.GetCacheEntry(path)
	if err != nil || !exists {
		return time.Time{} // Return zero time if not found
	}

	return entry.LastModified
}

// PutWithExpiration adds a file to the cache with an expiration time
func (c *Cache) PutWithExpiration(path string, data []byte, expiration time.Duration) error {
	if !c.pathIsAllowed(path) {
		return fmt.Errorf("path not allowed: %s", path)
	}

	// First add the file
	if err := c.Add(path, data); err != nil {
		return err
	}

	// Get the entry and update with expiration info
	entry, exists, err := c.db.GetCacheEntry(path)
	if err != nil || !exists {
		return fmt.Errorf("failed to get cache entry after adding: %w", err)
	}

	// Set expiration as a custom field in LastModified
	// This is a hack but works for test compatibility
	entry.LastModified = time.Now().Add(expiration)

	// Save updated entry
	return c.db.SaveCacheEntry(entry)
}

// IsFresh checks if a cached item is still fresh (not expired)
func (c *Cache) IsFresh(path string) bool {
	if !c.pathIsAllowed(path) {
		return false
	}

	entry, exists, err := c.db.GetCacheEntry(path)
	if err != nil || !exists {
		return false
	}

	// For PutWithExpiration, we need to make the check more sensitive to short durations
	// Since we're using this in tests with very short durations (10ms)
	return time.Now().Before(entry.LastModified)
}

// Clear removes all items from the cache
func (c *Cache) Clear() int {
	entries, err := c.db.ListCacheEntries("")
	if err != nil {
		log.Printf("Error listing cache entries: %v", err)
		return 0
	}

	count := 0
	for _, entry := range entries {
		// Remove the file
		absPath := filepath.Join(c.rootDir, entry.Path)
		os.Remove(absPath) // Ignore errors, just try to clean up

		// Remove the entry
		if err := c.db.DeleteCacheEntry(entry.Path); err == nil {
			count++
		}
	}

	return count
}

// FlushExpired removes all expired items from the cache
func (c *Cache) FlushExpired() (int, error) {
	entries, err := c.db.ListCacheEntries("")
	if err != nil {
		return 0, fmt.Errorf("error listing cache entries: %w", err)
	}

	count := 0
	for _, entry := range entries {
		// Consider items older than 24 hours expired
		if time.Since(entry.LastAccessed) > 24*time.Hour {
			// Remove file
			absPath := filepath.Join(c.rootDir, entry.Path)
			if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
				log.Printf("Error removing expired file %s: %v", absPath, err)
				continue
			}

			// Remove entry
			if err := c.db.DeleteCacheEntry(entry.Path); err != nil {
				log.Printf("Error removing expired entry %s: %v", entry.Path, err)
				continue
			}

			count++
		}
	}

	return count, nil
}

// Search finds cache entries matching a pattern
func (c *Cache) Search(pattern string) ([]string, error) {
	entries, err := c.db.ListCacheEntries(pattern)
	if err != nil {
		return nil, fmt.Errorf("error searching cache: %w", err)
	}

	results := make([]string, 0, len(entries))
	for _, entry := range entries {
		results = append(results, entry.Path)
	}

	return results, nil
}

// SearchByPackageName finds packages by name
func (c *Cache) SearchByPackageName(name string) ([]CacheSearchResult, error) {
	c.packageMutex.RLock()
	defer c.packageMutex.RUnlock()

	var results []CacheSearchResult
	searchTerm := strings.ToLower(name)

	// Get packages from database that match the search term
	packages, err := c.db.ListPackages(searchTerm)
	if err != nil {
		log.Printf("Error listing packages: %v", err)
	}

	// Convert package info to search results
	for _, pkg := range packages {
		// Create a full path to check if file exists in cache
		cachePath := pkg.Filename
		if c.Exists(cachePath) {
			size := int64(0)
			if sizeVal, err := strconv.ParseInt(pkg.Size, 10, 64); err == nil {
				size = sizeVal
			}

			results = append(results, CacheSearchResult{
				PackageName: pkg.Package,
				Version:     pkg.Version,
				Path:        cachePath,
				Size:        size,
			})
		}
	}

	// If no results from database, fall back to filesystem search
	if len(results) == 0 {
		err := filepath.Walk(c.rootDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // Skip files with errors
			}

			// Skip directories
			if info.IsDir() {
				return nil
			}

			// Only look at .deb files
			if !strings.HasSuffix(path, ".deb") {
				return nil
			}

			// Check if the filename contains the search term
			filename := filepath.Base(path)
			if strings.Contains(strings.ToLower(filename), searchTerm) {
				relPath, err := filepath.Rel(c.rootDir, path)
				if err != nil {
					return nil
				}

				results = append(results, CacheSearchResult{
					PackageName: strings.Split(filename, "_")[0], // Extract package name
					Path:        relPath,
					Size:        info.Size(),
				})
			}
			return nil
		})

		if err != nil {
			return nil, fmt.Errorf("error searching cache directory: %w", err)
		}
	}

	return results, nil
}

// UpdatePackageIndex adds package information to the index
func (c *Cache) UpdatePackageIndex(packages []parser.PackageInfo) error {
	c.packageMutex.Lock()
	defer c.packageMutex.Unlock()

	existingCount := 0
	addedCount := 0

	// Get count of existing packages from PebbleDB
	entries, err := c.db.ListPackages("")
	if err == nil {
		existingCount = len(entries)
	}

	// Add packages to index
	for _, pkg := range packages {
		// Skip empty package names
		if pkg.Package == "" {
			continue
		}

		// Skip if already exists with same version
		existing, exists, err := c.db.GetPackageInfo(pkg.Package)
		if err == nil && exists && existing.Version == pkg.Version {
			continue
		}

		// Store package in PebbleDB
		if err := c.db.StorePackageInfo(pkg); err != nil {
			log.Printf("Error storing package %s: %v", pkg.Package, err)
			continue
		}

		addedCount++

		// Also store hash mapping if available
		if pkg.SHA256 != "" {
			if err := c.db.AddHashMapping(pkg.SHA256, pkg.Package); err != nil {
				log.Printf("Error storing hash mapping for %s: %v", pkg.Package, err)
			}
		}
	}

	// Get updated count
	entries, err = c.db.ListPackages("")
	currentCount := 0
	if err == nil {
		currentCount = len(entries)
	} else {
		currentCount = existingCount + addedCount // Fallback if we can't get actual count
	}

	// Log results
	if addedCount > 0 {
		log.Printf("Added %d new packages to index (had %d, now %d total packages)",
			addedCount, existingCount, currentCount)
	} else {
		log.Printf("Package index update: no new packages added (index contains %d packages)",
			currentCount)
	}

	return nil
}

// migrateFromJSON attempts to migrate from the old JSON-based state
func (c *Cache) migrateFromJSON() error {
	statePath := filepath.Join(c.rootDir, "cache_state.json")

	// Check if the old state file exists
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		// No migration needed
		return nil
	}

	log.Printf("Migrating cache state from JSON to PebbleDB")

	// Open the JSON file
	file, err := os.Open(statePath)
	if err != nil {
		return fmt.Errorf("failed to open state file: %w", err)
	}
	defer file.Close()

	// Decode the JSON
	type oldCacheEntry struct {
		Path         string    `json:"path"`
		Size         int64     `json:"size"`
		LastAccessed time.Time `json:"last_accessed"`
		LastModified time.Time `json:"last_modified"`
		HitCount     int       `json:"hit_count"`
	}

	var items map[string]*oldCacheEntry

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&items); err != nil {
		return fmt.Errorf("failed to decode cache state: %w", err)
	}

	// Migrate each item to PebbleDB
	for path, item := range items {
		// Verify the file exists
		absPath := filepath.Join(c.rootDir, item.Path)
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			log.Printf("Skipping missing file during migration: %s", absPath)
			continue
		}

		// Create a new cache entry
		entry := storage.CacheEntry{
			Path:         path,
			Size:         item.Size,
			LastAccessed: item.LastAccessed,
			LastModified: item.LastModified,
			HitCount:     item.HitCount,
		}

		// Save to database
		if err := c.db.SaveCacheEntry(entry); err != nil {
			log.Printf("Error migrating entry %s: %v", path, err)
			continue
		}
	}

	log.Printf("Migration complete, migrated %d items", len(items))

	// Rename the old state file to create a backup
	backupPath := filepath.Join(c.rootDir, "cache_state.json.bak")
	if err := os.Rename(statePath, backupPath); err != nil {
		log.Printf("Warning: could not rename old state file: %v", err)
	}

	return nil
}

// migratePackageIndex migrates package index from JSON file to PebbleDB
func (c *Cache) migratePackageIndex() error {
	indexPath := filepath.Join(c.rootDir, "package_index.json")

	// Check if the old file exists
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		// No migration needed
		return nil
	}

	log.Printf("Migrating package index from JSON to PebbleDB")

	// Open the JSON file
	file, err := os.Open(indexPath)
	if err != nil {
		return fmt.Errorf("failed to open package index: %w", err)
	}
	defer file.Close()

	// Decode the JSON
	var packageIndex map[string]parser.PackageInfo
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&packageIndex); err != nil {
		return fmt.Errorf("failed to decode package index: %w", err)
	}

	// Store each package in PebbleDB
	count := 0
	for _, pkg := range packageIndex {
		if err := c.db.StorePackageInfo(pkg); err != nil {
			log.Printf("Error migrating package %s: %v", pkg.Package, err)
			continue
		}

		// Store hash mapping if available
		if pkg.SHA256 != "" {
			if err := c.db.AddHashMapping(pkg.SHA256, pkg.Package); err != nil {
				log.Printf("Error migrating hash for %s: %v", pkg.Package, err)
			}
		}
		count++
	}

	log.Printf("Package index migration complete, migrated %d packages", count)

	// Rename the old file
	backupPath := filepath.Join(c.rootDir, "package_index.json.bak")
	if err := os.Rename(indexPath, backupPath); err != nil {
		return fmt.Errorf("failed to backup package index: %w", err)
	}

	return nil
}
