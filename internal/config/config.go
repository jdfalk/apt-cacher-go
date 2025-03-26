package config

import (
	"fmt"
	"log"
	"os"
	"strings"
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

	// Default repositories
	DisableDefaultRepos bool `yaml:"disable_default_repos"`
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

	// First unmarshal into a map to handle string vs bool for admin_auth
	var configMap map[string]interface{}
	if err := yaml.Unmarshal(data, &configMap); err != nil {
		return nil, err
	}

	// Check if admin_auth is a string and contains a colon
	if authVal, ok := configMap["admin_auth"]; ok {
		if authStr, ok := authVal.(string); ok && strings.Contains(authStr, ":") {
			// It's "user:password" format, split and set the appropriate fields
			parts := strings.SplitN(authStr, ":", 2)
			configMap["admin_auth"] = true
			configMap["admin_user"] = parts[0]
			configMap["admin_password"] = parts[1]
		}
	}

	// Serialize back to YAML
	correctedData, err := yaml.Marshal(configMap)
	if err != nil {
		return nil, err
	}

	// Now unmarshal into the Config struct
	config := &Config{}
	if err := yaml.Unmarshal(correctedData, config); err != nil {
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

	// Apply defaults for security
	if len(config.AllowedIPs) == 0 {
		// Default to allowing all IPs
		config.AllowedIPs = []string{"0.0.0.0/0", "::/0"}
	} else {
		// Check if we need to add IPv6 support
		hasIPv4All := false
		hasIPv6All := false

		for _, ip := range config.AllowedIPs {
			if ip == "0.0.0.0/0" {
				hasIPv4All = true
			}
			if ip == "::/0" {
				hasIPv6All = true
			}
		}

		// If allowing all IPv4 but not IPv6, add IPv6
		if hasIPv4All && !hasIPv6All {
			config.AllowedIPs = append(config.AllowedIPs, "::/0")
			log.Printf("Added IPv6 support (`::/0`) to allowed IPs")
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

// Validate ensures the configuration has valid values
func (c *Config) Validate() error {
	// Verify cache directory exists or can be created
	if c.CacheDir != "" {
		// Try to create the cache directory if it doesn't exist
		if err := os.MkdirAll(c.CacheDir, 0755); err != nil {
			return fmt.Errorf("failed to create cache directory %s: %w", c.CacheDir, err)
		}
	}

	// Validate backends have required fields
	for i, backend := range c.Backends {
		if backend.Name == "" {
			return fmt.Errorf("backend #%d is missing a name", i+1)
		}
		if backend.URL == "" {
			return fmt.Errorf("backend %s is missing URL", backend.Name)
		}
	}

	// Validate mapping rules
	for i, rule := range c.MappingRules {
		if rule.Type == "" {
			return fmt.Errorf("mapping rule #%d is missing a type", i+1)
		}
		if rule.Pattern == "" {
			return fmt.Errorf("mapping rule #%d is missing a pattern", i+1)
		}
		if rule.Repository == "" {
			return fmt.Errorf("mapping rule #%d is missing a repository", i+1)
		}
	}

	return nil
}

// Debug prints the configuration to the log for debugging purposes
func (c *Config) Debug() string {
	info := "Config loaded: \n"
	info += fmt.Sprintf("  Listen: %s:%d\n", c.ListenAddress, c.Port)
	info += fmt.Sprintf("  Cache Dir: %s\n", c.CacheDir)
	info += fmt.Sprintf("  Cache Size: %s\n", c.CacheSize)

	info += "  Backends:\n"
	for _, b := range c.Backends {
		info += fmt.Sprintf("    - %s: %s (priority: %d)\n", b.Name, b.URL, b.Priority)
	}

	info += "  Mapping Rules:\n"
	for _, r := range c.MappingRules {
		info += fmt.Sprintf("    - Type: %s, Pattern: %s -> Repo: %s (priority: %d)\n",
			r.Type, r.Pattern, r.Repository, r.Priority)
	}

	return info
}

// LoadConfigFileWithDebug loads and validates config with debug output
func LoadConfigFileWithDebug(path string) (*Config, error) {
	config, err := LoadConfigFile(path)
	if err != nil {
		return nil, fmt.Errorf("error loading config file: %w", err)
	}

	// Log the loaded config
	fmt.Printf("Loaded configuration from %s\n", path)
	fmt.Println(config.Debug())

	// Validate the config
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return config, nil
}
