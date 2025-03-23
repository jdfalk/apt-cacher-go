package mapper

import (
	"testing"
)

func TestPathMapping(t *testing.T) {
	mapper := New()

	testCases := []struct {
		name           string
		path           string
		wantRepository string
		wantRemotePath string
		wantIsIndex    bool
	}{
		{
			name:           "Debian package",
			path:           "/debian/pool/main/n/nginx/nginx_1.18.0-6.1_amd64.deb",
			wantRepository: "debian",
			wantRemotePath: "pool/main/n/nginx/nginx_1.18.0-6.1_amd64.deb",
			wantIsIndex:    false,
		},
		{
			name:           "Ubuntu package",
			path:           "/ubuntu/pool/main/p/python3.9/python3.9_3.9.5-3_amd64.deb",
			wantRepository: "ubuntu",
			wantRemotePath: "pool/main/p/python3.9/python3.9_3.9.5-3_amd64.deb",
			wantIsIndex:    false,
		},
		{
			name:           "Debian release file",
			path:           "/debian/dists/bullseye/Release",
			wantRepository: "debian",
			wantRemotePath: "dists/bullseye/Release",
			wantIsIndex:    true,
		},
		{
			name:           "Ubuntu packages index",
			path:           "/ubuntu/dists/focal/main/binary-amd64/Packages.gz",
			wantRepository: "ubuntu",
			wantRemotePath: "dists/focal/main/binary-amd64/Packages.gz",
			wantIsIndex:    true,
		},
		{
			name:           "Debian security",
			path:           "/security.debian.org/dists/bullseye-security/Release",
			wantRepository: "security.debian.org",
			wantRemotePath: "dists/bullseye-security/Release",
			wantIsIndex:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := mapper.MapPath(tc.path)
			if err != nil {
				t.Fatalf("MapPath(%q) error: %v", tc.path, err)
			}

			if result.Repository != tc.wantRepository {
				t.Errorf("Repository = %q, want %q", result.Repository, tc.wantRepository)
			}

			if result.RemotePath != tc.wantRemotePath {
				t.Errorf("RemotePath = %q, want %q", result.RemotePath, tc.wantRemotePath)
			}

			if result.IsIndex != tc.wantIsIndex {
				t.Errorf("IsIndex = %v, want %v", result.IsIndex, tc.wantIsIndex)
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
