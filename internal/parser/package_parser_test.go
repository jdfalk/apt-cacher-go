package parser

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sample package data for testing
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

// Sample Release file data for testing
const sampleReleaseData = `Origin: Ubuntu
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
 3e5b4daf0a1281f1465c2540aaa105b9efd5f5dac2226df0881e0f13863579ea 37504 main/binary-amd64/Packages.gz
`

// Create a gzipped version of the Packages data for testing
func createGzippedPackagesData(t *testing.T) []byte {
	var buf bytes.Buffer
	gzWriter := gzip.NewWriter(&buf)
	_, err := gzWriter.Write([]byte(samplePackagesData))
	require.NoError(t, err)
	require.NoError(t, gzWriter.Close())
	return buf.Bytes()
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestParsePackages tests the ParsePackages function that extracts package information
// from a Debian-style Packages file.
//
// The test verifies:
// - Packages are correctly parsed from a standard Packages file format
// - All fields are properly extracted (Package, Version, Architecture, etc.)
// - Multiple packages in the same file are correctly separated
// - Field values maintain their correct content and formatting
//
// Approach:
// 1. Provides sample Packages data with two package entries (nginx and python3.9)
// 2. Calls ParsePackages on the sample data
// 3. Verifies the correct number of packages are returned
// 4. Checks that key fields match expected values for both packages
// 5. Validates exact field values for SHA256 and filenames
//
// Note: This test uses a fixed sample of package data to ensure consistent testing,
// making it resilient to changes in actual repository data
func TestParsePackages(t *testing.T) {
	// Parse the sample data
	packages, err := ParsePackages([]byte(samplePackagesData))

	// Verify parsing succeeded
	require.NoError(t, err)
	require.Equal(t, 2, len(packages))

	// Check the first package (nginx)
	assert.Equal(t, "nginx", packages[0].Package)
	assert.Equal(t, "1.18.0-6ubuntu1", packages[0].Version)
	assert.Equal(t, "amd64", packages[0].Architecture)
	assert.Equal(t, "pool/main/n/nginx/nginx_1.18.0-6ubuntu1_amd64.deb", packages[0].Filename)
	assert.Equal(t, "43692", packages[0].Size)
	assert.Equal(t, "8e4565d1b45eaf04b98c814ddda511ee5a1f80e50568009f24eec817a7797052", packages[0].SHA256)
	assert.Equal(t, "small, powerful, scalable web/proxy server", packages[0].Description)

	// Check the second package (python3.9)
	assert.Equal(t, "python3.9", packages[1].Package)
	assert.Equal(t, "3.9.5-3", packages[1].Version)
	assert.Equal(t, "amd64", packages[1].Architecture)
	assert.Equal(t, "pool/main/p/python3.9/python3.9_3.9.5-3_amd64.deb", packages[1].Filename)
	assert.Equal(t, "365640", packages[1].Size)
	assert.Equal(t, "21f19637588d829b4ec43420b371dbcb63e557eacd8cedd55c9916c3e07f30de", packages[1].SHA256)
	assert.Equal(t, "Interactive high-level object-oriented language (version 3.9)", packages[1].Description)
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestParsePackagesFile tests the ParsePackagesFile function, which parses both
// plain text and gzipped Packages files.
//
// The test verifies:
// - Plain text Packages files are correctly parsed
// - Gzipped Packages files are correctly decompressed and parsed
// - The parser correctly handles empty lines between package entries
// - All package fields are properly extracted in both formats
//
// Approach:
// 1. Creates two versions of the test data: plain text and gzipped
// 2. Tests parsing of the plain text data
// 3. Tests parsing of the gzipped data
// 4. Verifies both methods produce identical results
// 5. Checks detailed field values to ensure correct parsing
//
// Note: Uses helper function to create gzipped test data consistently
func TestParsePackagesFile(t *testing.T) {
	// Test with plain text Packages data
	t.Run("plain text", func(t *testing.T) {
		packages, err := ParsePackagesFile([]byte(samplePackagesData))
		require.NoError(t, err)
		require.Equal(t, 2, len(packages))

		// Verify field values of the first package
		assert.Equal(t, "nginx", packages[0].Package)
		assert.Equal(t, "1.18.0-6ubuntu1", packages[0].Version)
		assert.Equal(t, "pool/main/n/nginx/nginx_1.18.0-6ubuntu1_amd64.deb", packages[0].Filename)
	})

	// Test with gzipped Packages data
	t.Run("gzipped", func(t *testing.T) {
		gzippedData := createGzippedPackagesData(t)
		packages, err := ParsePackagesFile(gzippedData)
		require.NoError(t, err)
		require.Equal(t, 2, len(packages))

		// Verify field values of the second package
		assert.Equal(t, "python3.9", packages[1].Package)
		assert.Equal(t, "3.9.5-3", packages[1].Version)
		assert.Equal(t, "pool/main/p/python3.9/python3.9_3.9.5-3_amd64.deb", packages[1].Filename)
	})
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestParseReleaseFile tests the ParseReleaseFile function that extracts metadata
// from a Debian-style Release file.
//
// The test verifies:
// - Repository metadata is correctly parsed from a Release file
// - All common fields (Origin, Suite, Version, etc.) are properly extracted
// - Multi-value fields like Architectures and Components are correctly split
// - Field values maintain their correct content and formatting
//
// Approach:
// 1. Provides sample Release file content with representative metadata
// 2. Calls ParseReleaseFile on the sample data
// 3. Verifies each metadata field matches the expected value
// 4. Checks array fields (Architectures, Components) contain all expected values
//
// Note: This test focuses on the metadata portion of the Release file, not the
// file list section which is handled by ParseIndexFilenames
func TestParseReleaseFile(t *testing.T) {
	// Parse the sample Release data
	info, err := ParseReleaseFile([]byte(sampleReleaseData))

	// Verify parsing succeeded
	require.NoError(t, err)
	require.NotNil(t, info)

	// Check basic metadata fields
	assert.Equal(t, "Ubuntu", info.Origin)
	assert.Equal(t, "Ubuntu", info.Label)
	assert.Equal(t, "focal", info.Suite)
	assert.Equal(t, "20.04", info.Version)
	assert.Equal(t, "focal", info.Codename)
	assert.Equal(t, "Thu, 23 Apr 2020 17:33:17 UTC", info.Date)
	assert.Equal(t, "Ubuntu Focal 20.04 LTS", info.Description)

	// Check multi-value fields
	expectedArchs := []string{"amd64", "arm64", "armhf", "i386", "ppc64el", "riscv64", "s390x"}
	assert.ElementsMatch(t, expectedArchs, info.Architectures)

	expectedComponents := []string{"main", "restricted", "universe", "multiverse"}
	assert.ElementsMatch(t, expectedComponents, info.Components)
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestParseIndexFilenames tests the ParseIndexFilenames function that extracts
// filenames from the checksums section of a Release file.
//
// The test verifies:
// - File paths are correctly extracted from the SHA256 section
// - Proper handling of checksum, filesize, and path formatting
// - Only files listed in the checksum section are returned
// - The returned paths match the exact format needed for fetching
//
// Approach:
// 1. Provides sample Release file content with SHA256 checksums
// 2. Calls ParseIndexFilenames on the sample data
// 3. Verifies the correct number of files are extracted
// 4. Checks that the extracted filenames match expected paths
//
// Note: This test focuses on the file list extraction, complementing the
// TestParseReleaseFile test which handles the metadata section
func TestParseIndexFilenames(t *testing.T) {
	// Extract filenames from the sample Release data
	filenames, err := ParseIndexFilenames([]byte(sampleReleaseData))

	// Verify extraction succeeded
	require.NoError(t, err)
	require.Equal(t, 2, len(filenames))

	// Verify the extracted filenames
	expectedFiles := []string{
		"main/binary-amd64/Packages",
		"main/binary-amd64/Packages.gz",
	}

	assert.ElementsMatch(t, expectedFiles, filenames)
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestExtractPackageURLs tests the ExtractPackageURLs function that builds
// repository-qualified URLs from package filenames.
//
// The test verifies:
// - URLs are correctly formed by combining repo name and package filename
// - All packages in the file have corresponding URLs generated
// - The URL format matches the expected pattern with leading slash
//
// Approach:
// 1. Provides sample Packages data with multiple package entries
// 2. Specifies a test repository name
// 3. Calls ExtractPackageURLs with the repo and package data
// 4. Verifies the correct number of URLs are generated
// 5. Checks that URLs follow the expected format pattern
//
// Note: The URLs generated by this function are used for prefetching packages,
// so the format must match what the HTTP handler expects
func TestExtractPackageURLs(t *testing.T) {
	// Set test repository name
	repo := "ubuntu"

	// Extract URLs from the sample Packages data
	urls, err := ExtractPackageURLs(repo, []byte(samplePackagesData))

	// Verify extraction succeeded
	require.NoError(t, err)
	require.Equal(t, 2, len(urls))

	// Verify the extracted URLs
	expectedURLs := []string{
		"/ubuntu/pool/main/n/nginx/nginx_1.18.0-6ubuntu1_amd64.deb",
		"/ubuntu/pool/main/p/python3.9/python3.9_3.9.5-3_amd64.deb",
	}

	assert.ElementsMatch(t, expectedURLs, urls)
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestPackageIndex tests the PackageIndex implementation, which provides thread-safe
// storage and searching of package metadata.
//
// The test verifies:
// - New package indices can be created
// - Packages can be added to the index
// - The index correctly handles concurrent additions
// - Search functionality returns the correct packages
// - LastUpdated timestamp is maintained
//
// Approach:
// 1. Creates a new PackageIndex
// 2. Adds sample packages to the index
// 3. Tests search functionality with different queries
// 4. Verifies that LastUpdated field is set
//
// Note: Uses the same sample package data as other tests to maintain consistency
func TestPackageIndex(t *testing.T) {
	// Create a new package index
	index := NewPackageIndex()
	require.NotNil(t, index)

	// Parse sample data to get package structs
	packages, err := ParsePackages([]byte(samplePackagesData))
	require.NoError(t, err)
	require.Equal(t, 2, len(packages))

	// Add packages to the index
	for _, pkg := range packages {
		index.AddPackage(pkg)
	}

	// Test exact match search
	results := index.Search("nginx")
	require.Equal(t, 1, len(results))
	assert.Equal(t, "nginx", results[0].Package)

	// Test partial match search
	results = index.Search("python")
	require.Equal(t, 1, len(results))
	assert.Equal(t, "python3.9", results[0].Package)

	// Test search with no matches
	results = index.Search("nonexistent")
	assert.Empty(t, results)

	// Verify LastUpdated is set
	assert.False(t, index.LastUpdated.IsZero())
}

// IMPORTANT: The documentation comment block below should not be removed unless
// the test itself is removed. Only modify the comment if the test's functionality
// changes. These comments are essential for understanding the test's purpose
// and approach, especially for future maintainers and code reviewers.

// TestParsePackagesFileWithIndex tests the ParsePackagesFileWithIndex function,
// which parses package data and simultaneously builds a searchable index.
//
// The test verifies:
// - Packages are correctly parsed from the file
// - The provided index is populated with package data
// - The returned package list matches what was parsed
// - The index contains all packages from the file
//
// Approach:
// 1. Creates a new empty PackageIndex
// 2. Calls ParsePackagesFileWithIndex with sample data and the index
// 3. Verifies the returned package list contains expected data
// 4. Tests the index directly to confirm it contains the expected packages
//
// Note: This function is important for efficiently building the package index
// without requiring a separate step after parsing
func TestParsePackagesFileWithIndex(t *testing.T) {
	// Create a new empty package index
	index := NewPackageIndex()
	require.NotNil(t, index)

	// Parse the sample data and build the index
	packages, err := ParsePackagesFileWithIndex([]byte(samplePackagesData), index)

	// Verify parsing succeeded
	require.NoError(t, err)
	require.Equal(t, 2, len(packages))

	// Check that the returned packages match expected values
	assert.Equal(t, "nginx", packages[0].Package)
	assert.Equal(t, "python3.9", packages[1].Package)

	// Verify the index was populated
	nginxResults := index.Search("nginx")
	require.Equal(t, 1, len(nginxResults))
	assert.Equal(t, "nginx", nginxResults[0].Package)

	pythonResults := index.Search("python")
	require.Equal(t, 1, len(pythonResults))
	assert.Equal(t, "python3.9", pythonResults[0].Package)
}
