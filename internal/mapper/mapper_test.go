package mapper

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockMapper implements mock functionality for testing
type MockMapper struct {
	mock.Mock
}

func (m *MockMapper) MapPath(path string) (MappingResult, error) {
	args := m.Called(path)
	return args.Get(0).(MappingResult), args.Error(1)
}

func (m *MockMapper) AddRule(rule MappingRule) {
	m.Called(rule)
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

func TestPathMapping(t *testing.T) {
	m := New()

	testCases := []struct {
		name           string
		path           string
		wantRepository string
		wantRemotePath string
		wantCachePath  string
	}{
		{
			name:           "Debian release file",
			path:           "/debian/dists/bullseye/Release",
			wantRepository: "debian",
			wantRemotePath: "dists/bullseye/Release", // Fixed: Without repository prefix
			wantCachePath:  "debian/dists/bullseye/Release",
		},
		{
			name:           "Ubuntu packages index",
			path:           "/ubuntu/dists/focal/main/binary-amd64/Packages.gz",
			wantRepository: "ubuntu",
			wantRemotePath: "dists/focal/main/binary-amd64/Packages.gz", // Fixed: Without repository prefix
			wantCachePath:  "ubuntu/dists/focal/main/binary-amd64/Packages.gz",
		},
		{
			name:           "Debian security",
			path:           "/debian-security/dists/bullseye-security/Release",
			wantRepository: "security.debian.org",
			wantRemotePath: "dists/bullseye-security/Release", // Fixed
			wantCachePath:  "security.debian.org/dists/bullseye-security/Release",
		},
		{
			name:           "Package file",
			path:           "/debian/pool/main/h/hello/hello_2.10-2_amd64.deb",
			wantRepository: "debian",
			wantRemotePath: "pool/main/h/hello/hello_2.10-2_amd64.deb", // Fixed
			wantCachePath:  "debian/pool/main/h/hello/hello_2.10-2_amd64.deb",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := m.MapPath(tc.path)
			assert.NoError(t, err)
			assert.Equal(t, tc.wantRepository, result.Repository, "Repository")
			assert.Equal(t, tc.wantRemotePath, result.RemotePath, "RemotePath")
			assert.Equal(t, tc.wantCachePath, result.CachePath, "CachePath")
			// Verify the Rule field is populated
			assert.NotNil(t, result.Rule, "Rule should not be nil for valid paths")
		})
	}
}

func TestIsRepositoryIndexFile(t *testing.T) {
	testCases := []struct {
		name     string
		path     string
		expected bool
	}{
		{"dists/stable/Release", "dists/stable/Release", true},
		{"dists/stable/Release.gpg", "dists/stable/Release.gpg", true},
		{"dists/stable/InRelease", "dists/stable/InRelease", true},
		{"dists/stable/main/binary-amd64/Packages", "dists/stable/main/binary-amd64/Packages", true},
		{"dists/stable/main/binary-amd64/Packages.gz", "dists/stable/main/binary-amd64/Packages.gz", true},
		{"dists/stable/main/binary-amd64/Packages.bz2", "dists/stable/main/binary-amd64/Packages.bz2", true},
		{"dists/stable/main/binary-amd64/Packages.xz", "dists/stable/main/binary-amd64/Packages.xz", true},
		{"dists/stable/main/Contents-amd64.gz", "dists/stable/main/Contents-amd64.gz", true},
		{"pool/main/n/nginx/nginx_1.18.0-6.1_amd64.deb", "pool/main/n/nginx/nginx_1.18.0-6.1_amd64.deb", false},
		{"pool/main/p/python3.9/python3.9-dev_3.9.5-3_amd64.deb", "pool/main/p/python3.9/python3.9-dev_3.9.5-3_amd64.deb", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := isRepositoryIndexFile(tc.path)
			if got != tc.expected {
				t.Errorf("isRepositoryIndexFile(%q) = %v, want %v", tc.path, got, tc.expected)
			}
		})
	}
}

func TestPackageMapper(t *testing.T) {
	pm := NewPackageMapper()

	// Test adding a hash mapping
	packageName := "nginx"
	hash := "8e4565d1b45eaf04b98c814ddda511ee5a1f80e50568009f24eec817a7797052"
	pm.AddHashMapping(hash, packageName)

	// Test retrieving package name from hash
	testCases := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "Valid hash path",
			path:     "/ubuntu-ports/dists/oracular-updates/main/binary-arm64/by-hash/SHA256/8e4565d1b45eaf04b98c814ddda511ee5a1f80e50568009f24eec817a7797052",
			expected: "nginx",
		},
		{
			name:     "Unknown hash",
			path:     "/ubuntu-ports/dists/oracular-updates/main/binary-arm64/by-hash/SHA256/unknownhash",
			expected: "",
		},
		{
			name:     "Not a hash path",
			path:     "/ubuntu/dists/jammy/Release",
			expected: "",
		},
		{
			name:     "Empty path",
			path:     "",
			expected: "",
		},
		{
			name:     "Invalid path format",
			path:     "/by-hash/",
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := pm.GetPackageNameForHash(tc.path)
			assert.Equal(t, tc.expected, result)
		})
	}

	// Test multiple hashes
	pm.AddHashMapping("another-hash", "python3.9")

	result1 := pm.GetPackageNameForHash("/path/by-hash/SHA256/8e4565d1b45eaf04b98c814ddda511ee5a1f80e50568009f24eec817a7797052")
	assert.Equal(t, "nginx", result1)

	result2 := pm.GetPackageNameForHash("/path/by-hash/SHA256/another-hash")
	assert.Equal(t, "python3.9", result2)
}

func TestMockPackageMapper(t *testing.T) {
	// Create mock
	mockPM := new(MockPackageMapper)

	// Set expectations
	mockPM.On("AddHashMapping", "test-hash", "test-package").Return()
	mockPM.On("GetPackageNameForHash", "/path/by-hash/SHA256/test-hash").Return("test-package")
	mockPM.On("GetPackageNameForHash", "/path/by-hash/SHA256/unknown-hash").Return("")

	// Use mock
	mockPM.AddHashMapping("test-hash", "test-package")

	// Test retrieving known hash
	result := mockPM.GetPackageNameForHash("/path/by-hash/SHA256/test-hash")
	assert.Equal(t, "test-package", result)

	// Test retrieving unknown hash
	result = mockPM.GetPackageNameForHash("/path/by-hash/SHA256/unknown-hash")
	assert.Equal(t, "", result)

	// Verify all expectations were met
	mockPM.AssertExpectations(t)
}
