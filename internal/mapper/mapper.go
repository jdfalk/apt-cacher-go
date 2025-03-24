package mapper

import (
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"strings"
)

// PathRule defines a pattern and its associated repository
type PathRule struct {
	Pattern    *regexp.Regexp
	Repository string
}

// PathMapper handles mapping request paths to backend repositories
type PathMapper struct {
	rules []PathRule
}

// New creates a new path mapper with predefined rules
func New() *PathMapper {
	m := &PathMapper{}

	// Initialize with default rules
	if err := m.AddRule(`^(debian|ubuntu)/dists/(.*)$`, "%s"); err != nil {
		log.Printf("Warning: Failed to add mapping rule: %v", err)
	}
	if err := m.AddRule(`^security\.debian\.org/(.*)$`, "security.debian.org"); err != nil {
		log.Printf("Warning: Failed to add mapping rule: %v", err)
	}
	if err := m.AddRule(`^(archive|security)\.ubuntu\.com/ubuntu/(.*)$`, "archive.ubuntu.com/ubuntu"); err != nil {
		log.Printf("Warning: Failed to add mapping rule: %v", err)
	}
	if err := m.AddRule(`^ftp\.(.*?)\.debian\.org/(.*)$`, "deb.debian.org"); err != nil {
		log.Printf("Warning: Failed to add mapping rule: %v", err)
	}

	return m
}

// AddRule adds a new mapping rule
func (m *PathMapper) AddRule(pattern string, repo string) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}

	m.rules = append(m.rules, PathRule{
		Pattern:    re,
		Repository: repo,
	})

	return nil
}

// MapPath determines which backend to use for a given request path
func (m *PathMapper) MapPath(requestPath string) (*MappingResult, error) {
	// Normalize the path
	path := strings.TrimPrefix(requestPath, "/")

	// Check against rules
	for _, rule := range m.rules {
		if match := rule.Pattern.FindStringSubmatch(path); match != nil {
			repo := rule.Repository

			// If the repo has a %s, replace it with the first capture group
			if strings.Contains(repo, "%s") && len(match) > 1 {
				repo = fmt.Sprintf(repo, match[1])
			}

			// Determine if this is an index file
			isIndex := isRepositoryIndexFile(path)

			return &MappingResult{
				Repository: repo,
				RemotePath: path,
				CachePath:  filepath.Join(repo, path),
				IsIndex:    isIndex,
			}, nil
		}
	}

	// Default case - direct mapping
	// Split the path to get the first component as the repo
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid path format: %s", path)
	}

	repo := parts[0]
	remotePath := parts[1]
	isIndex := isRepositoryIndexFile(remotePath)

	return &MappingResult{
		Repository: repo,
		RemotePath: remotePath,
		CachePath:  path,
		IsIndex:    isIndex,
	}, nil
}

// isRepositoryIndexFile checks if the file is a repository index
func isRepositoryIndexFile(path string) bool {
	indexPatterns := []string{
		"Release$", "Release.gpg$", "InRelease$",
		"Packages(.gz|.bz2|.xz)?$",
		"Sources(.gz|.bz2|.xz)?$",
		"Contents-.*(.gz|.bz2|.xz)?$",
	}

	for _, pattern := range indexPatterns {
		if match, _ := regexp.MatchString(pattern, path); match {
			return true
		}
	}

	return false
}
