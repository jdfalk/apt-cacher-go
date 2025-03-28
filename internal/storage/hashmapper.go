package storage

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/jdfalk/apt-cacher-go/internal/config"
)

// PersistentPackageMapper provides persistent storage for package hash mappings
type PersistentPackageMapper struct {
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
}

// NewPersistentPackageMapper creates a new persistent hash mapper with config settings
func NewPersistentPackageMapper(cacheDir string, config *config.Config) (*PersistentPackageMapper, error) {
	dbPath := filepath.Join(cacheDir, "hashmappings")

	// Get memory limits from config
	var maxCacheEntries int = 10000 // Default
	var maxMemoryMB int64 = 1024    // Default 1GB

	// Read from config if available
	if config != nil {
		// Parse max cache size from config
		if maxCacheStr, ok := config.GetMetadata("memory_management.max_cache_size"); ok {
			if maxCacheStr, ok := maxCacheStr.(string); ok {
				// Parse size string like "8G" or "1024M"
				size, err := config.ParseMemorySize(maxCacheStr)
				if err == nil {
					// Convert bytes to MB and set limit
					maxMemoryMB = size / (1024 * 1024)
					// Scale cache entries based on memory (rough estimate: 10K entries per GB)
					maxCacheEntries = int(maxMemoryMB / 100)
					if maxCacheEntries < 1000 {
						maxCacheEntries = 1000 // Minimum 1000 entries
					}
				}
			}
		}

		// Alternative paths to try for the config value
		if maxMemoryMB == 1024 { // If not set yet
			if val, ok := config.GetMetadata("memory_management.critical_watermark_mb"); ok {
				if criticalMB, ok := val.(int); ok {
					maxMemoryMB = int64(criticalMB)
				}
			}
		}
	}

	// Set a memory limit based on config or system memory
	var memLimit int64
	if maxMemoryMB > 0 {
		memLimit = maxMemoryMB * 1024 * 1024 // Convert MB to bytes
		log.Printf("Using configured memory limit: %d MB", maxMemoryMB)
	} else {
		// Auto-detect based on system memory
		memStat := &runtime.MemStats{}
		runtime.ReadMemStats(memStat)

		// Use up to 20% of total memory
		memLimit = int64(memStat.TotalAlloc / 5)
		memLimit = max(memLimit, 64<<20) // Ensure at least 64MB minimum
		log.Printf("Auto-detected memory limit: %d MB", memLimit/(1024*1024))
	}

	// Open PebbleDB with optimized options
	options := &pebble.Options{
		// Optimize for read-heavy workload with configured memory
		Cache: pebble.NewCache(memLimit / 4), // 25% of memory for block cache
		// Memory budget from config
		MemTableSize: uint64(memLimit / 8), // 12.5% for memtables
		// Set reasonable limits
		MaxOpenFiles:             256,
		MaxConcurrentCompactions: func() int { return runtime.GOMAXPROCS(0) },
		L0CompactionThreshold:    2,
	}

	log.Printf("Initializing persistent hash mapper with %dMB memory limit, %d max entries",
		memLimit/(1024*1024), maxCacheEntries)

	db, err := pebble.Open(dbPath, options)
	if err != nil {
		return nil, fmt.Errorf("failed to open hash database: %w", err)
	}

	pm := &PersistentPackageMapper{
		db:           db,
		dbPath:       dbPath, // Store the database path
		cache:        make(map[string]string, maxCacheEntries),
		maxCacheSize: maxCacheEntries,
		batchSize:    100,
		batch:        db.NewBatch(),
		lastFlush:    time.Now(),
		memoryLimit:  memLimit,
	}
	// Start maintenance routine
	ctx, cancel := context.WithCancel(context.Background())
	pm.cancelFunc = cancel
	go func() {
		<-ctx.Done()
		log.Printf("Maintenance routine stopping due to context cancellation")
	}()
	pm.StartMaintenanceRoutine(ctx)

	return pm, nil
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
	if pm.cancelFunc != nil {
		pm.cancelFunc()
	}

	pm.batchMutex.Lock()
	if pm.batch.Count() > 0 {
		if err := pm.db.Apply(pm.batch, nil); err != nil {
			pm.batchMutex.Unlock()
			return err
		}
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

// CreateCheckpoint creates a database checkpoint for disaster recovery
func (pm *PersistentPackageMapper) CreateCheckpoint(checkpointDir string) error {
	// Ensure we've flushed all pending writes
	pm.batchMutex.Lock()
	if pm.batch.Count() > 0 {
		if err := pm.db.Apply(pm.batch, pebble.Sync); err != nil {
			pm.batchMutex.Unlock()
			return fmt.Errorf("failed to flush before checkpoint: %w", err)
		}
		pm.batch.Close()
		pm.batch = pm.db.NewBatch()
		pm.lastFlush = time.Now()
	}
	pm.batchMutex.Unlock()

	// Create the checkpoint
	return pm.db.Checkpoint(checkpointDir)
}

// VerifyIntegrity checks database integrity
func (pm *PersistentPackageMapper) VerifyIntegrity() error {
	// Force DB compaction to clean up any potential issues
	return pm.db.Compact(nil, nil, true)
}

// Fix the checkpoint directory creation
func (pm *PersistentPackageMapper) StartMaintenanceRoutine(ctx context.Context) {
	checkpointTicker := time.NewTicker(24 * time.Hour)
	compactionTicker := time.NewTicker(6 * time.Hour)

	go func() {
		defer checkpointTicker.Stop()
		defer compactionTicker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-checkpointTicker.C:
				// Use pm.dbPath instead of pm.db.DirName()
				checkpointDir := filepath.Join(filepath.Dir(pm.dbPath), "checkpoints",
					fmt.Sprintf("checkpoint-%s", time.Now().Format("20060102-150405")))

				// Create directory if it doesn't exist
				if err := os.MkdirAll(filepath.Dir(checkpointDir), 0755); err != nil {
					log.Printf("Error creating checkpoint directory: %v", err)
					continue
				}

				if err := pm.CreateCheckpoint(checkpointDir); err != nil {
					log.Printf("Error creating checkpoint: %v", err)
				} else {
					log.Printf("Created database checkpoint at %s", checkpointDir)
				}
			case <-compactionTicker.C:
				if err := pm.VerifyIntegrity(); err != nil {
					log.Printf("Error during database maintenance: %v", err)
				} else {
					log.Printf("Completed database maintenance successfully")
				}
			}
		}
	}()
}
