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

## System Architecture

### Component Overview

1. **HTTP Server** (`internal/server/`)
   - Handles client connections and request routing
   - Implements access control and security measures
   - Serves admin interface and API endpoints

2. **Cache Manager** (`internal/cache/`)
   - Manages local storage of package and index files
   - Implements expiration policies
   - Handles concurrent access with proper locking

3. **Backend Manager** (`internal/backend/`)
   - Communicates with upstream repositories
   - Manages connection pooling and retry logic
   - Handles download streaming

4. **Repository Mapper** (`internal/mapper/`)
   - Maps request paths to appropriate backends
   - Supports regex, prefix, and exact match rules
   - Implements path rewriting functionality

5. **Admin Interface** (`internal/server/admin.go`)
   - Provides web UI for statistics and management
   - Exposes cache control functions
   - Displays real-time metrics

6. **Metrics Collector** (`internal/metrics/`)
   - Tracks request statistics and performance metrics
   - Provides Prometheus-compatible endpoint
   - Collects cache hit/miss statistics

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

### Caching Strategy

- Differentiates between package files and index files
- Longer TTL for stable package versions
- Shorter TTL for repository index files
- Implements LRU eviction when cache size limits are reached

### Repository Mapping

- Advanced rule-based system for path mapping
- Support for regular expressions with capture groups
- Priority-based rule evaluation
- Default fallback rules for standard repositories

### Security Considerations

- IP-based access control
- Optional TLS support for client connections
- Proper file permission management
- Protection against path traversal attacks

## Configuration System

### Configuration File Format

apt-cacher-go uses a YAML configuration file with the following structure:

```yaml
# Example configuration file
cache_dir: /var/cache/apt-cacher-go
listen_address: 0.0.0.0
port: 3142
max_cache_size: 10GB
log_level: info

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

# Access control
allowed_ips:
  - 127.0.0.1
  - 192.168.0.0/24
```

### Environment Variables

All configuration options can be overridden with environment variables:

- `APT_CACHER_CACHE_DIR`: Path to cache directory
- `APT_CACHER_LISTEN_ADDRESS`: IP address to listen on
- `APT_CACHER_PORT`: Port number to listen on
- `APT_CACHER_MAX_CACHE_SIZE`: Maximum cache size
- `APT_CACHER_LOG_LEVEL`: Logging level (debug, info, warn, error)
