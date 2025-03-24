package mapper

import (
	"fmt"
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

// AdvancedMapper provides flexible path mapping with multiple rule types
type AdvancedMapper struct {
	rules []MappingRule
	mutex sync.RWMutex
}

// NewAdvancedMapper creates a new advanced path mapper
func NewAdvancedMapper() *AdvancedMapper {
	m := &AdvancedMapper{
		rules: make([]MappingRule, 0),
	}

	// Initialize with default rules
	m.AddRegexRule(`^(debian|ubuntu)/dists/(.*)$`, "%s", 100)
	m.AddRegexRule(`^security\.debian\.org/(.*)$`, "security.debian.org", 90)
	m.AddRegexRule(`^(archive|security)\.ubuntu\.com/ubuntu/(.*)$`, "archive.ubuntu.com/ubuntu", 90)
	m.AddPrefixRule("debian-security", "security.debian.org", 80)
	m.AddPrefixRule("ubuntu-security", "security.ubuntu.com/ubuntu", 80)

	// Advanced rules with rewriting
	m.AddRewriteRule(`^ppa/(.+?)/(.+?)/ubuntu/(.*)$`, "ppa.launchpad.net/$1/$2/ubuntu", "$3", 70)

	return m
}

// AddRegexRule adds a regex-based mapping rule
func (m *AdvancedMapper) AddRegexRule(pattern, repo string, priority int) error {
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
func (m *AdvancedMapper) AddPrefixRule(prefix, repo string, priority int) {
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
func (m *AdvancedMapper) AddExactRule(exact, repo string, priority int) {
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
func (m *AdvancedMapper) AddRewriteRule(pattern, repo, rewriteRule string, priority int) error {
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

// sortRules sorts rules by priority (highest first)
func (m *AdvancedMapper) sortRules() {
	sort.Slice(m.rules, func(i, j int) bool {
		return m.rules[i].Priority > m.rules[j].Priority
	})
}

// MapPath maps a request path using the configured rules
func (m *AdvancedMapper) MapPath(requestPath string) (*MappingResult, error) {
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
					remotePath = path
				}
			}

		case PrefixRule:
			if strings.HasPrefix(path, rule.Pattern) {
				matched = true
				repo = rule.Repository
				remotePath = strings.TrimPrefix(path, rule.Pattern)
				// Replace this conditional with unconditional TrimPrefix
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
			return &MappingResult{
				Repository: repo,
				RemotePath: remotePath,
				CachePath:  filepath.Join(repo, remotePath),
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

	return &MappingResult{
		Repository: repo,
		RemotePath: remotePath,
		CachePath:  path,
		IsIndex:    isIndex,
	}, nil
}

// MappingResult now includes the matching rule
type MappingResult struct {
	Repository string       // Which repository to use
	RemotePath string       // Path to request from the repository
	CachePath  string       // Path to store in the cache
	IsIndex    bool         // Whether this is a repository index file
	Rule       *MappingRule // The rule that matched
}
