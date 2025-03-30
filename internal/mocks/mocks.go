package mocks

import (
	"context"
	"net/http"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/cache"
	"github.com/jdfalk/apt-cacher-go/internal/mapper"
	"github.com/jdfalk/apt-cacher-go/internal/metrics"
	"github.com/jdfalk/apt-cacher-go/internal/parser"
	"github.com/stretchr/testify/mock"
)

// MockBackendManager mocks the BackendManager interface
type MockBackendManager struct {
	mock.Mock
}

func (m *MockBackendManager) Fetch(path string) ([]byte, error) {
	args := m.Called(path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockBackendManager) ProcessPackagesFile(repo, path string, data []byte) {
	m.Called(repo, path, data)
}

func (m *MockBackendManager) ProcessReleaseFile(repo, path string, data []byte) {
	m.Called(repo, path, data)
}

func (m *MockBackendManager) ForceCleanupPrefetcher() int {
	args := m.Called()
	return args.Int(0)
}

func (m *MockBackendManager) PrefetchOnStartup(ctx context.Context) {
	m.Called(ctx)
}

func (m *MockBackendManager) KeyManager() interface{} {
	args := m.Called()
	return args.Get(0)
}

// MockKeyManager implements the KeyManager interface for testing
type MockKeyManager struct {
	mock.Mock
}

func (m *MockKeyManager) HasKey(keyID string) bool {
	args := m.Called(keyID)
	return args.Bool(0)
}

func (m *MockKeyManager) GetKeyPath(keyID string) string {
	args := m.Called(keyID)
	return args.String(0)
}

func (m *MockKeyManager) FetchKey(keyID string) error {
	args := m.Called(keyID)
	return args.Error(0)
}

func (m *MockKeyManager) DetectKeyError(data []byte) (string, bool) {
	args := m.Called(data)
	return args.String(0), args.Bool(1)
}

// MockCache mocks the Cache interface
type MockCache struct {
	mock.Mock
}

func (m *MockCache) Get(path string) ([]byte, error) {
	args := m.Called(path)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockCache) Put(path string, data []byte) error {
	args := m.Called(path, data)
	return args.Error(0)
}

func (m *MockCache) PutWithExpiration(path string, data []byte, ttl time.Duration) error {
	args := m.Called(path, data, ttl)
	return args.Error(0)
}

func (m *MockCache) IsFresh(path string) bool {
	args := m.Called(path)
	return args.Bool(0)
}

func (m *MockCache) Exists(path string) bool {
	args := m.Called(path)
	return args.Bool(0)
}

func (m *MockCache) GetStats() cache.CacheStats {
	args := m.Called()
	return args.Get(0).(cache.CacheStats)
}

func (m *MockCache) GetLastModified(path string) time.Time {
	args := m.Called(path)
	return args.Get(0).(time.Time)
}

func (m *MockCache) SearchByPackageName(name string) ([]cache.CacheSearchResult, error) {
	args := m.Called(name)
	return args.Get(0).([]cache.CacheSearchResult), args.Error(1)
}

func (m *MockCache) UpdatePackageIndex(packages []parser.PackageInfo) error {
	args := m.Called(packages)
	return args.Error(0)
}

// For compatibility with optional interface methods
func (m *MockCache) Clear() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockCache) FlushExpired() (int, error) {
	args := m.Called()
	return args.Int(0), args.Error(1)
}

func (m *MockCache) Search(query string) ([]string, error) {
	args := m.Called(query)
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockCache) Close() error {
	args := m.Called()
	return args.Error(0)
}

// MockPathMapper mocks the PathMapper interface
type MockPathMapper struct {
	mock.Mock
}

func (m *MockPathMapper) MapPath(path string) (mapper.MappingResult, error) {
	args := m.Called(path)
	return args.Get(0).(mapper.MappingResult), args.Error(1)
}

// MockPackageMapper mocks the PackageMapper interface
type MockPackageMapper struct {
	mock.Mock
}

func (m *MockPackageMapper) AddHashMapping(hash, packageName string) {
	m.Called(hash, packageName)
}

func (m *MockPackageMapper) GetPackageNameForHash(path string) string {
	args := m.Called(path)
	return args.String(0)
}

func (m *MockPackageMapper) ClearCache() {
	m.Called()
}

// MockMetricsCollector mocks the MetricsCollector interface
type MockMetricsCollector struct {
	mock.Mock
}

func (m *MockMetricsCollector) RecordRequest(path string, duration time.Duration, clientIP, packageName string) {
	m.Called(path, duration, clientIP, packageName)
}

func (m *MockMetricsCollector) RecordCacheHit(path string, size int64) {
	m.Called(path, size)
}

func (m *MockMetricsCollector) RecordCacheMiss(path string, size int64) {
	m.Called(path, size)
}

func (m *MockMetricsCollector) RecordError(path string) {
	m.Called(path)
}

func (m *MockMetricsCollector) RecordBytesServed(bytes int64) {
	m.Called(bytes)
}

func (m *MockMetricsCollector) GetStatistics() metrics.Statistics {
	args := m.Called()
	return args.Get(0).(metrics.Statistics)
}

func (m *MockMetricsCollector) GetTopPackages(limit int) []metrics.PackageStats {
	args := m.Called(limit)
	return args.Get(0).([]metrics.PackageStats)
}

func (m *MockMetricsCollector) GetTopClients(limit int) []metrics.ClientStats {
	args := m.Called(limit)
	return args.Get(0).([]metrics.ClientStats)
}

func (m *MockMetricsCollector) SetLastClientIP(ip string) {
	m.Called(ip)
}

func (m *MockMetricsCollector) SetLastFileSize(size int64) {
	m.Called(size)
}

// MockMemoryMonitor mocks the MemoryMonitorInterface
type MockMemoryMonitor struct {
	mock.Mock
}

func (m *MockMemoryMonitor) Start() {
	m.Called()
}

func (m *MockMemoryMonitor) Stop() {
	m.Called()
}

// Fixing the GetMemoryUsage signature conflict by using map[string]any
// This matches the implementation in test_helpers.go
func (m *MockMemoryMonitor) GetMemoryUsage() map[string]any {
	args := m.Called()
	return args.Get(0).(map[string]any)
}

// MockResponseWriter implements the ResponseWriter interface for testing
type MockResponseWriter struct {
	mock.Mock
	StatusCode    int
	BytesWritten_ int64
	HeaderMap     http.Header
}

func (m *MockResponseWriter) Header() http.Header {
	return m.HeaderMap
}

func (m *MockResponseWriter) Write(data []byte) (int, error) {
	args := m.Called(data)
	m.BytesWritten_ += int64(len(data))
	return args.Int(0), args.Error(1)
}

func (m *MockResponseWriter) WriteHeader(statusCode int) {
	m.Called(statusCode)
	m.StatusCode = statusCode
}

func (m *MockResponseWriter) Status() int {
	return m.StatusCode
}

func (m *MockResponseWriter) BytesWritten() int64 {
	return m.BytesWritten_
}

func NewMockResponseWriter() *MockResponseWriter {
	w := &MockResponseWriter{
		HeaderMap:  make(http.Header),
		StatusCode: http.StatusOK,
	}
	w.On("Write", mock.Anything).Return(0, nil).Maybe()
	w.On("WriteHeader", mock.Anything).Maybe()
	return w
}
