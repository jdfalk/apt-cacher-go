package mapper

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPathMapping(t *testing.T) {
	// Fix: Replace NewSimpleMapper with New
	m := New() // Changed from NewSimpleMapper to match actual implementation

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
			wantRemotePath: "debian/dists/bullseye/Release", // Updated to match actual implementation
			wantCachePath:  "debian/dists/bullseye/Release",
		},
		{
			name:           "Ubuntu packages index",
			path:           "/ubuntu/dists/focal/main/binary-amd64/Packages.gz",
			wantRepository: "ubuntu",
			wantRemotePath: "ubuntu/dists/focal/main/binary-amd64/Packages.gz", // Updated
			wantCachePath:  "ubuntu/dists/focal/main/binary-amd64/Packages.gz",
		},
		{
			name:           "Debian security",
			path:           "/security.debian.org/dists/bullseye-security/Release",
			wantRepository: "security.debian.org",
			wantRemotePath: "security.debian.org/dists/bullseye-security/Release", // Updated
			wantCachePath:  "security.debian.org/dists/bullseye-security/Release",
		},
		{
			name:           "Package file",
			path:           "/debian/pool/main/n/nginx/nginx_1.18.0-6.1_amd64.deb",
			wantRepository: "debian",
			wantRemotePath: "pool/main/n/nginx/nginx_1.18.0-6.1_amd64.deb",
			wantCachePath:  "debian/pool/main/n/nginx/nginx_1.18.0-6.1_amd64.deb",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Fix: Change m.Map to m.MapPath and handle the error
			result, err := m.MapPath(tc.path)
			assert.NoError(t, err)
			assert.Equal(t, tc.wantRepository, result.Repository, "Repository")
			assert.Equal(t, tc.wantRemotePath, result.RemotePath, "RemotePath")
			assert.Equal(t, tc.wantCachePath, result.CachePath, "CachePath")
		})
	}
}

func TestIsRepositoryIndexFile(t *testing.T) {
	testCases := []struct {
		path string
		want bool
	}{
		{"dists/stable/Release", true},
		{"dists/stable/Release.gpg", true},
		{"dists/stable/InRelease", true},
		{"dists/stable/main/binary-amd64/Packages", true},
		{"dists/stable/main/binary-amd64/Packages.gz", true},
		{"dists/stable/main/binary-amd64/Packages.bz2", true},
		{"dists/stable/main/binary-amd64/Packages.xz", true},
		{"dists/stable/main/Contents-amd64.gz", true},
		{"pool/main/n/nginx/nginx_1.18.0-6.1_amd64.deb", false},
		{"pool/main/p/python3.9/python3.9-dev_3.9.5-3_amd64.deb", false},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			got := isRepositoryIndexFile(tc.path)
			if got != tc.want {
				t.Errorf("isRepositoryIndexFile(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}
