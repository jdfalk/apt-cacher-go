package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds the application configuration
type Config struct {
	// Server configuration
	ListenAddress string `yaml:"listen_address"`
	Port          int    `yaml:"port"`

	// TLS configuration
	TLSEnabled  bool   `yaml:"tls_enabled"`
	TLSPort     int    `yaml:"tls_port"`
	TLSCertFile string `yaml:"tls_cert_file"`
	TLSKeyFile  string `yaml:"tls_key_file"`

	// Cache configuration
	CacheDir        string `yaml:"cache_dir"`
	MaxCacheSize    int64  `yaml:"max_cache_size"`   // In MB
	CleanupInterval int    `yaml:"cleanup_interval"` // In hours

	// Backend configuration
	Backends []Backend `yaml:"backends"`

	// Mapping rules for advanced path mapping
	MappingRules []MappingRule `yaml:"mapping_rules"`

	// Security
	AllowedIPs []string `yaml:"allowed_ips"`
	AdminAuth  string   `yaml:"admin_auth"` // username:password

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

// Load reads configuration from the specified file path
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	config := &Config{
		// Default values
		ListenAddress:          "0.0.0.0",
		Port:                   3142, // Default apt-cacher-ng port
		TLSEnabled:             false,
		TLSPort:                3143, // Default TLS port
		CacheDir:               "/var/cache/apt-cacher-go",
		MaxCacheSize:           40960, // 40GB
		CleanupInterval:        1,     // Run cleanup every hour
		MaxConcurrentDownloads: 10,    // Default concurrent downloads
		PrefetchEnabled:        true,
		MaxPrefetches:          5,
	}

	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, err
	}

	return config, nil
}
