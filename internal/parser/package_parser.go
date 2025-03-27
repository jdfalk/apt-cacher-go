package parser

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// PackageInfo represents a single package's metadata
type PackageInfo struct {
	Package      string
	Version      string
	Architecture string
	Filename     string
	Size         string
	MD5sum       string
	SHA1         string
	SHA256       string
	Description  string
}

// IndexInfo represents a repository index file's metadata
type IndexInfo struct {
	Origin        string
	Label         string
	Suite         string
	Version       string
	Codename      string
	Date          string
	ValidUntil    string
	Architectures []string
	Components    []string
	Description   string
}

// PackageIndex maps package names to their repository locations
type PackageIndex struct {
	Packages    map[string][]PackageInfo // Map package names to their details
	LastUpdated time.Time
	mutex       sync.RWMutex
}

// NewPackageIndex creates a new package index
func NewPackageIndex() *PackageIndex {
	return &PackageIndex{
		Packages:    make(map[string][]PackageInfo),
		LastUpdated: time.Now(),
	}
}

// AddPackage adds a package to the index
func (idx *PackageIndex) AddPackage(pkg PackageInfo) {
	idx.mutex.Lock()
	defer idx.mutex.Unlock()

	idx.Packages[pkg.Package] = append(idx.Packages[pkg.Package], pkg)
	idx.LastUpdated = time.Now()
}

// Search searches the index for packages matching a query
func (idx *PackageIndex) Search(query string) []PackageInfo {
	idx.mutex.RLock()
	defer idx.mutex.RUnlock()

	var results []PackageInfo
	query = strings.ToLower(query)

	for name, pkgs := range idx.Packages {
		if strings.Contains(strings.ToLower(name), query) {
			results = append(results, pkgs...)
		}
	}

	return results
}

// ParsePackagesFile parses a Packages file (plain or gzipped)
func ParsePackagesFile(data []byte) ([]PackageInfo, error) {
	var reader io.Reader = bytes.NewReader(data)

	// Check if gzipped
	if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
		gzReader, err := gzip.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress gzipped data: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	packages := make([]PackageInfo, 0)
	scanner := bufio.NewScanner(reader)

	var pkg *PackageInfo

	for scanner.Scan() {
		line := scanner.Text()

		// Empty line marks end of a package entry
		if line == "" {
			if pkg != nil && pkg.Package != "" {
				packages = append(packages, *pkg)
				pkg = nil
			}
			continue
		}

		// Start of a new package
		if pkg == nil {
			pkg = &PackageInfo{}
		}

		// Parse key-value pairs
		if strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}

			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			switch key {
			case "Package":
				pkg.Package = value
			case "Version":
				pkg.Version = value
			case "Architecture":
				pkg.Architecture = value
			case "Filename":
				pkg.Filename = value
			case "Size":
				pkg.Size = value
			case "MD5sum":
				pkg.MD5sum = value
			case "SHA1":
				pkg.SHA1 = value
			case "SHA256":
				pkg.SHA256 = value
			case "Description":
				pkg.Description = value
			}
		}
	}

	// Add the last package if there is one
	if pkg != nil && pkg.Package != "" {
		packages = append(packages, *pkg)
	}

	return packages, nil
}

// ParsePackagesFileWithIndex parses a Packages file and builds an index
func ParsePackagesFileWithIndex(data []byte, index *PackageIndex) ([]PackageInfo, error) {
	packages, err := ParsePackagesFile(data)
	if err != nil {
		return nil, err
	}

	// Add packages to index
	for _, pkg := range packages {
		index.AddPackage(pkg)
	}

	return packages, nil
}

// ParseReleaseFile parses a Release file
func ParseReleaseFile(data []byte) (*IndexInfo, error) {
	reader := bytes.NewReader(data)
	scanner := bufio.NewScanner(reader)

	info := &IndexInfo{}

	for scanner.Scan() {
		line := scanner.Text()

		// Parse key-value pairs
		if strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				continue
			}

			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			switch key {
			case "Origin":
				info.Origin = value
			case "Label":
				info.Label = value
			case "Suite":
				info.Suite = value
			case "Version":
				info.Version = value
			case "Codename":
				info.Codename = value
			case "Date":
				info.Date = value
			case "Valid-Until":
				info.ValidUntil = value
			case "Architectures":
				info.Architectures = strings.Fields(value)
			case "Components":
				info.Components = strings.Fields(value)
			case "Description":
				info.Description = value
			}
		}
	}

	return info, nil
}

// ExtractPackageURLs extracts package URLs from a Packages file
func ExtractPackageURLs(repo string, packagesData []byte) ([]string, error) {
	packages, err := ParsePackagesFile(packagesData)
	if err != nil {
		return nil, err
	}

	urls := make([]string, 0, len(packages))

	for _, pkg := range packages {
		if pkg.Filename != "" {
			urls = append(urls, fmt.Sprintf("/%s/%s", repo, pkg.Filename))
		}
	}

	return urls, nil
}

// ParseIndexFilenames extracts filenames from Release file contents
func ParseIndexFilenames(releaseData []byte) ([]string, error) {
	// Skip metadata and find the file list section
	startMarker := "SHA256:"

	reader := bytes.NewReader(releaseData)
	scanner := bufio.NewScanner(reader)

	inFileList := false
	filenames := make([]string, 0)

	for scanner.Scan() {
		line := scanner.Text()

		if !inFileList && strings.HasPrefix(line, startMarker) {
			inFileList = true
			continue
		}

		if inFileList {
			if line == "" || !strings.HasPrefix(line, " ") {
				// End of file list
				break
			}

			// Parse hash line: checksum size path
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				filenames = append(filenames, fields[2])
			}
		}
	}

	return filenames, nil
}

// ParsePackages parses a Debian Packages file and returns a slice of PackageInfo structs
func ParsePackages(data []byte) ([]PackageInfo, error) {
	var packages []PackageInfo
	var currentPackage *PackageInfo

	scanner := bufio.NewScanner(bytes.NewReader(data))

	for scanner.Scan() {
		line := scanner.Text()

		// Empty line marks the end of a package entry
		if strings.TrimSpace(line) == "" {
			if currentPackage != nil && currentPackage.Package != "" {
				packages = append(packages, *currentPackage)
				currentPackage = nil
			}
			continue
		}

		// Start a new package if we don't have one
		if currentPackage == nil {
			currentPackage = &PackageInfo{}
		}

		// Parse field: value pairs
		if strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			field := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			switch field {
			case "Package":
				currentPackage.Package = value
			case "Version":
				currentPackage.Version = value
			case "Architecture":
				currentPackage.Architecture = value
			case "Filename":
				currentPackage.Filename = value
			case "Size":
				currentPackage.Size = value
			case "MD5sum":
				currentPackage.MD5sum = value
			case "SHA1":
				currentPackage.SHA1 = value
			case "SHA256":
				currentPackage.SHA256 = value
			case "Description":
				currentPackage.Description = value
			}
		}
	}

	// Add the last package if there was no empty line at the end
	if currentPackage != nil && currentPackage.Package != "" {
		packages = append(packages, *currentPackage)
	}

	return packages, scanner.Err()
}
