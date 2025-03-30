<!-- file: docs/testing_design.md -->

# apt-cacher-go Testing Design Document

## Testing Strategy

apt-cacher-go employs a comprehensive testing strategy covering:

1. Unit tests for individual components
2. Integration tests for component interactions
3. End-to-end tests simulating actual client behavior
4. Performance benchmarks for critical paths

### Test Environments

- Local development environment
- CI pipeline with automated tests
- Docker-based integration test environment

## Unit Testing

Unit tests focus on testing individual components in isolation:

- Cache operations (store, retrieve, expire)
- Path mapping rules
- Backend connection handling
- Configuration parsing
- Metrics collection

### Testing Frameworks

- Go's built-in testing package
- Testify for assertions and mocking
- httptest for HTTP handler testing

## Integration Testing

Integration tests verify interactions between components:

- Cache and backend manager coordination
- Server handling of various request types
- End-to-end request flow with mocked backends

### Test Data

- Sample package and index files
- Various repository structures
- Edge cases (large files, malformed requests)

## Performance Testing

Performance tests measure:

- Request throughput under various loads
- Memory usage patterns
- Cache hit ratio impact on performance
- Concurrent download capabilities

### Benchmarks

- Single package download speed
- Multiple simultaneous client simulation
- Large repository index handling
- Cache pruning performance

## Test Coverage

- Target: >80% code coverage
- Critical path coverage: >95%
- Automated coverage reporting in CI

## Test File Organization

```
/
├── internal/
│   ├── cache/
│   │   └── cache_test.go
│   ├── mapper/
│   │   └── advanced_mapper_test.go
│   ├── backend/
│   │   └── manager_test.go
│   └── server/
│       └── server_test.go
├── integration/
│   └── integration_test.go
└── cmd/
    └── serve/
        └── serve_test.go
```

## Docker-Based Testing

The project includes a Docker-based testing environment that:

1. Sets up multiple APT clients
2. Configures an apt-cacher-go instance
3. Simulates package installations and updates
4. Measures performance metrics
5. Validates cache behavior

## Continuous Integration

The CI pipeline includes:

1. Running unit tests on every commit
2. Integration tests on pull requests
3. Performance benchmarks compared to baseline
4. Code coverage reporting
5. Static analysis with Go linters

## Test Helpers and Utilities

### TestCache Implementation

The `TestCache` in `backend_test.go` demonstrates how to implement test doubles:

```go
type TestCache struct {
    *cache.Cache
    PackageMapper *mapper.PackageMapper
    packageIdx    *packageIndex
}
```

This pattern allows tests to:

- Wrap real components
- Override specific behaviors
- Track method calls
- Simulate various conditions

### Mock HTTP Servers

For testing HTTP interactions, the project uses:

```go
func setupMockServer() *httptest.Server {
    return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Simulate various backend responses
    }))
}
```

### Temporary Test Directories

Tests use the Go testing package's `t.TempDir()` to create isolated test environments:

```go
func TestCacheStorage(t *testing.T) {
    tempDir := t.TempDir()
    cache, err := cache.New(tempDir, 1024*1024*10)
    // Test operations...
}
```

## Race Condition Testing

The project employs specific strategies to detect and prevent race conditions:

1. Tests are run with Go's race detector: `go test -race ./...`
2. Concurrency-focused tests intentionally create high contention
3. Stress tests run operations in parallel to expose synchronization issues
4. Timeouts prevent deadlocks during tests

## Test Data Management

The project includes:

1. Sample Debian/Ubuntu package files
2. Repository index file templates
3. Utilities to generate test data programmatically
4. Compressed sample repositories for realistic testing

## Testing in CI/CD Pipeline

The continuous integration setup includes:

1. GitHub Actions workflow for automated testing
2. Matrix testing across multiple Go versions
3. Caching of dependencies for faster builds
4. Performance comparison with previous baseline
5. Docker-based integration test environments
