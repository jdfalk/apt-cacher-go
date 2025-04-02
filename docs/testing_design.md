<!-- file: docs/testing_design.md -->

# apt-cacher-go Testing Design Document

## Testing Strategy

apt-cacher-go employs a comprehensive testing strategy covering:

1. Unit tests for individual components
2. Integration tests for component interactions
3. End-to-end tests simulating actual client behavior
4. Performance benchmarks for critical paths
5. Concurrency tests to detect race conditions

### Test Environments

- Local development environment
- CI pipeline with automated tests
- Docker-based integration test environment
- Race detector enabled testing environment

## Unit Testing

Unit tests focus on testing individual components in isolation:

- Cache operations (store, retrieve, expire)
- Path mapping rules
- Backend connection handling
- Configuration parsing
- Metrics collection
- Memory monitoring functionality
- PebbleDB storage operations
- Prefetcher behavior

### Testing Frameworks

- Go's built-in testing package
- Testify for assertions and mocking
- httptest for HTTP handler testing
- Context-aware tests with timeouts and cancellation

## Integration Testing

Integration tests verify interactions between components:

- Cache and backend manager coordination
- Server handling of various request types
- End-to-end request flow with mocked backends
- HTTPS and protocol handling
- Memory pressure adaptation
- Prefetching and background tasks

### Test Data

- Sample package and index files
- Various repository structures
- Edge cases (large files, malformed requests)
- Repository signature files
- GPG key testing

## Performance Testing

Performance tests measure:

- Request throughput under various loads
- Memory usage patterns
- Cache hit ratio impact on performance
- Concurrent download capabilities
- Database operation efficiency
- Prefetch scaling behavior

### Benchmarks

- Single package download speed
- Multiple simultaneous client simulation
- Large repository index handling
- Cache pruning performance
- PebbleDB read/write performance
- Memory pressure handling

## Test Coverage

- Target: >80% code coverage
- Critical path coverage: >95%
- Automated coverage reporting in CI
- Special focus on error handling paths

## Test File Organization

```
/
├── internal/
│   ├── cache/
│   │   ├── cache_test.go
│   │   └── lru_test.go
│   ├── mapper/
│   │   └── mapper_test.go
│   ├── backend/
│   │   ├── backend_test.go
│   │   └── prefetcher_test.go
│   ├── server/
│   │   ├── server_test.go
│   │   ├── https_test.go
│   │   ├── admin_test.go
│   │   ├── health_test.go
│   │   └── memory_test.go
│   ├── storage/
│   │   └── pebbledb_test.go
│   ├── keymanager/
│   │   └── keymanager_test.go
│   ├── metrics/
│   │   ├── metrics_test.go
│   │   └── prometheus_test.go
│   ├── config/
│   │   └── config_test.go
│   ├── queue/
│   │   └── queue_test.go
│   ├── parser/
│   │   └── package_parser_test.go
│   └── security/
│       └── acl_test.go
└── integration/
    ├── basic_test.go
    ├── prefetch_test.go
    └── concurrent_test.go
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

## Common Test Patterns

### Mock Components

Most tests use mocks for dependencies:

```go
// Example mock of cache interface
type MockCache struct {
    mock.Mock
}

func (m *MockCache) Get(path string) (io.ReadCloser, error) {
    args := m.Called(path)
    return args.Get(0).(io.ReadCloser), args.Error(1)
}

func (m *MockCache) Put(path string, data io.Reader) error {
    args := m.Called(path, data)
    return args.Error(0)
}
```

### Test Fixtures

Tests use fixtures for standardized setup:

```go
type TestFixture struct {
    server      *Server
    mockCache   *MockCache
    mockBackend *MockBackend
    // Other mocked dependencies
    tempDir     string
    cleanup     func()
}

func NewTestFixture(t *testing.T) *TestFixture {
    // Setup test environment
    tempDir := t.TempDir()

    mockCache := new(MockCache)
    mockBackend := new(MockBackend)

    server := NewServer(Config{
        // Test configuration
    }, WithCache(mockCache), WithBackend(mockBackend))

    return &TestFixture{
        server:      server,
        mockCache:   mockCache,
        mockBackend: mockBackend,
        tempDir:     tempDir,
    }
}
```

### Test Helpers

Common test utility functions:

```go
// Create a test package for testing
func createTestPackage(t *testing.T, size int) (string, []byte) {
    data := make([]byte, size)
    // Fill with random data

    path := fmt.Sprintf("test_package_%d.deb", time.Now().UnixNano())
    return path, data
}

// Create a mock HTTP response
func mockHTTPResponse(code int, body string) *http.Response {
    return &http.Response{
        StatusCode: code,
        Body:       io.NopCloser(strings.NewReader(body)),
        Header:     make(http.Header),
    }
}
```

## Database Testing

PebbleDB tests require special handling:

```go
func TestDatabaseStore_SaveAndRetrieve(t *testing.T) {
    // Create temp directory for database
    tempDir := t.TempDir()

    // Create database store
    db, err := storage.NewDatabaseStore(tempDir, &config.Config{
        // Test configuration
    })
    require.NoError(t, err)

    // Clean up after test
    t.Cleanup(func() {
        db.Close()
    })

    // Test data
    entry := CacheEntry{
        Path:         "test.deb",
        Size:         1024,
        LastAccessed: time.Now(),
        LastModified: time.Now(),
        HitCount:     1,
    }

    // Test operations
    err = db.SaveCacheEntry(entry)
    require.NoError(t, err)

    retrievedEntry, exists, err := db.GetCacheEntry("test.deb")
    require.NoError(t, err)
    assert.True(t, exists)
    assert.Equal(t, entry.Size, retrievedEntry.Size)
    assert.Equal(t, entry.HitCount, retrievedEntry.HitCount)
}
```

## HTTP Testing

Server handlers are tested with httptest:

```go
func TestHandlePackageRequest(t *testing.T) {
    fixture := NewTestFixture(t)

    // Setup expectations
    packagePath := "/ubuntu/pool/main/n/nginx/nginx_1.18.0-0ubuntu1_amd64.deb"
    packageData := []byte("mock package data")

    fixture.mockMapper.On("MapPath", packagePath).Return("ubuntu", packagePath, nil)
    fixture.mockCache.On("Get", packagePath).Return(nil, os.ErrNotExist)
    fixture.mockBackend.On("Fetch", "ubuntu", packagePath).Return(
        io.NopCloser(bytes.NewReader(packageData)), nil)
    fixture.mockCache.On("Put", packagePath, mock.Anything).Return(nil)

    // Create test request
    req := httptest.NewRequest("GET", "http://localhost:3142"+packagePath, nil)
    w := httptest.NewRecorder()

    // Handle request
    fixture.server.handlePackageRequest(w, req)

    // Assertions
    assert.Equal(t, http.StatusOK, w.Code)
    assert.Equal(t, packageData, w.Body.Bytes())

    // Verify expectations
    fixture.mockMapper.AssertExpectations(t)
    fixture.mockCache.AssertExpectations(t)
    fixture.mockBackend.AssertExpectations(t)
}
```

## Concurrency Testing

Tests ensure thread safety:

```go
func TestConcurrentAccess(t *testing.T) {
    db, err := storage.NewDatabaseStore(t.TempDir(), &config.Config{})
    require.NoError(t, err)
    t.Cleanup(func() { db.Close() })

    const goroutines = 20
    const iterations = 50

    var wg sync.WaitGroup
    wg.Add(goroutines)

    // Launch goroutines to access DB concurrently
    for i := 0; i < goroutines; i++ {
        go func(id int) {
            defer wg.Done()

            for j := 0; j < iterations; j++ {
                // Write operations
                entry := CacheEntry{
                    Path:         fmt.Sprintf("test%d_%d.deb", id, j),
                    Size:         int64(id * j),
                    LastAccessed: time.Now(),
                    LastModified: time.Now(),
                    HitCount:     1,
                }
                err := db.SaveCacheEntry(entry)
                assert.NoError(t, err)

                // Read operations
                _, _, err = db.GetCacheEntry(entry.Path)
                assert.NoError(t, err)
            }
        }(i)
    }

    wg.Wait()
}
```

## Context Cancellation Testing

Tests verify clean shutdown:

```go
func TestContextCancellation(t *testing.T) {
    fixture := NewTestFixture(t)

    // Create cancellable context
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Setup long operation to be cancelled
    longFetchPath := "/ubuntu/dists/jammy/main/binary-amd64/Packages.gz"
    slowResponse := func() io.ReadCloser {
        pr, pw := io.Pipe()
        go func() {
            defer pw.Close()
            // Write slowly
            for i := 0; i < 10; i++ {
                select {
                case <-ctx.Done():
                    return
                case <-time.After(100 * time.Millisecond):
                    pw.Write([]byte("data chunk"))
                }
            }
        }()
        return pr
    }

    fixture.mockBackend.On("Fetch", "ubuntu", longFetchPath).Return(slowResponse(), nil)

    // Start download in background
    resultCh := make(chan error)
    go func() {
        _, err := fixture.server.fetchWithContext(ctx, "ubuntu", longFetchPath)
        resultCh <- err
    }()

    // Cancel context
    time.Sleep(250 * time.Millisecond)
    cancel()

    // Check for expected cancellation error
    select {
    case err := <-resultCh:
        assert.ErrorIs(t, err, context.Canceled)
    case <-time.After(2 * time.Second):
        t.Fatal("Timeout waiting for cancellation")
    }
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
