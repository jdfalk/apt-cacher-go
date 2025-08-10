<!-- file: docs/technical_design.md -->

# apt-cacher-go Technical Design Document

## Project Overview

apt-cacher-go is a high-performance caching proxy for Debian/Ubuntu package
repositories written in Go. It serves as an intermediary between APT clients and
official repositories, storing downloaded packages locally to reduce bandwidth
usage and improve installation speeds across multiple systems.

### Key Features

- High-performance concurrent downloads with Go's goroutines
- Smart caching with different expiration policies for index vs. package files
- Repository path mapping with flexible rule-based configuration
- Web-based administration interface with real-time statistics
- Prometheus metrics integration for monitoring
- IP-based access control
- HTTPS support with tunneling capabilities
- Memory pressure monitoring and adaptation
- Efficient persistent storage with PebbleDB

## System Architecture

### Component Overview

1. **HTTP Server** (`internal/server/`)
   - Handles client connections and request routing
   - Implements access control and security measures
   - Serves admin interface and API endpoints
   - Supports HTTPS and CONNECT tunneling
   - Manages graceful shutdown with proper resource cleanup

2. **Cache Manager** (`internal/cache/`)
   - Manages local storage of package and index files
   - Implements expiration policies and LRU eviction
   - Handles concurrent access with proper locking
   - Provides state persistence across restarts
   - Uses PebbleDB for efficient metadata storage

3. **Backend Manager** (`internal/backend/`)
   - Communicates with upstream repositories
   - Manages connection pooling and retry logic
   - Handles download streaming and rate limiting
   - Implements intelligent prefetching of packages
   - Supports multiple protocol handling (HTTP/HTTPS)

4. **Repository Mapper** (`internal/mapper/`)
   - Maps request paths to appropriate backends
   - Supports regex, prefix, and exact match rules
   - Implements path rewriting functionality
   - Maintains priority-based rule evaluation
   - Handles architecture-specific path patterns

5. **Admin Interface** (`internal/server/admin.go`)
   - Provides web UI for statistics and management
   - Exposes cache control functions (clear, prune)
   - Displays real-time metrics and system status
   - Provides package search functionality
   - Monitors system resource utilization

6. **Metrics Collector** (`internal/metrics/`)
   - Tracks request statistics and performance metrics
   - Provides Prometheus-compatible endpoint
   - Collects cache hit/miss statistics
   - Monitors system resource utilization
   - Tracks prefetch success/failure rates

7. **Memory Monitor** (`internal/server/memory.go`)
   - Tracks system memory pressure
   - Initiates garbage collection when needed
   - Throttles operations during high memory usage
   - Prevents out-of-memory crashes
   - Provides feedback to other components

8. **Key Manager** (`internal/keymanager/`)
   - Manages repository GPG keys
   - Auto-retrieves missing keys when needed
   - Validates package signatures
   - Manages key expiration and renewal
   - Supports multiple keyserver sources

9. **Storage System** (`internal/storage/`)
   - PebbleDB-based persistent storage
   - Efficient key-value operations
   - Atomic batch operations
   - Supports package indexing and search
   - Manages cache metadata

### Data Flow

1. Client sends package request to apt-cacher-go
2. Server validates request and applies access control
3. Path mapper determines which repository should handle the request
4. Cache is checked for the requested package
5. If found and valid, package is served from cache
6. If not found or expired, backend manager downloads from upstream
7. Downloaded package is stored in cache and served to client
8. Metrics are updated to reflect the transaction
9. If the memory monitor detects pressure, it may throttle operations
10. Prefetcher may extract additional URLs to download in the background

## Key Design Decisions

### Concurrency Model

- Uses Go's goroutines for handling multiple requests
- Implements connection pooling for backend requests
- Uses mutex locks for thread-safe cache operations
- Employs atomic operations for counters and flags
- Employs context-based cancellation for timeouts
- Uses sync.WaitGroup for coordinating goroutine completion
- Implements queue-based work distribution with rate limiting
- Implements timeouts to prevent deadlocks during shutdown

### Caching Strategy

- Differentiates between package files and index files
- Longer TTL for stable package versions
- Shorter TTL for repository index files
- Implements LRU eviction when cache size limits are reached
- Maintains separate locks for cache metadata and statistics
- Persists cache state to disk for recovery after restart
- Uses PebbleDB for efficient metadata storage
- Implements batch operations for write efficiency

### Repository Mapping

- Advanced rule-based system for path mapping
- Support for regular expressions with capture groups
- Priority-based rule evaluation
- Default fallback rules for standard repositories
- Efficient route matching with precompiled patterns
- Support for path rewriting for complex repository structures
- Architecture-specific filtering capabilities

### Security Considerations

- IP-based access control
- Optional TLS support for client connections
- HTTPS backend support with proper certificate validation
- Proper file permission management
- Protection against path traversal attacks
- Rate limiting to prevent abuse
- Controlled access to admin interface
- Secure GPG key handling and verification

### Memory Management

- Memory usage monitoring
- High and critical watermark thresholds
- Adaptive behavior based on memory pressure
- Proactive garbage collection
- Limiting concurrent operations under pressure
- Graceful handling of resource constraints
- Memory pressure feedback to prefetcher

### Storage Engine

- PebbleDB for persistent key-value storage
- Log-structured merge tree for efficient writes
- Compression for reduced disk usage
- Memory-mapped I/O for performance
- Batch operations for atomic updates
- Cache invalidation based on TTL
- Hierarchical storage structure

## Configuration System

### Configuration File Format

apt-cacher-go uses a YAML configuration file with the following structure:

```yaml
# Server configuration
listen_address: 0.0.0.0
port: 3142
admin_port: 9283
log_level: info

# Cache configuration
cache_dir: /var/cache/apt-cacher-go
max_cache_size: 10GB
cache_ttl:
  index: 1h
  package: 30d
  default: 7d

# Performance settings
max_concurrent_downloads: 10
prefetch:
  enabled: true
  max_concurrent: 5
  architectures:
    - amd64
    - arm64
  warmup_on_startup: true
  batch_size: 10
memory_high_watermark: 2048
memory_critical_watermark: 3072
memory_check_interval: 30s

# TLS configuration
tls_enabled: false
tls_port: 3143
tls_cert: /etc/apt-cacher-go/cert.pem
tls_key: /etc/apt-cacher-go/key.pem

# Repository mapping rules
mapping_rules:
  - type: regex
    pattern: '(security|archive).ubuntu.com/ubuntu'
    priority: 100

  - type: prefix
    pattern: 'debian.org'
    priority: 50

  - type: rewrite
    pattern: 'ppa.launchpadcontent.net/([^/]+)/([^/]+)'
    repository: 'ppa/%1/%2'
    rewrite_rule: 'ppa/%1/%2'
    priority: 75

# Access control
allowed_ips:
  - 127.0.0.1
  - 10.0.0.0/8

# Repository backends
backends:
  - name: ubuntu
    url: http://archive.ubuntu.com/ubuntu
    priority: 100
  - name: debian
    url: http://deb.debian.org/debian
    priority: 90

# Key management
key_management:
  enabled: true
  auto_retrieve: true
  key_ttl: 365d
  keyservers:
    - hkp://keyserver.ubuntu.com
  key_dir: /var/cache/apt-cacher-go/keys
```

### Environment Variables

All configuration options can be overridden with environment variables:

- `APT_CACHER_CACHE_DIR`: Path to cache directory
- `APT_CACHER_LISTEN_ADDRESS`: IP address to listen on
- `APT_CACHER_PORT`: Port number to listen on
- `APT_CACHER_ADMIN_PORT`: Admin interface port
- `APT_CACHER_MAX_CACHE_SIZE`: Maximum cache size
- `APT_CACHER_LOG_LEVEL`: Logging level (debug, info, warn, error)
- `APT_CACHER_MAX_CONCURRENT_DOWNLOADS`: Maximum parallel downloads
- `APT_CACHER_PREFETCH_ENABLED`: Enable background prefetching
- `APT_CACHER_PREFETCH_MAX_CONCURRENT`: Maximum prefetch connections
- `APT_CACHER_MEMORY_HIGH_WATERMARK`: Memory usage high threshold (MB)
- `APT_CACHER_MEMORY_CRITICAL_WATERMARK`: Memory usage critical threshold (MB)
- `APT_CACHER_TLS_ENABLED`: Enable HTTPS support
- `APT_CACHER_TLS_PORT`: HTTPS port number
- `APT_CACHER_TLS_CERT`: TLS certificate file path
- `APT_CACHER_TLS_KEY`: TLS key file path

## Implementation Details

### PebbleDB Integration

1. Cache metadata is stored in PebbleDB for persistence
2. Package information is indexed for efficient searches
3. Hash-to-package mappings enable fast lookups by SHA256
4. Atomic batch operations ensure data consistency
5. Data is compressed to reduce storage requirements
6. In-memory caching reduces database reads for hot items

### Prefetching System

1. Analyzes package index files to extract downloadable URLs
2. Filters packages by configured architectures
3. Queues package downloads with priority-based processing
4. Monitors and adapts to memory pressure
5. Tracks metrics for success/failure rates
6. Uses rate limiting to avoid overwhelming upstream servers
7. Implements timeout and error handling for resilience
8. Supports background startup warmup of cache

### HTTPS and Protocol Support

1. TLS termination for direct HTTPS client connections
2. CONNECT tunneling support for transparent proxying
3. Repository URL remapping from HTTPS to HTTP
4. GPG key server tunneling support
5. Certificate validation for security
6. Protocol negotiation and upgrade handling

### Thread Safety

The codebase uses several mechanisms to ensure thread safety:

1. Mutex locks (sync.Mutex, sync.RWMutex) for access to shared data structures
2. Fine-grained locking with separate mutexes for different concerns
3. Atomic operations (atomic package) for counters and flags
4. Channel-based communication between goroutines
5. sync.WaitGroup for coordinating goroutine completion
6. sync.Once for operations that must execute exactly once
7. Timeouts to prevent deadlocks during shutdown
8. Context-based cancellation for propagating shutdown signals

### Error Handling

Error handling follows these patterns:

1. Errors are propagated up the call stack
2. Context is added using fmt.Errorf with %w for wrapping
3. HTTP handlers translate errors to appropriate status codes
4. Critical errors are logged with sufficient context for debugging
5. Graceful degradation when possible (using cached data when downloads fail)
6. Retries with exponential backoff for transient failures
7. Health monitoring to detect persistent issues

### Memory Management

1. MemoryMonitor tracks both application and system memory
2. Configurable thresholds trigger different levels of action
3. Forced garbage collection under memory pressure
4. Throttling of prefetch operations when memory is tight
5. Metrics reporting for monitoring and alerting
6. Cache pruning when storage limits are reached
7. Balanced approach between memory usage and performance
