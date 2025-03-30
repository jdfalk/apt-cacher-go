package server

import (
	"context"
	"io"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/cache"
	"github.com/jdfalk/apt-cacher-go/internal/mapper"
	"github.com/jdfalk/apt-cacher-go/internal/metrics"
	"github.com/jdfalk/apt-cacher-go/internal/parser"
)

// ServerOptions contains options for creating a new server
type ServerOptions struct {
	Version          string
	Logger           io.Writer
	BackendManager   BackendManager
	Cache            Cache
	PathMapper       PathMapper
	PackageMapper    PackageMapper
	MetricsCollector MetricsCollector
	MemoryMonitor    MemoryMonitorInterface
}

// Cache defines the interface for caching operations
type Cache interface {
	// Get retrieves an item from the cache
	Get(path string) ([]byte, error)

	// Put stores an item in the cache
	Put(path string, data []byte) error

	// PutWithExpiration stores an item with an expiration time
	PutWithExpiration(path string, data []byte, ttl time.Duration) error

	// IsFresh checks if a cached item is fresh
	IsFresh(path string) bool

	// Exists checks if an item exists in the cache
	Exists(path string) bool

	// GetStats returns cache statistics
	GetStats() cache.CacheStats

	// GetLastModified returns when a file was last modified
	GetLastModified(path string) time.Time

	// SearchByPackageName searches for packages by name
	SearchByPackageName(name string) ([]cache.CacheSearchResult, error)

	// UpdatePackageIndex updates the package index
	UpdatePackageIndex(packages []parser.PackageInfo) error

	// Search searches for files in the cache by path pattern
	Search(query string) ([]string, error)
}

// BackendManager defines the interface for managing backend operations
type BackendManager interface {
	// Fetch fetches content from a backend
	Fetch(path string) ([]byte, error)

	// ProcessPackagesFile processes a package index file
	ProcessPackagesFile(repo, path string, data []byte)

	// ProcessReleaseFile processes a release file
	ProcessReleaseFile(repo, path string, data []byte)

	// ForceCleanupPrefetcher forces cleanup of prefetcher
	ForceCleanupPrefetcher() int

	// PrefetchOnStartup performs prefetch operations on startup
	PrefetchOnStartup(ctx context.Context)

	// KeyManager returns the key manager instance
	KeyManager() interface{}
}

// PathMapper defines the interface for path mapping
type PathMapper interface {
	// MapPath maps a request path to a repository and cache path
	MapPath(path string) (mapper.MappingResult, error)
}

// PackageMapper defines the interface for package mapping
type PackageMapper interface {
	// AddHashMapping adds a hash-to-package mapping
	AddHashMapping(hash, packageName string)

	// GetPackageNameForHash gets a package name for a hash
	GetPackageNameForHash(hash string) string

	// ClearCache clears the package mapper cache
	ClearCache()
}

// MetricsCollector defines the interface for collecting metrics
type MetricsCollector interface {
	// RecordRequest records a request
	RecordRequest(path string, duration time.Duration, clientIP, packageName string)

	// RecordCacheHit records a cache hit
	RecordCacheHit(path string, size int64)

	// RecordCacheMiss records a cache miss
	RecordCacheMiss(path string, size int64)

	// RecordError records an error
	RecordError(path string)

	// RecordBytesServed records bytes served
	RecordBytesServed(bytes int64)

	// GetStatistics returns metrics statistics
	GetStatistics() metrics.Statistics

	// GetTopPackages returns the most accessed packages
	GetTopPackages(limit int) []metrics.PackageStats

	// GetTopClients returns the clients with most requests
	GetTopClients(limit int) []metrics.ClientStats

	// SetLastClientIP sets the last client IP
	SetLastClientIP(ip string)

	// SetLastFileSize sets the last file size
	SetLastFileSize(size int64)
}

// MemoryMonitorInterface defines the interface for memory monitoring
type MemoryMonitorInterface interface {
	// Start starts the memory monitor
	Start()

	// Stop stops the memory monitor
	Stop()

	// GetMemoryUsage returns current memory usage stats
	GetMemoryUsage() map[string]any
}

// KeyManager defines the interface for GPG key management
type KeyManager interface {
	// HasKey checks if a key exists
	HasKey(keyID string) bool

	// GetKeyPath gets the path to a key file
	GetKeyPath(keyID string) string

	// FetchKey fetches a key
	FetchKey(keyID string) error

	// DetectKeyError detects key errors in content
	DetectKeyError(data []byte) (string, bool)
}
