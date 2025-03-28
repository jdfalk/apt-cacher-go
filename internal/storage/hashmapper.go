package storage

import (
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
)

// PersistentPackageMapper provides persistent storage for package hash mappings
type PersistentPackageMapper struct {
	db           *pebble.DB
	cache        map[string]string // In-memory cache for hot items
	cacheMutex   sync.RWMutex
	maxCacheSize int
	batchSize    int
	batchMutex   sync.Mutex
	batch        *pebble.Batch
	lastFlush    time.Time
	memoryLimit  int64 // Memory limit in bytes
}

// NewPersistentPackageMapper creates a new persistent hash mapper
func NewPersistentPackageMapper(cacheDir string) (*PersistentPackageMapper, error) {
	dbPath := filepath.Join(cacheDir, "hashmappings")

	// Set a memory limit based on system memory
	var memLimit int64
	memStat := &runtime.MemStats{}
	runtime.ReadMemStats(memStat)

	// Use up to 20% of total memory, max 1GB
	memLimit = int64(memStat.TotalAlloc / 5)
	if memLimit > 1<<30 { // 1GB
		memLimit = 1 << 30
	}
	if memLimit < 64<<20 { // 64MB minimum
		memLimit = 64 << 20
	}

	// Open PebbleDB with optimized options
	options := &pebble.Options{
		// Optimize for read-heavy workload
		Cache: pebble.NewCache(int64(64 * 1024 * 1024)), // 64MB cache
		// Memory budget (adjust based on system memory)
		MemTableSize: uint64(memLimit / 8), // FIX: Type conversion to uint64
		// Set reasonable limits
		MaxOpenFiles: 256,
		// FIX: Provide a function instead of a direct value
		MaxConcurrentCompactions: func() int { return runtime.GOMAXPROCS(0) },
		L0CompactionThreshold:    2,
	}

	log.Printf("Initializing persistent hash mapper with %dMB memory limit", memLimit/(1024*1024))

	db, err := pebble.Open(dbPath, options)
	if err != nil {
		return nil, fmt.Errorf("failed to open hash database: %w", err)
	}

	return &PersistentPackageMapper{
		db:           db,
		cache:        make(map[string]string, 1000),
		maxCacheSize: 1000,
		batchSize:    100,
		batch:        db.NewBatch(),
		lastFlush:    time.Now(),
		memoryLimit:  memLimit,
	}, nil
}

// AddHashMapping adds a hash->package mapping
func (pm *PersistentPackageMapper) AddHashMapping(hash, packageName string) error {
	// First update memory cache
	pm.cacheMutex.Lock()
	if len(pm.cache) >= pm.maxCacheSize {
		// Clear cache when it gets too big
		pm.cache = make(map[string]string, pm.maxCacheSize)
	}
	pm.cache[hash] = packageName
	pm.cacheMutex.Unlock()

	// Add to disk batch
	pm.batchMutex.Lock()
	defer pm.batchMutex.Unlock()

	if err := pm.batch.Set([]byte(hash), []byte(packageName), nil); err != nil {
		return err
	}

	// Commit batch periodically
	// FIX: Match types for the comparison
	if uint32(pm.batch.Count()) >= uint32(pm.batchSize) || time.Since(pm.lastFlush) > 5*time.Second {
		if err := pm.db.Apply(pm.batch, nil); err != nil {
			return err
		}
		// Create a new batch instead of reusing
		pm.batch.Close()
		pm.batch = pm.db.NewBatch()
		pm.lastFlush = time.Now()
	}

	return nil
}

// GetPackageNameForHash returns the package name for a hash
func (pm *PersistentPackageMapper) GetPackageNameForHash(path string) string {
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
	pm.cacheMutex.RLock()
	pkg, found := pm.cache[hash]
	pm.cacheMutex.RUnlock()

	if found {
		return pkg
	}

	// Not in cache, check database
	value, closer, err := pm.db.Get([]byte(hash))
	if err != nil {
		return ""
	}
	defer closer.Close()

	packageName := string(value)

	// Add to memory cache
	pm.cacheMutex.Lock()
	if len(pm.cache) >= pm.maxCacheSize {
		pm.cache = make(map[string]string, pm.maxCacheSize)
	}
	pm.cache[hash] = packageName
	pm.cacheMutex.Unlock()

	return packageName
}

// Close properly closes the database
func (pm *PersistentPackageMapper) Close() error {
	pm.batchMutex.Lock()
	if pm.batch.Count() > 0 {
		if err := pm.db.Apply(pm.batch, nil); err != nil {
			pm.batchMutex.Unlock()
			return err
		}
		pm.batch.Close()
	}
	pm.batchMutex.Unlock()

	return pm.db.Close()
}

// ClearCache clears the in-memory cache to reduce memory pressure
func (pm *PersistentPackageMapper) ClearCache() {
	pm.cacheMutex.Lock()
	pm.cache = make(map[string]string, pm.maxCacheSize/2) // Allocate at half capacity to save memory
	pm.cacheMutex.Unlock()
	log.Printf("Hash mapper cache cleared due to memory pressure")
}
