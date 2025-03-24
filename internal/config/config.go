package config

import (
	"os" // Replace io/ioutil with os
	"time"

	"gopkg.in/yaml.v2"
)

// Config holds the application configuration
type Config struct {
	// Server configuration
	ListenAddress string `yaml:"listen_address"`
	Port          int    `yaml:"port"`
	AdminPort     int    `yaml:"admin_port"`
	TLSEnabled    bool   `yaml:"tls_enabled"`
	TLSCert       string `yaml:"tls_cert"`
	TLSKey        string `yaml:"tls_key"`
	TLSPort       int    `yaml:"tls_port"`
	LogFile       string `yaml:"log_file"`
	LogLevel      string `yaml:"log_level"`

	// Cache configuration
	CacheDir    string            `yaml:"cache_dir"`
	CacheSize   string            `yaml:"cache_size"`
	CacheTTLs   map[string]string `yaml:"cache_ttls"`
	CleanupFreq string            `yaml:"cleanup_freq"`

	// Backend configuration
	Backends []Backend `yaml:"backends"`

	// Mapping rules
	MappingRules []MappingRule `yaml:"mapping_rules"`

	// Security settings
	AllowedIPs    []string `yaml:"allowed_ips"`
	RateLimit     int      `yaml:"rate_limit"`
	AuthEnabled   bool     `yaml:"auth_enabled"`
	AdminUser     string   `yaml:"username"`
	AdminPassword string   `yaml:"password"`
	AdminAuth     bool     `yaml:"admin_auth"`

	// Metrics configuration
	MetricsEnabled bool `yaml:"metrics_enabled"`

	// Download queue
	MaxConcurrentDownloads int `yaml:"max_concurrent_downloads"`

	// Prefetching
	PrefetchEnabled bool `yaml:"prefetch_enabled"`
	MaxPrefetches   int  `yaml:"max_prefetches"`
}

// Backend represents a repository backend
type Backend struct {
	Name     string `yaml:"name"`
	URL      string `yaml:"url"`
	Priority int    `yaml:"priority"`
}

// MappingRule defines a rule for mapping request paths
type MappingRule struct {
	Type        string `yaml:"type"` // regex, prefix, exact, rewrite
	Pattern     string `yaml:"pattern"`
	Repository  string `yaml:"repository"`
	Priority    int    `yaml:"priority"`
	RewriteRule string `yaml:"rewrite_rule,omitempty"`
}

// LoadConfigFile loads configuration from a YAML file
func LoadConfigFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	config := &Config{}
	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, err
	}

	// Apply defaults
	if config.ListenAddress == "" {
		config.ListenAddress = "0.0.0.0"
	}
	if config.Port == 0 {
		config.Port = 3142
	}
	if config.CacheDir == "" {
		config.CacheDir = "/var/cache/apt-cacher-go"
	}
	if config.CacheSize == "" {
		config.CacheSize = "10G"
	}
	if config.CleanupFreq == "" {
		config.CleanupFreq = "1h"
	}

	// Default cache TTLs
	if config.CacheTTLs == nil {
		config.CacheTTLs = map[string]string{
			"index":   "1h",
			"package": "30d",
		}
	}

	return config, nil
}

// ParseCacheSize parses the cache size string (like "10G") into bytes
func (c *Config) ParseCacheSize() (int64, error) {
	// Implementation
	return 10 * 1024 * 1024 * 1024, nil // 10GB default
}

// ParseCleanupFrequency parses the cleanup frequency string
func (c *Config) ParseCleanupFrequency() (time.Duration, error) {
	return time.ParseDuration(c.CleanupFreq)
}

// GetCacheTTL returns the cache TTL for the specified type
func (c *Config) GetCacheTTL(fileType string) (time.Duration, error) {
	ttl, exists := c.CacheTTLs[fileType]
	if !exists {
		// Return default TTL based on file type
		switch fileType {
		case "index":
			ttl = "1h"
		case "package":
			ttl = "30d"
		default:
			ttl = "1h"
		}
	}
	return time.ParseDuration(ttl)
}
