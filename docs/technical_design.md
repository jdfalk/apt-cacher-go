<!-- file: docs/technical_design.md -->

# apt-cacher-go Technical Design Document

## Project Overview

apt-cacher-go is a high-performance caching proxy for Debian/Ubuntu package repositories written in Go. It serves as an intermediary between APT clients and official repositories, storing downloaded packages locally to reduce bandwidth usage and improve installation speeds across multiple systems.

### Key Features

- High-performance concurrent downloads with Go's goroutines
- Smart caching with different expiration policies for index vs. package files
- Repository path mapping with flexible rule-based configuration
- Web-based administration interface with real-time statistics
- Prometheus metrics integration for monitoring
- IP-based access control
- Optional HTTPS support
- Memory pressure monitoring and adaptation

## System Architecture

### Component Overview

1. **HTTP Server** (`internal/server/`)
   - Handles client connections and request routing
   - Implements access control and security measures
   - Serves admin interface and API endpoints
   - Manages graceful shutdown with proper resource cleanup

2. **Cache Manager** (`internal/cache/`)
   - Manages local storage of package and index files
   - Implements expiration policies and LRU eviction
   - Handles concurrent access with proper locking
   - Provides state persistence across restarts

3. **Backend Manager** (`internal/backend/`)
   - Communicates with upstream repositories
   - Manages connection pooling and retry logic
   - Handles download streaming and rate limiting
   - Implements intelligent prefetching of packages

4. **Repository Mapper** (`internal/mapper/`)
   - Maps request paths to appropriate backends
   - Supports regex, prefix, and exact match rules
   - Implements path rewriting functionality
   - Maintains priority-based rule evaluation

5. **Admin Interface** (`internal/server/admin.go`)
   - Provides web UI for statistics and management
   - Exposes cache control functions (clear, prune)
   - Displays real-time metrics and system status
   - Provides package search functionality

6. **Metrics Collector** (`internal/metrics/`)
   - Tracks request statistics and performance metrics
   - Provides Prometheus-compatible endpoint
   - Collects cache hit/miss statistics
   - Monitors system resource utilization

7. **Memory Monitor** (`internal/server/memory.go`)
   - Tracks system memory pressure
   - Initiates garbage collection when needed
   - Throttles operations during high memory usage
   - Prevents out-of-memory crashes

### Data Flow

1. Client sends package request to apt-cacher-go
2. Server validates request and applies access control
3. Path mapper determines which repository should handle the request
4. Cache is checked for the requested package
5. If found and valid, package is served from cache
6. If not found or expired, backend manager downloads from upstream
7. Downloaded package is stored in cache and served to client
8. Metrics are updated to reflect the transaction

## Key Design Decisions

### Concurrency Model

- Uses Go's goroutines for handling multiple requests
- Implements connection pooling for backend requests
- Uses mutex locks for thread-safe cache operations
- Employs context-based cancellation for timeouts
- Uses sync.WaitGroup for coordinating goroutine completion
- Implements timeouts to prevent deadlocks during shutdown

### Caching Strategy

- Differentiates between package files and index files
- Longer TTL for stable package versions
- Shorter TTL for repository index files
- Implements LRU eviction when cache size limits are reached
- Maintains separate locks for cache metadata and statistics
- Persists cache state to disk for recovery after restart

### Repository Mapping

- Advanced rule-based system for path mapping
- Support for regular expressions with capture groups
- Priority-based rule evaluation
- Default fallback rules for standard repositories
- Efficient route matching with precompiled patterns
- Support for path rewriting for complex repository structures

### Security Considerations

- IP-based access control
- Optional TLS support for client connections
- Proper file permission management
- Protection against path traversal attacks
- Rate limiting to prevent abuse
- Controlled access to admin interface

### Memory Management

- Memory usage monitoring
- High and critical watermark thresholds
- Adaptive behavior based on memory pressure
- Proactive garbage collection
- Limiting concurrent operations under pressure
- Graceful handling of resource constraints

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
prefetch_enabled: true
prefetch_limit: 5
memory_high_watermark: 2048
memory_critical_watermark: 3072
memory_check_interval: 30s

# TLS configuration
tls_enabled: false
tls_port: 3143
tls_cert_file: /etc/apt-cacher-go/cert.pem
tls_key_file: /etc/apt-cacher-go/key.pem

# Repository mapping rules
mapping_rules:
  - type: regex
    pattern: "(security|archive).ubuntu.com/ubuntu"
    repository: ubuntu
    priority: 100

  - type: prefix
    pattern: "debian.org"
    repository: debian
    priority: 50

  - type: rewrite
    pattern: "ppa.launchpadcontent.net/([^/]+)/([^/]+)"
    repository: ppa/%1/%2
    rewrite_rule: "ppa/%1/%2"
    priority: 75

# Access control
allowed_ips:
  - 127.0.0.1
  - 192.168.0.0/24
  - 10.0.0.0/8

# Repository backends
backends:
  - name: ubuntu
    url: http://archive.ubuntu.com/ubuntu
    priority: 100
    enabled: true

  - name: ubuntu-security
    url: http://security.ubuntu.com/ubuntu
    priority: 95
    enabled: true

  - name: debian
    url: http://deb.debian.org/debian
    priority: 90
    enabled: true

  - name: debian-security
    url: http://security.debian.org/debian-security
    priority: 85
    enabled: true
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
- `APT_CACHER_PREFETCH_LIMIT`: Maximum prefetch connections
- `APT_CACHER_MEMORY_HIGH_WATERMARK`: Memory usage high threshold (MB)
- `APT_CACHER_MEMORY_CRITICAL_WATERMARK`: Memory usage critical threshold (MB)
- `APT_CACHER_TLS_ENABLED`: Enable HTTPS support
- `APT_CACHER_TLS_PORT`: HTTPS port number
- `APT_CACHER_TLS_CERT_FILE`: TLS certificate file path
- `APT_CACHER_TLS_KEY_FILE`: TLS key file path

## Implementation Considerations

### Thread Safety

The codebase uses several mechanisms to ensure thread safety:

1. Mutex locks (sync.Mutex, sync.RWMutex) for access to shared data structures
2. Fine-grained locking with separate mutexes for different concerns
3. Atomic operations for counters and flags
4. Channel-based communication between goroutines
5. sync.WaitGroup for coordinating goroutine completion
6. Timeouts to prevent deadlocks during shutdown

### Error Handling

Error handling follows these patterns:

1. Errors are propagated up the call stack
2. Context is added using fmt.Errorf with %w for wrapping
3. HTTP handlers translate errors to appropriate status codes
4. Critical errors are logged with sufficient context for debugging
5. Graceful degradation when possible (using cached data when downloads fail)

### Performance Optimization

Several techniques are used to optimize performance:

1. Connection pooling for backend requests
2. Prefetching commonly used packages
3. Memory-mapped files for large packages
4. LRU caching with efficient eviction
5. Goroutines for concurrent operations
6. Adaptive behavior based on system load
7. Intelligent pacing of background operations
