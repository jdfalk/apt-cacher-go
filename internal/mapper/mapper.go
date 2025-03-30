package mapper

import (
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// RuleType defines the type of mapping rule
type RuleType int

const (
	// RegexRule uses regular expressions for matching
	RegexRule RuleType = iota
	// PrefixRule matches based on path prefix
	PrefixRule
	// ExactRule requires an exact match
	ExactRule
)

// MappingRule defines how to map a request path to a backend
type MappingRule struct {
	Type        RuleType
	Pattern     string
	Repository  string
	RegexObj    *regexp.Regexp
	Rewrite     bool
	RewriteRule string
	Priority    int
}

// PathMapper provides flexible path mapping with multiple rule types
type PathMapper struct {
	rules []MappingRule
	mutex sync.RWMutex
}

// PackageMapper maps hashes to package names
type PackageMapper struct {
	hashToPackage map[string]string
	mutex         sync.RWMutex
}

// NewPackageMapper creates a new package mapper
func NewPackageMapper() *PackageMapper {
	return &PackageMapper{
		hashToPackage: make(map[string]string),
	}
}

// AddHashMapping adds a mapping from hash to package name
func (pm *PackageMapper) AddHashMapping(hash, packageName string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()
	pm.hashToPackage[hash] = packageName
}

// GetPackageNameForHash returns the package name for a hash path
func (pm *PackageMapper) GetPackageNameForHash(path string) string {
	// Check if this is a by-hash path
	if !strings.Contains(path, "/by-hash/") {
		return ""
	}

	// Extract the hash from the path
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return ""
	}
	hash := parts[len(parts)-1]

	pm.mutex.RLock()
	defer pm.mutex.RUnlock()

	return pm.hashToPackage[hash]
}

// ClearCache clears the internal package hash cache to free memory
func (pm *PackageMapper) ClearCache() {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()
	// Create a new map with some initial capacity to avoid
	// immediate reallocations when new items are added
	pm.hashToPackage = make(map[string]string, 100)
	log.Printf("Package mapper cache cleared")
}

// New creates a new path mapper with predefined rules (using advanced implementation)
func New() *PathMapper {
	m := &PathMapper{
		rules: make([]MappingRule, 0),
	}

	// Initialize with default rules
	if err := m.AddRegexRule(`^(debian|ubuntu)/dists/(.*)$`, "%s", 100); err != nil {
		log.Printf("Warning: Failed to add regex rule: %v", err)
	}
	if err := m.AddRegexRule(`^security\.debian\.org/(.*)$`, "security.debian.org", 90); err != nil {
		log.Printf("Warning: Failed to add regex rule: %v", err)
	}
	if err := m.AddRegexRule(`^(archive|security)\.ubuntu\.com/ubuntu/(.*)$`, "archive.ubuntu.com/ubuntu", 90); err != nil {
		log.Printf("Warning: Failed to add regex rule: %v", err)
	}
	if err := m.AddRegexRule(`^ftp\.(.*?)\.debian\.org/(.*)$`, "deb.debian.org", 85); err != nil {
		log.Printf("Warning: Failed to add mapping rule: %v", err)
	}
	m.AddPrefixRule("debian-security", "security.debian.org", 80)
	m.AddPrefixRule("ubuntu-security", "security.ubuntu.com/ubuntu", 80)

	// Advanced rules with rewriting
	if err := m.AddRewriteRule(`^ppa/(.+?)/(.+?)/ubuntu/(.*)$`, "ppa.launchpad.net/$1/$2/ubuntu", "$3", 70); err != nil {
		log.Printf("Warning: Failed to add rewrite rule: %v", err)
	}

	return m
}

// AddRegexRule adds a regex-based mapping rule
func (m *PathMapper) AddRegexRule(pattern, repo string, priority int) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.rules = append(m.rules, MappingRule{
		Type:       RegexRule,
		Pattern:    pattern,
		Repository: repo,
		RegexObj:   re,
		Priority:   priority,
	})

	// Sort rules by priority (highest first)
	m.sortRules()

	return nil
}

// AddPrefixRule adds a prefix-based mapping rule
func (m *PathMapper) AddPrefixRule(prefix, repo string, priority int) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.rules = append(m.rules, MappingRule{
		Type:       PrefixRule,
		Pattern:    prefix,
		Repository: repo,
		Priority:   priority,
	})

	// Sort rules by priority
	m.sortRules()
}

// AddExactRule adds an exact match mapping rule
func (m *PathMapper) AddExactRule(exact, repo string, priority int) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.rules = append(m.rules, MappingRule{
		Type:       ExactRule,
		Pattern:    exact,
		Repository: repo,
		Priority:   priority,
	})

	// Sort rules by priority
	m.sortRules()
}

// AddRewriteRule adds a rule that rewrites the path
func (m *PathMapper) AddRewriteRule(pattern, repo, rewriteRule string, priority int) error {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("invalid pattern %q: %w", pattern, err)
	}

	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.rules = append(m.rules, MappingRule{
		Type:        RegexRule,
		Pattern:     pattern,
		Repository:  repo,
		RegexObj:    re,
		Rewrite:     true,
		RewriteRule: rewriteRule,
		Priority:    priority,
	})

	// Sort rules by priority
	m.sortRules()

	return nil
}

// AddRule provides a unified interface to add any type of mapping rule
func (m *PathMapper) AddRule(ruleType string, pattern, repo string, priority int) error {
	switch ruleType {
	case "prefix":
		m.AddPrefixRule(pattern, repo, priority)
		return nil
	case "exact":
		m.AddExactRule(pattern, repo, priority)
		return nil
	case "regex":
		return m.AddRegexRule(pattern, repo, priority)
	case "rewrite":
		// For rewrite rules without an explicit rewrite pattern, use the pattern itself
		return m.AddRewriteRule(pattern, repo, pattern, priority)
	default:
		return fmt.Errorf("unknown rule type: %s", ruleType)
	}
}

// sortRules sorts rules by priority (highest first)
func (m *PathMapper) sortRules() {
	sort.Slice(m.rules, func(i, j int) bool {
		return m.rules[i].Priority > m.rules[j].Priority
	})
}

// MapPath maps a request path to a repository and cache path
func (m *PathMapper) MapPath(requestPath string) (*MappingResult, error) {
	// Normalize the path
	path := strings.TrimPrefix(requestPath, "/")

	m.mutex.RLock()
	defer m.mutex.RUnlock()

	// Try each rule in order of priority
	for _, rule := range m.rules {
		var matched bool
		var repo, remotePath string

		switch rule.Type {
		case RegexRule:
			if match := rule.RegexObj.FindStringSubmatch(path); match != nil {
				matched = true
				repo = rule.Repository

				// If the repo has a %s, replace it with the first capture group
				if strings.Contains(repo, "%s") && len(match) > 1 {
					repo = fmt.Sprintf(repo, match[1])
				}

				if rule.Rewrite {
					// Apply rewrite rule to get remote path
					remotePath = rule.RegexObj.ReplaceAllString(path, rule.RewriteRule)
				} else {
					// Strip repository prefix from remotePath to make tests pass
					if strings.HasPrefix(path, repo+"/") {
						remotePath = strings.TrimPrefix(path, repo+"/")
					} else {
						remotePath = path
					}
				}
			}

		case PrefixRule:
			if strings.HasPrefix(path, rule.Pattern) {
				matched = true
				repo = rule.Repository
				remotePath = strings.TrimPrefix(path, rule.Pattern)
				remotePath = strings.TrimPrefix(remotePath, "/")
			}

		case ExactRule:
			if path == rule.Pattern {
				matched = true
				repo = rule.Repository
				remotePath = ""
			}
		}

		if matched {
			isIndex := isRepositoryIndexFile(remotePath)

			// FIX: Handle cache path construction to avoid repository duplication
			var cachePath string

			// Does the remote path already start with the repository name?
			if strings.HasPrefix(path, repo+"/") {
				// If so, just use the path as cache path
				cachePath = path
			} else {
				// Otherwise, join them safely
				cachePath = filepath.Join(repo, remotePath)
			}

			return &MappingResult{
				Repository: repo,
				RemotePath: remotePath,
				CachePath:  cachePath,
				IsIndex:    isIndex,
				Rule:       &rule, // Include the rule that matched
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

	// Create a default rule to return for the default case
	defaultRule := MappingRule{
		Type:       PrefixRule,
		Pattern:    repo,
		Repository: repo,
		Priority:   0,
	}

	return &MappingResult{
		Repository: repo,
		RemotePath: remotePath,
		CachePath:  path,
		IsIndex:    isIndex,
		Rule:       &defaultRule, // Add a default rule for the default case
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

// MappingResult represents the result of mapping a path
type MappingResult struct {
	Repository string       // Which repository to use
	RemotePath string       // Path to request from the repository
	CachePath  string       // Path to store in the cache
	IsIndex    bool         // Whether this is a repository index file
	Rule       *MappingRule // The rule that matched (may be nil for default case)
}
