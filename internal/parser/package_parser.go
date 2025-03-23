package parser

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
)

// PackageInfo represents a single package's metadata
type PackageInfo struct {
	Package      string
	Version      string
	Architecture string
	Filename     string
	Size         string
	MD5Sum       string
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
				pkg.MD5Sum = value
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
