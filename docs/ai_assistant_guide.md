<!-- file: docs/ai_assistant_guide.md -->
<!-- markdownlint-disable MD029 -->

# apt-cacher-go AI Assistant Guide

This guide is designed to help AI assistants understand the project structure,
core components, and implementation details of apt-cacher-go.

## Project Purpose

apt-cacher-go serves as a high-performance caching proxy for Debian/Ubuntu
package repositories. Its primary purpose is to reduce bandwidth usage and speed
up package installations across multiple systems by locally caching downloaded
packages.

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
       adminServer   *http.Server
       cache         Cache
       backend       BackendManager
       metrics       MetricsCollector
       acl           *security.ACL
       mapper        PathMapper
       packageMapper PackageMapper
       memoryMonitor MemoryMonitorInterface
       // Additional fields omitted
   }
   ```

2. **MemoryMonitor** (`internal/server/memory.go`)
   ```go
   type MemoryMonitor struct {
       mutex                sync.RWMutex
       highWatermarkMB      int64
       criticalWatermarkMB  int64
       memoryPressure       int64
       memoryPressureAction func(pressure int)
       // Additional fields omitted
   }
   ```

### Cache Components

3. **Cache** (`internal/cache/cache.go`)

   ```go
   type Cache struct {
       rootDir     string
       maxSize     int64
       db          *storage.DatabaseStore
       packageMutex sync.RWMutex
   }
   ```

4. **LRUCache** (`internal/cache/lru.go`)

   ```go
   type LRUCache struct {
       capacity  int
       items     map[string]*list.Element
       evictList *list.List
       mutex     sync.RWMutex
   }
   ```

5. **DatabaseStore** (`internal/storage/pebbledb.go`)
   ```go
   type DatabaseStore struct {
       db                *pebble.DB
       dbPath            string
       cache             map[string]string
       cacheMutex        sync.RWMutex
       batchMutex        sync.Mutex
       batch             *pebble.Batch
       packageCount      int64
       packageCountMutex sync.RWMutex
       // Additional fields omitted
   }
   ```

### Backend Components

6. **Manager** (`internal/backend/backend.go`)

   ```go
   type Manager struct {
       backends       []*Backend
       cache          CacheProvider
       client         *http.Client
       mapper         PathMapperProvider
       packageMapper  PackageMapperProvider
       downloadQ      *queue.Queue
       prefetcher     *Prefetcher
       keyManager     *keymanager.KeyManager
       // Additional fields omitted
   }
   ```

7. **Prefetcher** (`internal/backend/prefetcher.go`)

   ```go
   type Prefetcher struct {
       manager        PrefetcherManager
       active         sync.Map
       maxActive      int
       architectures  map[string]bool
       memoryPressure int32
       prefetchQueue  chan prefetchRequest
       metrics        *metrics.PrefetchMetrics
       // Additional fields omitted
   }
   ```

8. **Queue** (`internal/queue/queue.go`)
   ```go
   type Queue struct {
       taskCh     chan *Task
       stopCh     chan struct{}
       workers    int
       isStopping int32
       stopOnce   sync.Once
       // Additional fields omitted
   }
   ```

### Mapper Components

9. **PathMapper** (`internal/mapper/mapper.go`)

   ```go
   type PathMapper struct {
       rules []MappingRule
       mutex sync.RWMutex
   }
   ```

10. **PackageMapper** (`internal/mapper/mapper.go`)
    ```go
    type PackageMapper struct {
        hashToPackage map[string]string
        mutex         sync.RWMutex
    }
    ```

### Security Components

11. **ACL** (`internal/security/acl.go`)

    ```go
    type ACL struct {
        allowedNetworks []*net.IPNet
        mutex           sync.RWMutex
    }
    ```

12. **KeyManager** (`internal/keymanager/keymanager.go`)
    ```go
    type KeyManager struct {
        config     *config.KeyManagementConfig
        keyCache   map[string]time.Time
        fetchMutex sync.RWMutex
    }
    ```

## Key Workflows

### Package Request Flow

1. Client sends request to apt-cacher-go (e.g.,
   `http://apt-cacher:3142/ubuntu/pool/main/n/nginx/nginx_1.18.0-0ubuntu1_amd64.deb`)
2. Server receives request in `handlePackageRequest` function
   (server/handlers.go)
3. Path mapper determines repository and package path using `mapper.MapPath()`
4. Cache is checked for the package with `cache.Get()`
5. If found and valid, package is served from cache
6. If not found or expired:
   - Backend manager downloads from appropriate repository using
     `backend.Fetch()`
   - Package is streamed to client while being saved to cache
   - Package metadata is extracted and stored for indexing
7. Metrics are updated with `metrics.RecordRequest()` and related methods

### HTTP/HTTPS Handling

1. The server can handle both HTTP and HTTPS requests
2. For HTTPS requests, the server can:
   - Act as an HTTPS endpoint with TLS termination
   - Remap HTTPS repository requests to HTTP backend connections
   - Support CONNECT tunneling for repository key servers
3. HTTPS remapping is handled in `handleHTTPSRequest`
4. CONNECT tunneling is implemented in `handleConnectRequest`

### Memory Management

1. MemoryMonitor tracks system and application memory usage
2. Regular checks are performed at configured intervals
3. When memory pressure exceeds thresholds, actions are triggered:
   - GC is forced to reclaim memory
   - Prefetch operations may be delayed or cancelled
   - Background tasks may be throttled
4. Memory stats are exposed through metrics and admin interface

### Package Prefetching

1. When index files are downloaded, prefetcher extracts package URLs
2. Package URLs are filtered by architecture if configured
3. Prefetch requests are added to a queue with priorities
4. Worker goroutines process the queue with controlled concurrency
5. If memory pressure is high, prefetching is throttled or paused
6. Metrics track prefetch success/failure rates

## Implementation Details

### PebbleDB Storage

1. PebbleDB provides persistent key-value storage for:
   - Package metadata
   - Cache entry information
   - Hash-to-package mappings
   - Cache statistics
2. Batch operations improve performance for multiple writes
3. Memory caching reduces database reads for hot items
4. Atomic counters ensure thread-safe operations

### Thread Safety

The codebase uses several mechanisms to ensure thread safety:

1. Mutex locks (sync.Mutex, sync.RWMutex) for access to shared data structures
2. Fine-grained locking with separate mutexes for different concerns (e.g.,
   packageMutex, cacheMutex)
3. Atomic operations (atomic package) for counters and flags
4. Channel-based communication between goroutines
5. sync.WaitGroup for coordinating goroutine completion
6. sync.Once for operations that must execute exactly once
7. Timeouts to prevent deadlocks during shutdown

### Error Handling

Error handling follows these patterns:

1. Errors are propagated up the call stack
2. Context is added using fmt.Errorf with %w for wrapping
3. HTTP handlers translate errors to appropriate status codes
4. Critical errors are logged with sufficient context for debugging
5. Graceful degradation when possible (e.g., using cached data when downloads
   fail)

### Testing Approach

1. Extensive use of interfaces for mocking dependencies
2. TestFixture pattern for setting up test environments
3. Concurrent testing with timeouts to catch race conditions
4. Explicit cleanup with t.Cleanup to ensure resources are released
5. Context cancellation testing for graceful shutdown
6. Comprehensive documentation comments for test purpose and approach

## Common Development Tasks

1. **Adding a new API endpoint:**
   - Add a new handler function in server/handlers.go
   - Add route in setupHTTPHandlers() in server/server.go
   - Add tests in server/server_test.go

2. **Modifying caching behavior:**
   - Update relevant methods in cache/cache.go
   - Modify storage/pebbledb.go for persistence changes
   - Update expiration logic in cache.PutWithExpiration

3. **Adding prefetch support for new file types:**
   - Update ProcessIndexFile in backend/prefetcher.go
   - Modify FilterURLsByArchitecture to handle new patterns
   - Add test cases in prefetcher_test.go

4. **Debugging deadlocks:**
   - Look for missing mutex unlocks or deferred unlocks
   - Check for circular dependencies in lock acquisition
   - Verify that channels have sufficient buffer sizes or are properly consumed
   - Add timeouts to context-based operations
