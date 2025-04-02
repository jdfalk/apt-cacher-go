package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/parser"
)

// DatabaseStore provides a persistent storage mechanism using PebbleDB
// for package hash mappings, cache state and other persistent data
type DatabaseStore struct {
	db           *pebble.DB
	dbPath       string            // Store the database path
	cache        map[string]string // In-memory cache for hot items
	cacheMutex   sync.RWMutex
	maxCacheSize int
	batchSize    int
	batchMutex   sync.Mutex
	batch        *pebble.Batch
	lastFlush    time.Time
	memoryLimit  int64              // Memory limit in bytes
	cancelFunc   context.CancelFunc // Store cancel function for maintenance routine

	// Cache statistics
	currentSize int64
	maxSize     int64
	hitCount    int64
	missCount   int64
	statsMutex  sync.RWMutex
}

// CacheEntry represents metadata for a cached file
type CacheEntry struct {
	Path         string    `json:"path"`
	Size         int64     `json:"size"`
	LastAccessed time.Time `json:"last_accessed"`
	LastModified time.Time `json:"last_modified"`
	HitCount     int       `json:"hit_count"`
}

// NewDatabaseStore creates a new persistent database store
func NewDatabaseStore(cacheDir string, config *config.Config) (*DatabaseStore, error) {
	dbPath := filepath.Join(cacheDir, "pebbledb")

	// Get memory limits from config
	var maxCacheEntries int = 10000                  // Default
	var maxMemoryMB int64 = 1024                     // Default 1GB
	var maxCacheSize int64 = 10 * 1024 * 1024 * 1024 // Default 10GB

	// Read from config if available
	if config != nil {
		// Parse memory settings from config
		if val, ok := config.Metadata["memory_management.max_cache_size"]; ok {
			if strVal, ok := val.(string); ok {
				// Parse size string like "1024MB"
				if strings.HasSuffix(strVal, "MB") {
					numVal := strings.TrimSuffix(strVal, "MB")
					if parsed, err := strconv.ParseInt(numVal, 10, 64); err == nil {
						maxMemoryMB = parsed
					}
				}
			}
		}

		if val, ok := config.Metadata["memory_management.max_entries"]; ok {
			if numVal, ok := val.(int); ok {
				maxCacheEntries = numVal
			}
		}

		// Parse max cache size
		if config.CacheSize != "" {
			if size, err := parseSize(config.CacheSize); err == nil {
				maxCacheSize = size
			}
		}
	}

	// Set memory limit
	var memLimit int64
	if maxMemoryMB > 0 {
		memLimit = maxMemoryMB * 1024 * 1024 // Convert MB to bytes
		log.Printf("Using configured memory limit: %d MB", maxMemoryMB)
	} else {
		// Auto-detect based on system memory
		memStat := &runtime.MemStats{}
		runtime.ReadMemStats(memStat)
		memLimit = int64(memStat.TotalAlloc / 5)
		memLimit = max(memLimit, 64<<20) // Ensure at least 64MB minimum
		log.Printf("Auto-detected memory limit: %d MB", memLimit/(1024*1024))
	}

	// Open PebbleDB with optimized options
	options := &pebble.Options{
		// Optimize for read-heavy workload with configured memory
		Cache:                    pebble.NewCache(memLimit / 4), // 25% of memory for block cache
		MemTableSize:             uint64(memLimit / 8),          // 12.5% for memtables
		MaxOpenFiles:             256,
		MaxConcurrentCompactions: func() int { return runtime.GOMAXPROCS(0) },
		L0CompactionThreshold:    2,
	}

	log.Printf("Initializing database store with %dMB memory limit, %d max entries",
		memLimit/(1024*1024), maxCacheEntries)

	db, err := pebble.Open(dbPath, options)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	store := &DatabaseStore{
		db:           db,
		dbPath:       dbPath,
		cache:        make(map[string]string, maxCacheEntries),
		maxCacheSize: maxCacheEntries,
		batchSize:    100,
		batch:        db.NewBatch(),
		lastFlush:    time.Now(),
		memoryLimit:  memLimit,
		maxSize:      maxCacheSize,
	}

	// Load existing cache state
	err = store.loadCacheState()
	if err != nil {
		log.Printf("Failed to load cache state: %v", err)
	}

	// Start maintenance routine
	ctx, cancel := context.WithCancel(context.Background())
	store.cancelFunc = cancel
	go func() {
		<-ctx.Done()
		log.Printf("Maintenance routine stopping due to context cancellation")
	}()
	store.StartMaintenanceRoutine(ctx)

	return store, nil
}

// loadCacheState loads existing cache state from the database
func (ds *DatabaseStore) loadCacheState() error {
	// Load statistics
	ds.statsMutex.Lock()
	defer ds.statsMutex.Unlock()

	// Get current size
	sizeBytes, closer, err := ds.db.Get([]byte("s:current_size"))
	if err == nil {
		ds.currentSize, _ = strconv.ParseInt(string(sizeBytes), 10, 64)
		closer.Close()
	}

	// Get hit count
	hitBytes, closer, err := ds.db.Get([]byte("s:hit_count"))
	if err == nil {
		ds.hitCount, _ = strconv.ParseInt(string(hitBytes), 10, 64)
		closer.Close()
	}

	// Get miss count
	missBytes, closer, err := ds.db.Get([]byte("s:miss_count"))
	if err == nil {
		ds.missCount, _ = strconv.ParseInt(string(missBytes), 10, 64)
		closer.Close()
	}

	log.Printf("Cache state loaded successfully (size: %d bytes, hits: %d, misses: %d)",
		ds.currentSize, ds.hitCount, ds.missCount)

	return nil
}

// saveCacheState saves the current cache state to the database
func (ds *DatabaseStore) saveCacheState() error {
	ds.statsMutex.RLock()
	currentSize := ds.currentSize
	hitCount := ds.hitCount
	missCount := ds.missCount
	ds.statsMutex.RUnlock()

	// Use a batch for atomic updates
	batch := ds.db.NewBatch()
	defer batch.Close()

	// Save statistics
	if err := batch.Set([]byte("s:current_size"), []byte(strconv.FormatInt(currentSize, 10)), nil); err != nil {
		return err
	}
	if err := batch.Set([]byte("s:hit_count"), []byte(strconv.FormatInt(hitCount, 10)), nil); err != nil {
		return err
	}
	if err := batch.Set([]byte("s:miss_count"), []byte(strconv.FormatInt(missCount, 10)), nil); err != nil {
		return err
	}

	// Commit the batch
	return ds.db.Apply(batch, pebble.Sync)
}

// SaveCacheState exports the cache state saving functionality
func (ds *DatabaseStore) SaveCacheState() error {
	return ds.saveCacheState()
}

// GetCacheEntry retrieves cache metadata for a path
func (ds *DatabaseStore) GetCacheEntry(path string) (CacheEntry, bool, error) {
	key := "c:" + path
	data, closer, err := ds.db.Get([]byte(key))
	if err != nil {
		if err == pebble.ErrNotFound {
			return CacheEntry{}, false, nil
		}
		return CacheEntry{}, false, err
	}
	defer closer.Close()

	var entry CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return CacheEntry{}, false, err
	}

	return entry, true, nil
}

// SaveCacheEntry stores cache metadata for a path
func (ds *DatabaseStore) SaveCacheEntry(entry CacheEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	key := "c:" + entry.Path

	// Update cache size statistics
	ds.statsMutex.Lock()
	ds.currentSize += entry.Size
	ds.statsMutex.Unlock()

	// Save the entry
	return ds.db.Set([]byte(key), data, nil)
}

// DeleteCacheEntry removes a cache entry
func (ds *DatabaseStore) DeleteCacheEntry(path string) error {
	key := "c:" + path

	// Get current entry to update size
	entry, exists, err := ds.GetCacheEntry(path)
	if err != nil {
		return err
	}

	if exists {
		// Update cache size statistics
		ds.statsMutex.Lock()
		ds.currentSize -= entry.Size
		if ds.currentSize < 0 {
			ds.currentSize = 0
		}
		ds.statsMutex.Unlock()
	}

	// Delete the entry
	return ds.db.Delete([]byte(key), nil)
}

// RecordCacheHit records a cache hit
func (ds *DatabaseStore) RecordCacheHit(path string) error {
	entry, exists, err := ds.GetCacheEntry(path)
	if err != nil {
		return err
	}

	if !exists {
		return fmt.Errorf("cache entry not found: %s", path)
	}

	// Update hit count and access time
	entry.HitCount++
	entry.LastAccessed = time.Now()

	// Update statistics
	ds.statsMutex.Lock()
	ds.hitCount++
	ds.statsMutex.Unlock()

	// Save updated entry
	return ds.SaveCacheEntry(entry)
}

// RecordCacheMiss records a cache miss
func (ds *DatabaseStore) RecordCacheMiss() {
	ds.statsMutex.Lock()
	ds.missCount++
	ds.statsMutex.Unlock()
}

// GetStats returns cache statistics
func (ds *DatabaseStore) GetStats() (map[string]interface{}, error) {
	ds.statsMutex.RLock()
	defer ds.statsMutex.RUnlock()

	stats := map[string]interface{}{
		"currentSize": ds.currentSize,
		"maxSize":     ds.maxSize,
		"hitCount":    ds.hitCount,
		"missCount":   ds.missCount,
	}

	// Calculate hit rate
	totalRequests := ds.hitCount + ds.missCount
	if totalRequests > 0 {
		stats["hitRate"] = float64(ds.hitCount) / float64(totalRequests)
	} else {
		stats["hitRate"] = 0.0
	}

	return stats, nil
}

// ListCacheEntries lists all cache entries matching a pattern
func (ds *DatabaseStore) ListCacheEntries(pattern string) ([]CacheEntry, error) {
	var entries []CacheEntry

	// Create an iterator with prefix "c:"
	iter, err := ds.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte("c:"),
		UpperBound: []byte("c:\xff"),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	// Iterate through cache entries
	for iter.First(); iter.Valid(); iter.Next() {
		key := string(iter.Key())
		path := strings.TrimPrefix(key, "c:")

		// Skip if pattern doesn't match
		if pattern != "" && !strings.Contains(path, pattern) {
			continue
		}

		var entry CacheEntry
		if err := json.Unmarshal(iter.Value(), &entry); err != nil {
			log.Printf("Error unmarshaling cache entry for %s: %v", path, err)
			continue
		}

		entries = append(entries, entry)
	}

	return entries, iter.Error()
}

// AddHashMapping adds a hash->package mapping
func (ds *DatabaseStore) AddHashMapping(hash, packageName string) error {
	// First update memory cache
	ds.cacheMutex.Lock()
	if len(ds.cache) >= ds.maxCacheSize {
		// Clear cache when it gets too big
		ds.cache = make(map[string]string, ds.maxCacheSize)
	}
	ds.cache[hash] = packageName
	ds.cacheMutex.Unlock()

	// Add to disk batch with hash prefix
	key := "h:" + hash
	ds.batchMutex.Lock()
	defer ds.batchMutex.Unlock()

	if err := ds.batch.Set([]byte(key), []byte(packageName), nil); err != nil {
		return err
	}

	// Commit batch periodically
	if uint32(ds.batch.Count()) >= uint32(ds.batchSize) || time.Since(ds.lastFlush) > 5*time.Second {
		if err := ds.db.Apply(ds.batch, pebble.Sync); err != nil {
			return err
		}
		ds.batch.Close()
		ds.batch = ds.db.NewBatch()
		ds.lastFlush = time.Now()
	}

	return nil
}

// GetPackageNameForHash returns the package name for a hash
func (ds *DatabaseStore) GetPackageNameForHash(path string) string {
	// Extract hash from path
	if !strings.Contains(path, "/by-hash/") {
		return ""
	}

	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return ""
	}
	hash := parts[len(parts)-1]

	// Check memory cache first
	ds.cacheMutex.RLock()
	pkg, found := ds.cache[hash]
	ds.cacheMutex.RUnlock()

	if found {
		return pkg
	}

	// Not in cache, check database with hash prefix
	key := "h:" + hash
	value, closer, err := ds.db.Get([]byte(key))
	if err != nil {
		return ""
	}
	defer closer.Close()

	packageName := string(value)

	// Add to memory cache
	ds.cacheMutex.Lock()
	if len(ds.cache) >= ds.maxCacheSize {
		ds.cache = make(map[string]string, ds.maxCacheSize)
	}
	ds.cache[hash] = packageName
	ds.cacheMutex.Unlock()

	return packageName
}

// Put adds a file to the cache
func (ds *DatabaseStore) Put(path string, data []byte) error {
	// Calculate absolute path first
	absPath := path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(ds.dbPath, "..", path)
	}

	// Create filesystem directory if needed
	dir := filepath.Dir(absPath)
	if dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	// Write the file to the filesystem
	if err := os.WriteFile(absPath, data, 0644); err != nil {
		return err
	}

	// Create cache entry
	entry := CacheEntry{
		Path:         path,
		Size:         int64(len(data)),
		LastAccessed: time.Now(),
		LastModified: time.Now(),
		HitCount:     1,
	}

	// Save entry to database
	return ds.SaveCacheEntry(entry)
}

// Get retrieves a file from the cache
func (ds *DatabaseStore) Get(path string) ([]byte, error) {
	// Check if entry exists
	_, exists, err := ds.GetCacheEntry(path)
	if err != nil {
		return nil, err
	}

	if !exists {
		ds.RecordCacheMiss()
		return nil, fmt.Errorf("file not in cache: %s", path)
	}

	// Record hit
	if err := ds.RecordCacheHit(path); err != nil {
		log.Printf("Error recording cache hit: %v", err)
	}

	// Read file from filesystem
	absPath := path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(ds.dbPath, "..", path)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}

	return data, nil
}

// Exists checks if a file exists in the cache
func (ds *DatabaseStore) Exists(path string) bool {
	_, exists, _ := ds.GetCacheEntry(path)
	return exists
}

// parseSize converts string size representations like "10GB" or "1024MB" to bytes
func parseSize(sizeStr string) (int64, error) {
	sizeStr = strings.TrimSpace(sizeStr)
	var multiplier int64 = 1

	if strings.HasSuffix(sizeStr, "GB") {
		multiplier = 1024 * 1024 * 1024
		sizeStr = strings.TrimSuffix(sizeStr, "GB")
	} else if strings.HasSuffix(sizeStr, "MB") {
		multiplier = 1024 * 1024
		sizeStr = strings.TrimSuffix(sizeStr, "MB")
	} else if strings.HasSuffix(sizeStr, "KB") {
		multiplier = 1024
		sizeStr = strings.TrimSuffix(sizeStr, "KB")
	} else if strings.HasSuffix(sizeStr, "B") {
		sizeStr = strings.TrimSuffix(sizeStr, "B")
	}

	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size format: %s", sizeStr)
	}

	return size * multiplier, nil
}

// StartMaintenanceRoutine begins periodic maintenance tasks like cache pruning
func (ds *DatabaseStore) StartMaintenanceRoutine(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				log.Println("Running cache maintenance routine")
				if err := ds.pruneCacheIfNeeded(); err != nil {
					log.Printf("Error during cache maintenance: %v", err)
				}
				if err := ds.saveCacheState(); err != nil {
					log.Printf("Error saving cache state: %v", err)
				}
			case <-ctx.Done():
				log.Println("Stopping maintenance routine")
				return
			}
		}
	}()
}

// pruneCacheIfNeeded removes least recently used items if cache exceeds size limits
func (ds *DatabaseStore) pruneCacheIfNeeded() error {
	ds.statsMutex.RLock()
	currentSize := ds.currentSize
	maxSize := ds.maxSize
	ds.statsMutex.RUnlock()

	// Only prune if we're over the limit
	if currentSize <= maxSize {
		return nil
	}

	// Get all entries and sort by last accessed
	entries, err := ds.ListCacheEntries("")
	if err != nil {
		return err
	}

	// Sort entries by last accessed time (oldest first)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].LastAccessed.Before(entries[j].LastAccessed)
	})

	// Remove oldest entries until we're under the limit
	var bytesRemoved int64
	for _, entry := range entries {
		if currentSize-bytesRemoved <= maxSize*9/10 { // Aim for 90% of max
			break
		}

		// Delete the file
		absPath := filepath.Join(ds.dbPath, "..", entry.Path)
		if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
			log.Printf("Failed to delete cache file %s: %v", absPath, err)
			continue
		}

		// Delete the entry
		if err := ds.DeleteCacheEntry(entry.Path); err != nil {
			log.Printf("Failed to delete cache entry %s: %v", entry.Path, err)
			continue
		}

		bytesRemoved += entry.Size
	}

	log.Printf("Cache pruning completed, removed %d bytes", bytesRemoved)
	return nil
}

// Close properly shuts down the database, flushing any pending writes and stopping
// maintenance routines
func (ds *DatabaseStore) Close() error {
	// Cancel maintenance routine if running
	if ds.cancelFunc != nil {
		ds.cancelFunc()
	}

	// Flush any pending batch operations
	ds.batchMutex.Lock()
	if ds.batch != nil && ds.batch.Count() > 0 {
		// Apply final batch
		err := ds.db.Apply(ds.batch, pebble.Sync)
		ds.batch.Close()
		ds.batchMutex.Unlock()
		if err != nil {
			return fmt.Errorf("error flushing final batch: %w", err)
		}
	} else {
		if ds.batch != nil {
			ds.batch.Close()
		}
		ds.batchMutex.Unlock()
	}

	// Save final cache state
	if err := ds.saveCacheState(); err != nil {
		log.Printf("Error saving final cache state: %v", err)
	}

	// Close the database
	if ds.db != nil {
		return ds.db.Close()
	}
	return nil
}

// StorePackageInfo stores package information in the database
func (ds *DatabaseStore) StorePackageInfo(pkg parser.PackageInfo) error {
	data, err := json.Marshal(pkg)
	if err != nil {
		return err
	}

	key := "p:" + pkg.Package
	return ds.db.Set([]byte(key), data, nil)
}

// GetPackageInfo retrieves package information from the database
func (ds *DatabaseStore) GetPackageInfo(packageName string) (parser.PackageInfo, bool, error) {
	key := "p:" + packageName
	data, closer, err := ds.db.Get([]byte(key))
	if err != nil {
		if err == pebble.ErrNotFound {
			return parser.PackageInfo{}, false, nil
		}
		return parser.PackageInfo{}, false, err
	}
	defer closer.Close()

	var pkg parser.PackageInfo
	if err := json.Unmarshal(data, &pkg); err != nil {
		return parser.PackageInfo{}, false, err
	}

	return pkg, true, nil
}

// ListPackages returns a list of all packages
func (ds *DatabaseStore) ListPackages(pattern string) ([]parser.PackageInfo, error) {
	var packages []parser.PackageInfo

	// Create an iterator with prefix "p:"
	iter, err := ds.db.NewIter(&pebble.IterOptions{
		LowerBound: []byte("p:"),
		UpperBound: []byte("p:\xff"),
	})
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	// Iterate through package entries
	for iter.First(); iter.Valid(); iter.Next() {
		key := string(iter.Key())
		packageName := strings.TrimPrefix(key, "p:")

		// Skip if pattern doesn't match
		if pattern != "" && !strings.Contains(packageName, pattern) {
			continue
		}

		var pkg parser.PackageInfo
		if err := json.Unmarshal(iter.Value(), &pkg); err != nil {
			log.Printf("Error unmarshaling package info for %s: %v", packageName, err)
			continue
		}

		packages = append(packages, pkg)
	}

	return packages, iter.Error()
}

// The rest of the implementation remains similar to the existing methods
// with minor adjustments to work with the new data model
