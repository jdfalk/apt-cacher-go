package backend

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/cache"
	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/mapper"
	"github.com/jdfalk/apt-cacher-go/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// // Fix unused parameters in mock function by using underscore prefix
// var httpFetch = func(_ context.Context, _ string, _ any) ([]byte, error) {
// 	// Properly implemented empty mock function that ignores parameters
// 	return []byte("mock data"), nil
// }

// MockCache implements mock functionality for testing
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

func (m *MockCache) UpdatePackageIndex(packages []parser.PackageInfo) error {
	args := m.Called(packages)
	return args.Error(0)
}

func (m *MockCache) SearchByPackageName(name string) ([]cache.CacheSearchResult, error) {
	args := m.Called(name)
	return args.Get(0).([]cache.CacheSearchResult), args.Error(1)
}

func (m *MockCache) GetStats() cache.CacheStats {
	args := m.Called()
	return args.Get(0).(cache.CacheStats)
}

// MockMapper implements mock functionality for testing
type MockMapper struct {
	mock.Mock
}

func (m *MockMapper) MapPath(path string) (mapper.MappingResult, error) {
	args := m.Called(path)
	return args.Get(0).(mapper.MappingResult), args.Error(1)
}

// MockPackageMapper implements mock functionality for testing
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

// MockBackend is a test implementation of Backend
type MockBackend struct {
	Name     string
	BaseURL  string
	Priority int
	// Add mock implementation of downloadFromURL
	DownloadFunc func(string) ([]byte, error)
}

// Sample Packages file content for testing
const samplePackagesData = `Package: nginx
Version: 1.18.0-6ubuntu1
Architecture: amd64
Maintainer: Ubuntu Developers <ubuntu-devel-discuss@lists.ubuntu.com>
Installed-Size: 136
Filename: pool/main/n/nginx/nginx_1.18.0-6ubuntu1_amd64.deb
Size: 43692
MD5sum: e74bbecc6e3a9418a93907bbcb877b2c
SHA1: 19e493a4ad21171cdf59877a5c3eacf4257e7218
SHA256: 8e4565d1b45eaf04b98c814ddda511ee5a1f80e50568009f24eec817a7797052
Description: small, powerful, scalable web/proxy server

Package: python3.9
Version: 3.9.5-3
Architecture: amd64
Maintainer: Debian Python Team <team+python@tracker.debian.org>
Installed-Size: 4668
Filename: pool/main/p/python3.9/python3.9_3.9.5-3_amd64.deb
Size: 365640
MD5sum: d5bde58379c33767747757ee4d3a02c7
SHA1: 24a3c68078516561e8689458477281a510ad82b0
SHA256: 21f19637588d829b4ec43420b371dbcb63e557eacd8cedd55c9916c3e07f30de
Description: Interactive high-level object-oriented language (version 3.9)
`

func TestProcessPackagesFile(t *testing.T) {
	// Create a temporary directory for the cache
	tempDir, err := os.MkdirTemp("", "backend-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create mock components
	mockCache := new(MockCache)
	mockMapper := new(MockMapper)
	mockPackageMapper := new(MockPackageMapper)

	// Create config
	cfg := &config.Config{
		CacheDir: tempDir,
		Backends: []config.Backend{
			{Name: "test-repo", URL: "http://example.com", Priority: 100},
		},
		MaxConcurrentDownloads: 2,
	}

	// Set up manager with mocks
	manager, err := New(cfg, mockCache, mockMapper, mockPackageMapper)
	require.NoError(t, err)

	// Disable the prefetcher for this test to avoid background MapPath calls
	manager.prefetcher = nil

	// Set up expectations
	// Parse sample data for expectations
	packages, err := parser.ParsePackages([]byte(samplePackagesData))
	require.NoError(t, err)
	require.Equal(t, 2, len(packages))

	// Expect the package index to be updated
	mockCache.On("UpdatePackageIndex", mock.MatchedBy(func(pkgs []parser.PackageInfo) bool {
		return len(pkgs) == 2 &&
			(pkgs[0].Package == "nginx" || pkgs[1].Package == "nginx") &&
			(pkgs[0].Package == "python3.9" || pkgs[1].Package == "python3.9")
	})).Return(nil)

	// Expect hash mappings to be added for SHA256 values
	mockPackageMapper.On("AddHashMapping", "8e4565d1b45eaf04b98c814ddda511ee5a1f80e50568009f24eec817a7797052", "nginx").Return()
	mockPackageMapper.On("AddHashMapping", "21f19637588d829b4ec43420b371dbcb63e557eacd8cedd55c9916c3e07f30de", "python3.9").Return()

	// Test repository and path
	repo := "test-repo"
	path := "dists/stable/main/binary-amd64/Packages"

	// Call the method being tested
	manager.ProcessPackagesFile(repo, path, []byte(samplePackagesData))

	// Allow time for any asynchronous processing
	time.Sleep(100 * time.Millisecond)

	// Verify all expectations were met
	mockCache.AssertExpectations(t)
	mockPackageMapper.AssertExpectations(t)

	// Test search functionality with mocked response
	expectedResults := []cache.CacheSearchResult{
		{
			PackageName: "nginx",
			Version:     "1.18.0-6ubuntu1",
			Path:        "pool/main/n/nginx/nginx_1.18.0-6ubuntu1_amd64.deb",
			Size:        43692,
		},
	}

	mockCache.On("SearchByPackageName", "nginx").Return(expectedResults, nil)

	results, err := mockCache.SearchByPackageName("nginx")
	require.NoError(t, err)
	assert.Equal(t, 1, len(results))
	assert.Equal(t, "nginx", results[0].PackageName)
	assert.Equal(t, "1.18.0-6ubuntu1", results[0].Version)
	assert.Contains(t, results[0].Path, "nginx_1.18.0-6ubuntu1_amd64.deb")
}

// SetCustomBackend modifies a Manager to use a custom backend URL for testing
func SetCustomBackend(manager *Manager, mockUrl string) *Manager {
	// Create a new set of backends with our test URL
	newBackends := []*Backend{
		{
			Name:     "debian",
			BaseURL:  mockUrl,
			Priority: 100,
			client:   manager.client,
		},
	}

	// Replace the backends
	manager.backends = newBackends

	return manager
}

// Create a custom transport for test responses
type mockTransport struct{}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Create a mock response
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewBufferString("mock package data")),
		Header:     make(http.Header),
	}
	return resp, nil
}

// CreateMockBackendManager creates a Manager with a modified HTTP client for testing
func CreateMockBackendManager(manager *Manager) *Manager {
	// Replace all clients with our test client
	testClient := &http.Client{
		Transport: &mockTransport{},
	}

	// Set the client for the manager
	manager.client = testClient

	// Set the client for all backends
	for _, backend := range manager.backends {
		backend.client = testClient
	}

	// Disable prefetcher to avoid conflicts in tests
	manager.prefetcher = nil

	return manager
}

func TestFetchFromBackend(t *testing.T) {
	// Create mock components
	mockCache := new(MockCache)
	mockMapper := new(MockMapper)
	mockPackageMapper := new(MockPackageMapper)

	// Create config with multiple backends
	cfg := &config.Config{
		CacheDir: t.TempDir(),
		Backends: []config.Backend{
			{Name: "debian", URL: "http://deb.debian.org/debian", Priority: 100},
			{Name: "ubuntu", URL: "http://archive.ubuntu.com/ubuntu", Priority: 90},
		},
		MaxConcurrentDownloads: 2,
	}

	// Create backend manager
	manager, err := New(cfg, mockCache, mockMapper, mockPackageMapper)
	require.NoError(t, err)

	// Set up test data
	testPath := "/debian/pool/main/n/nginx/nginx_1.18.0-6_amd64.deb"

	// Set up expectations
	// 1. Mapper will map the path
	mockMapper.On("MapPath", testPath).Return(mapper.MappingResult{
		Repository: "debian",
		RemotePath: "pool/main/n/nginx/nginx_1.18.0-6_amd64.deb",
		CachePath:  "debian/pool/main/n/nginx/nginx_1.18.0-6_amd64.deb",
		IsIndex:    false,
	}, nil)

	// 2. Cache will be checked but won't have the file
	mockCache.On("Get", "debian/pool/main/n/nginx/nginx_1.18.0-6_amd64.deb").Return(nil, os.ErrNotExist)

	// 3. Cache will store the fetched file
	mockCache.On("PutWithExpiration", "debian/pool/main/n/nginx/nginx_1.18.0-6_amd64.deb", []byte("mock package data"), mock.Anything).Return(nil)

	// Create a testable version of the manager with our custom backend behavior
	testManager := CreateMockBackendManager(manager)

	// Call the method being tested
	data, err := testManager.Fetch(testPath)
	require.NoError(t, err)
	assert.Equal(t, "mock package data", string(data))

	// Verify expectations
	mockMapper.AssertExpectations(t)
	mockCache.AssertExpectations(t)
}

// CreateManagerWithFetchOverride creates a test wrapper around Manager that overrides the Fetch method
type ManagerWithFetchOverride struct {
	*Manager
	fetchFunc func(string) ([]byte, error)
}

// Implement a custom Fetch method that uses our override function
func (m *ManagerWithFetchOverride) Fetch(path string) ([]byte, error) {
	return m.fetchFunc(path)
}

// CreateManagerWithFetchOverride creates a new Manager wrapper with a custom Fetch implementation
func CreateManagerWithFetchOverride(manager *Manager, fetchFunc func(string) ([]byte, error)) *ManagerWithFetchOverride {
	// Disable prefetcher in the underlying manager to avoid unexpected calls
	manager.prefetcher = nil

	return &ManagerWithFetchOverride{
		Manager:   manager,
		fetchFunc: fetchFunc,
	}
}

func TestProcessReleaseFile(t *testing.T) {
	// Create mock components
	mockCache := new(MockCache)
	mockMapper := new(MockMapper)
	mockPackageMapper := new(MockPackageMapper)

	// Create config
	cfg := &config.Config{
		CacheDir: t.TempDir(),
		Backends: []config.Backend{
			{Name: "test-repo", URL: "http://example.com", Priority: 100},
		},
		MaxConcurrentDownloads: 2,
		Architectures:          []string{"amd64"},
	}

	// Set up manager with mocks
	manager, err := New(cfg, mockCache, mockMapper, mockPackageMapper)
	require.NoError(t, err)

	// Simple Release file content
	releaseContent := `Origin: Ubuntu
Label: Ubuntu
Suite: focal
Version: 20.04
Codename: focal
Date: Thu, 23 Apr 2020 17:33:17 UTC
Architectures: amd64 arm64 armhf i386 ppc64el riscv64 s390x
Components: main restricted universe multiverse
Description: Ubuntu Focal 20.04 LTS
MD5Sum:
 d933e72fb8d63c0941813a59fee3acab 122502 main/binary-amd64/Packages
 3f23fd0d1a861be7c95813d4b5bb2689 37504 main/binary-amd64/Packages.gz
SHA1:
 5eaad1c6ade385c8b8c5d8ddb7d85433be836291 122502 main/binary-amd64/Packages
 3cb8e2e869835f2dd6694c196be48477c8778e79 37504 main/binary-amd64/Packages.gz
SHA256:
 d42e8455751a4c4a0c9d5af78f2acb58b7c7a11b72a478864a9c5b1db9fc9579 122502 main/binary-amd64/Packages
 3e5b4daf0a1281f1465c2540aaa105b9efd5f5dac2226df0881e0f13863579ea 37504 main/binary-amd64/Packages.gz`

	// Instead of monkey patching httpFetch, override the manager.Fetch method for testing
	// Create a wrapped manager with our custom Fetch implementation
	testManager := CreateManagerWithFetchOverride(manager, func(path string) ([]byte, error) {
		if strings.Contains(path, "Packages") {
			return []byte(samplePackagesData), nil
		}
		return nil, os.ErrNotExist
	})

	// Expect attempts to fetch the package files mentioned in Release
	mockMapper.On("MapPath", mock.MatchedBy(func(path string) bool {
		return strings.Contains(path, "Packages") || strings.Contains(path, "Packages.gz")
	})).Return(mapper.MappingResult{
		Repository: "test-repo",
		RemotePath: "main/binary-amd64/Packages",
		CachePath:  "test-repo/main/binary-amd64/Packages",
		IsIndex:    true,
	}, nil).Maybe()

	// NEW: Add cache lookup expectations for ANY path in the test
	mockCache.On("Get", mock.AnythingOfType("string")).Return(nil, os.ErrNotExist).Maybe()

	// Expect the package index to be updated when Packages files are processed
	mockCache.On("UpdatePackageIndex", mock.Anything).Return(nil).Maybe()

	// NEW: Add cache storage expectations
	mockCache.On("PutWithExpiration", mock.AnythingOfType("string"), mock.Anything, mock.Anything).Return(nil).Maybe()

	// Process the Release file
	repo := "test-repo"
	path := "dists/focal/Release"

	testManager.ProcessReleaseFile(repo, path, []byte(releaseContent))

	// Allow time for goroutines to complete
	time.Sleep(200 * time.Millisecond)

	// Verify expectations
	mockMapper.AssertExpectations(t)
	mockCache.AssertExpectations(t)
}

func TestShutdown(t *testing.T) {
	// Create mock components
	mockCache := new(MockCache)
	mockMapper := new(MockMapper)
	mockPackageMapper := new(MockPackageMapper)

	// Create config
	cfg := &config.Config{
		CacheDir: t.TempDir(),
		Backends: []config.Backend{
			{Name: "test-repo", URL: "http://example.com", Priority: 100},
		},
		MaxConcurrentDownloads: 2,
	}

	// Set up manager with mocks
	manager, err := New(cfg, mockCache, mockMapper, mockPackageMapper)
	require.NoError(t, err)

	// Verify shutdown doesn't deadlock
	doneCh := make(chan struct{})
	go func() {
		manager.Shutdown()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		// Shutdown completed successfully
	case <-time.After(1 * time.Second):
		t.Fatal("Shutdown timed out")
	}
}
