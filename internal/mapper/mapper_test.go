package mapper

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPathMapping(t *testing.T) {
	m := New()

	testCases := []struct {
		name           string
		path           string
		wantRepository string
		wantRemotePath string
		wantCachePath  string
	}{
		// Update tests to ensure cache paths don't duplicate repository names
		{
			name:           "Debian release file",
			path:           "/debian/dists/bullseye/Release",
			wantRepository: "debian",
			wantRemotePath: "dists/bullseye/Release", // Modified
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
			result, err := m.MapPath(tc.path)
			assert.NoError(t, err)
			assert.Equal(t, tc.wantRepository, result.Repository, "Repository")
			assert.Equal(t, tc.wantRemotePath, result.RemotePath, "RemotePath")
			assert.Equal(t, tc.wantCachePath, result.CachePath, "CachePath")
			// Add test for the new Rule field
			if tc.path != "/nonexistent/path" {
				assert.NotNil(t, result.Rule, "Rule should not be nil for valid paths")
			}
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
