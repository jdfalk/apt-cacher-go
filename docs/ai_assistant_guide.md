<!-- file: docs/ai_assistant_guide.md -->
<!-- markdownlint-disable MD029 -->

# apt-cacher-go AI Assistant Guide

This guide is designed to help AI assistants understand the project structure, core components, and implementation details of apt-cacher-go.

## Project Purpose

apt-cacher-go serves as a high-performance caching proxy for Debian/Ubuntu package repositories. Its primary purpose is to reduce bandwidth usage and speed up package installations across multiple systems by locally caching downloaded packages.

## Code Organization

The codebase follows a modular structure:

- `/cmd` - Entry points and command-line interface
  - `/serve` - Main server command
  - `/admin` - Administration tool
  - `/benchmark` - Performance testing tools
  - `/apt-cacher-go` - Main binary entry point
- `/internal` - Core implementation components:
  - `/server` - HTTP server and request handling
  - `/cache` - Local storage management
  - `/backend` - Upstream repository communication
  - `/mapper` - Repository path mapping
  - `/metrics` - Statistics and monitoring
  - `/config` - Configuration management
  - `/parser` - Package and repository data parsing
  - `/queue` - Concurrent operation management
  - `/security` - Access control and authentication
  - `/storage` - File storage operations
  - `/keymanager` - Repository key verification
- `/integration` - Integration tests
- `/docs` - Documentation files
- `/scripts` - Utility scripts for deployment and maintenance

## Important Data Structures

### Server Components

1. **Server** (`internal/server/server.go`)

   ```go
   type Server struct {
       cfg           *config.Config
       httpServer    *http.Server
       httpsServer   *http.Server
       cache         *cache.Cache
       backend       *backend.Manager
       metrics       *metrics.Collector
       prometheus    *metrics.PrometheusCollector
       acl           *security.ACL
       mapper        *mapper.AdvancedMapper
       startTime     time.Time
       version       string
       memoryMonitor *MemoryMonitor
   }
   ```

2. **MemoryMonitor** (`internal/server/memory.go`)

   ```go
   type MemoryMonitor struct {
       highWatermarkMB      int
       criticalWatermarkMB  int
       memoryPressureAction func(pressure int)
       checkInterval        time.Duration
       memoryPressure       int64
       stopChan             chan struct{}
       gcCount              uint32
   }
   ```

### Cache Components

3. **Cache** (`internal/cache/cache.go`)

   ```go
   type Cache struct {
       rootDir     string
       maxSize     int64
       currentSize int64
       items       map[string]*cacheEntry
       mutex       sync.RWMutex
       lruCache    *LRUCache
       hitCount    int64
       missCount   int64
       statsMutex  sync.RWMutex
   }
   ```

4. **CacheEntry** (`internal/cache/cache.go`)

   ```go
   type cacheEntry struct {
       Path         string    `json:"path"`
       Size         int64     `json:"size"`
       LastAccessed time.Time `json:"last_accessed"`
       LastModified time.Time `json:"last_modified"`
       HitCount     int       `json:"hit_count"`
   }
   ```

5. **CacheSearchResult** (`internal/cache/cache.go`)

   ```go
   type CacheSearchResult struct {
       Path        string
       PackageName string
       Version     string
       Size        int64
       LastAccess  time.Time
       IsCached    bool
   }
   ```

6. **CacheStats** (`internal/cache/cache.go`)

   ```go
   type CacheStats struct {
       CurrentSize int64
       MaxSize     int64
       Items       int
       HitRate     float64
       MissRate    float64
       Hits        int64
       Misses      int64
   }
   ```

7. **LRUCache** (`internal/cache/lru.go`)

   ```go
   type LRUCache struct {
       capacity int
       items    map[string]*LRUItem
       head     *LRUItem
       tail     *LRUItem
       mutex    sync.Mutex
   }
   ```

### Backend Components

8. **Manager** (`internal/backend/backend.go`)

   ```go
   type Manager struct {
       cfg              *config.Config
       cache            *cache.Cache
       mapper           *mapper.AdvancedMapper
       packageMapper    *mapper.PackageMapper
       httpClient       *http.Client
       downloadQueue    *queue.DownloadQueue
       prefetcher       *Prefetcher
       backends         map[string]*Backend
       backendsMutex    sync.RWMutex
       repoKeyManager   *keymanager.Manager
   }
   ```

9. **Backend** (`internal/backend/backend.go`)

   ```go
   type Backend struct {
       Name     string
       BaseURL  string
       Priority int
       client   *http.Client
   }
   ```

10. **Prefetcher** (`internal/backend/prefetcher.go`)

    ```go
    type Prefetcher struct {
        manager         *Manager
        maxConcurrency  int
        activeFetches   int32
        architectures   map[string]bool
        prefetchQueue   chan prefetchOperation
        stopChan        chan struct{}
        wg              sync.WaitGroup
        memoryPressure  int32
    }
    ```

11. **prefetchOperation** (`internal/backend/prefetcher.go`)

    ```go
    type prefetchOperation struct {
        url       string
        repo      string
        priority  int
        recursive bool
    }
    ```

12. **TestCache** (used in testing, `internal/backend/backend_test.go`)

    ```go
    type TestCache struct {
        *cache.Cache
        PackageMapper *mapper.PackageMapper
        packageIdx    *packageIndex
        GetPackageIndex func() *packageIndex
    }
    ```

13. **packageIndex** (testing implementation, `internal/backend/backend_test.go`)

    ```go
    type packageIndex struct {
        packages map[string]parser.PackageInfo
        mutex    sync.Mutex
    }
    ```

### Mapper Components

14. **AdvancedMapper** (`internal/mapper/advanced_mapper.go`)

    ```go
    type AdvancedMapper struct {
        rules    []MappingRule
        mutex    sync.RWMutex
    }
    ```

15. **MappingRule** (`internal/mapper/advanced_mapper.go`)

    ```go
    type MappingRule struct {
        Type        RuleType
        Pattern     string
        Repository  string
        Priority    int
        RegexObj    *regexp.Regexp
        Rewrite     bool
        RewriteRule string
    }
    ```

16. **PathMapper** (`internal/mapper/mapper.go`)

    ```go
    type PathMapper struct {
        rules []MappingRule
        mutex sync.RWMutex
    }
    ```

17. **MappingResult** (`internal/mapper/mapper.go`)

    ```go
    type MappingResult struct {
        Repository  string
        RemotePath  string
        CachePath   string
        IsIndex     bool
        Rule        *MappingRule
    }
    ```

18. **PackageMapper** (`internal/mapper/package_mapper.go`)

    ```go
    type PackageMapper struct {
        hashPackageMap map[string]string
        mutex         sync.RWMutex
    }
    ```

### Parser Components

19. **PackageInfo** (`internal/parser/packages.go`)

    ```go
    type PackageInfo struct {
        Package      string
        Version      string
        Architecture string
        Maintainer   string
        InstalledSize string
        Filename    string
        Size        string
        MD5sum      string
        SHA1        string
        SHA256      string
        Description string
    }
    ```

### Config Components

20. **Config** (`internal/config/config.go`)

    ```go
    type Config struct {
        ListenAddress string
        CacheDir      string
        MaxCacheSize  string
        LogFile       string
        LogLevel      string
        Backends      []Backend
        AdminAuth     bool
        AdminUser     string
        AdminPassword string
        AdminPort     int
        CacheTTL      string
        HTTPS         bool
        TLSCert       string
        TLSKey        string
        MaxConcurrentDownloads int
        MemoryHighWatermark    int
        MemoryCriticalWatermark int
        MemoryCheckInterval    string
        EnabledArchitectures   []string
        AllowedIPs             []string
    }
    ```

21. **Backend** (`internal/config/config.go`)

    ```go
    type Backend struct {
        Name     string
        URL      string
        Priority int
        Enabled  bool
    }
    ```

## Key Workflows

### Package Request Flow

1. Client sends request to apt-cacher-go (e.g., <http://apt-cacher:3142/ubuntu/pool/main/n/nginx/nginx_1.18.0-0ubuntu1_amd64.deb>)
2. Server receives request in `handlePackage` function (server/handlers.go)
3. Path mapper determines repository and package path using `mapper.Map()`
4. Cache is checked for the package with `cache.Get()`
5. If found and valid, package is served from cache
6. If not found or expired:
   - Backend manager downloads from appropriate repository using `backend.Fetch()`
   - Package is stored in cache with `cache.Put()`
   - Package is streamed to client
7. Metrics are updated with `metrics.RecordCache()`

### Repository Index Processing

1. Client requests an index file (e.g., Release, Packages)
2. Server identifies it as an index file with `IsIndexFile()`
3. If cached copy exists and is valid, it's served
4. Otherwise, backend fetches from upstream
5. For certain index types:
   - `ProcessReleaseFile()` extracts information about other index files
   - `ProcessPackagesFile()` parses package info and updates the package index
6. Package index is used for search functionality

### Cache Management Flow

1. Cache entries are stored with metadata (size, access time, expiration)
2. When cache size would exceed limits, LRU eviction is triggered:
   - `evictItems()` removes least recently used items to make space
   - Uses LRUCache to track access order efficiently
3. Cache state is periodically saved to disk with `saveState()`
4. On startup, saved state is loaded from disk with `loadState()`
5. Safe shutdown with timeout protection in `Close()` method

### Memory Monitoring

1. MemoryMonitor tracks system and application memory usage
2. Regular checks are performed at configured intervals
3. When memory pressure exceeds thresholds, actions are triggered:
   - GC is forced to reclaim memory
   - Prefetch operations may be delayed or cancelled
4. Memory stats are exposed through metrics and admin interface

## Implementation Details

### Thread Safety

The codebase uses several mechanisms to ensure thread safety:

1. Mutex locks (sync.Mutex, sync.RWMutex) for access to shared data structures
2. Fine-grained locking with separate mutexes for different concerns (e.g., cache.mutex and cache.statsMutex)
3. Atomic operations (atomic package) for counters and flags
4. Channel-based communication between goroutines
5. sync.WaitGroup for coordinating goroutine completion
6. Timeouts to prevent deadlocks during shutdown (e.g., in Cache.Close())

### Error Handling

Error handling follows these patterns:

1. Errors are propagated up the call stack
2. Context is added using fmt.Errorf with %w for wrapping
3. HTTP handlers translate errors to appropriate status codes
4. Critical errors are logged with sufficient context for debugging
5. Graceful degradation when possible (e.g., using cached data when downloads fail)

### Testing Approach

The project follows these testing practices:

1. Unit tests for individual components
2. Integration tests for end-to-end functionality
3. Test doubles (mocks, stubs) for isolating components
4. The `TestCache` in backend tests shows how to create test doubles
5. Performance benchmarks for critical operations
6. Docker-based integration testing with real HTTP clients

### Common Development Tasks

1. **Adding a new API endpoint:**
   - Add a new handler function in server/handlers.go
   - Register the handler in server/server.go
   - Add appropriate tests

2. **Adding a new configuration option:**
   - Add the field to the Config struct in config/config.go
   - Update the loadConfig and validateConfig functions
   - Add appropriate defaults and validation
   - Update the component that uses the configuration

3. **Implementing a new caching strategy:**
   - Modify the cache/cache.go file
   - Update the evictItems or other cache management methods
   - Add metrics to track effectiveness

4. **Fixing deadlocks and race conditions:**
   - Use proper lock ordering to avoid deadlocks
   - Minimize the scope of locks to reduce contention
   - Use atomic operations for simple flags and counters
   - Run tests with the race detector
   - Add timeouts for operations that might block indefinitely
