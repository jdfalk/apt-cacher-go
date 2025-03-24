<!-- file: docs/ai_assistant_guide.md -->

# apt-cacher-go AI Assistant Guide

This guide is designed to help AI assistants understand the project structure, core components, and implementation details of apt-cacher-go.

## Project Purpose

apt-cacher-go serves as a high-performance caching proxy for Debian/Ubuntu package repositories. Its primary purpose is to reduce bandwidth usage and speed up package installations across multiple systems by locally caching downloaded packages.

## Code Organization

The codebase follows a modular structure:

- `/cmd` - Entry points and command-line interface
  - `/serve` - Main server command
- `/internal` - Core implementation components:
  - `/server` - HTTP server and request handling
  - `/cache` - Local storage management
  - `/backend` - Upstream repository communication
  - `/mapper` - Repository path mapping
  - `/metrics` - Statistics and monitoring
  - `/config` - Configuration management
- `/integration` - Integration tests
- `/docs` - Documentation files

## Important Data Structures

1. **Server** (`internal/server/server.go`)
   ```go
   type Server struct {
       cfg         *config.Config
       httpServer  *http.Server
       httpsServer *http.Server
       cache       *cache.Cache
       backend     *backend.Manager
       metrics     *metrics.Collector
       prometheus  *metrics.PrometheusCollector
       acl         *security.ACL
       mapper      *mapper.AdvancedMapper
       startTime   time.Time
       version     string
   }
   ```

2. **Cache** (`internal/cache/cache.go`)
   ```go
   type Cache struct {
       rootDir     string
       maxSize     int64
       currentSize int64
       items       map[string]*CacheEntry
       mutex       sync.RWMutex
   }
   ```

3. **MappingRule** (`internal/mapper/advanced_mapper.go`)
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

4. **BackendManager** (`internal/backend/manager.go`)
   ```go
   type Manager struct {
       config      *config.Config
       cache       *cache.Cache
       mapper      *mapper.AdvancedMapper
       httpClient  *http.Client
       downloadQueue *queue.DownloadQueue
       prefetcher   *prefetcher.Prefetcher
   }
   ```

## Key Workflows

### Package Request Flow

1. Client sends request to apt-cacher-go (e.g., http://apt-cacher:3142/ubuntu/pool/main/n/nginx/nginx_1.18.0-0ubuntu1_amd64.deb)
2. Server receives request in `handlePackage` function
3. Path mapper determines repository and package path
4. Cache is checked for the package
5. If found and valid, package is served from cache
6. If not found or expired:
   - Backend manager downloads from appropriate repository
   - Package is stored in cache
   - Package is streamed to client
7. Metrics are updated

### Cache Management Flow

1. Cache entries are stored with metadata (size, access time, expiration)
2. Periodic cache cleanup removes expired entries
3. When cache size exceeds limits, LRU entries are removed
4. Admin operations can trigger manual cache operations

## Common Development Tasks

### Adding a New Feature

1. Identify the component to modify
2. Add tests first (TDD approach)
3. Implement the feature with proper error handling
4. Update documentation
5. Add metrics if performance-sensitive

### Debugging Issues

- Check server logs for error messages
- Review metrics for anomalies in request patterns
- Verify cache state through admin interface
- Use Go's race detector for concurrency issues

## Implementation Notes

### Thread Safety

The project uses several approaches for thread safety:

1. Mutex locks for shared data (e.g., cache entries)
2. Atomic operations for simple counters
3. Context-based cancellation for coordinating goroutines
4. Channel communication for safe data sharing between goroutines

Example from metrics.go:
```go
// Thread-safe increment of counter
func (c *Collector) IncRequests() {
    atomic.AddInt64(&c.totalRequests, 1)
}

// Thread-safe access to map with mutex
func (c *Collector) RecordResponseTime(pathType string, duration time.Duration) {
    c.mutex.Lock()
    defer c.mutex.Unlock()

    c.responseTimeData[pathType] = append(c.responseTimeData[pathType], duration)
}
```

### Error Handling

The project follows these error handling patterns:

1. Functions return errors rather than panicking
2. Errors include context using `fmt.Errorf` with `%w`
3. HTTP handlers convert errors to appropriate status codes
4. Errors are logged with sufficient context

### Configuration Loading

The configuration system prioritizes sources in this order:

1. Command-line flags
2. Environment variables
3. Configuration file
4. Default values

This allows for flexible deployment in different environments.
