package backend

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/cache"
	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/mapper"
	"github.com/jdfalk/apt-cacher-go/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCache wraps the real cache for testing purposes
type TestCache struct {
	*cache.Cache
	PackageMapper *mapper.PackageMapper // Used for hash-to-package mapping in UpdatePackageIndex
}

// GetPackageMapper returns the package mapper - adding this method silences the unused field warning
func (tc *TestCache) GetPackageMapper() *mapper.PackageMapper {
	return tc.PackageMapper
}

// GetPackageIndex returns a package index - stub implementation for tests
func (tc *TestCache) GetPackageIndex() *packageIndex {
	// Return a simple in-memory package index for testing
	return &packageIndex{
		packages: make(map[string]parser.PackageInfo),
	}
}

// Define a package index type for the test
type packageIndex struct {
	packages map[string]parser.PackageInfo
	mutex    sync.Mutex
}

// AddPackage adds a package to the index
func (pi *packageIndex) AddPackage(pkg parser.PackageInfo) {
	pi.mutex.Lock()
	defer pi.mutex.Unlock()
	pi.packages[pkg.Package] = pkg
}

// Fix SearchByPackageName to convert pkg.Size from string to int64
func (tc *TestCache) SearchByPackageName(name string) ([]cache.CacheSearchResult, error) {
	results := []cache.CacheSearchResult{}

	index := tc.GetPackageIndex()
	if index == nil {
		return results, nil
	}

	for pkgName, pkg := range index.packages {
		if strings.Contains(pkgName, name) {
			// Convert Size from string to int64
			var size int64
			if sizeVal, err := strconv.ParseInt(pkg.Size, 10, 64); err == nil {
				size = sizeVal
			}

			results = append(results, cache.CacheSearchResult{
				Path:        pkg.Filename,
				PackageName: pkg.Package,
				Version:     pkg.Version,
				Size:        size, // Use converted int64 value
				LastAccess:  time.Now(),
				IsCached:    true,
			})
		}
	}

	return results, nil
}

// UpdatePackageIndex is our test implementation that adds packages to the index
func (tc *TestCache) UpdatePackageIndex(packages []parser.PackageInfo) error {
	// Add packages to the cache's packageIndex
	for _, pkg := range packages {
		if tc.GetPackageIndex() != nil {
			tc.GetPackageIndex().AddPackage(pkg)

			// Extract hash from filename if it contains by-hash
			if hashStart := strings.Index(pkg.Filename, "/by-hash/"); hashStart >= 0 {
				hashPart := pkg.Filename[hashStart+len("/by-hash/SHA256/"):]
				if endHash := strings.Index(hashPart, "/"); endHash >= 0 {
					hashPart = hashPart[:endHash]
				}

				// Add hash mapping for search
				if hashPart != "" && pkg.Package != "" {
					tc.PackageMapper.AddHashMapping(hashPart, pkg.Package) // Fixed to use exported field
				}
			} else if pkg.SHA256 != "" {
				// If we have the SHA256 hash directly, add that mapping
				tc.PackageMapper.AddHashMapping(pkg.SHA256, pkg.Package) // Fixed to use exported field
			}
		}

		// Call the real UpdatePackageIndex method if it's accessible
		if err := tc.Cache.UpdatePackageIndex(packages); err != nil {
			return err
		}
	}
	return nil
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

	// Create cache and mapper instances
	cfg := &config.Config{
		CacheDir: tempDir,
		Backends: []config.Backend{
			{Name: "test-repo", URL: "http://example.com", Priority: 100},
		},
		MaxConcurrentDownloads: 2,
	}

	realCache, err := cache.New(tempDir, 1024*1024*100) // 100MB cache
	require.NoError(t, err)

	// Create a package mapper for testing
	packageMapper := mapper.NewPackageMapper()

	// Create our test cache wrapper
	testCache := &TestCache{
		Cache:         realCache,
		PackageMapper: packageMapper,
	}

	pathMapper := mapper.New()
	manager, err := New(cfg, testCache.Cache, pathMapper, packageMapper)
	require.NoError(t, err)

	// Test processing packages file
	repo := "test-repo"
	path := "dists/stable/main/binary-amd64/Packages"

	// Call the method we're testing
	manager.ProcessPackagesFile(repo, path, []byte(samplePackagesData))

	// Verify the packages were added to the index
	// Wait a short time for background goroutine to complete
	time.Sleep(50 * time.Millisecond)

	// Verify that we can get the package mapper
	mapper := testCache.GetPackageMapper()
	assert.NotNil(t, mapper, "Package mapper should not be nil")

	// Search for one of the packages
	results, err := testCache.SearchByPackageName("nginx")
	require.NoError(t, err)

	// Verify results
	assert.NotEmpty(t, results, "Should have found nginx package")
	if len(results) > 0 {
		assert.Equal(t, "nginx", results[0].PackageName)
		assert.Equal(t, "1.18.0-6ubuntu1", results[0].Version)
		assert.Contains(t, results[0].Path, "pool/main/n/nginx/nginx_1.18.0-6ubuntu1_amd64.deb")
	}
}
