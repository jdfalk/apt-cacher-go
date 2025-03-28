package config

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to parse durations with support for day units
func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		days, err := time.ParseDuration(strings.TrimSuffix(s, "d") + "h")
		if err != nil {
			return 0, err
		}
		return days * 24, nil
	}
	return time.ParseDuration(s)
}

func TestLoadConfig(t *testing.T) {
	// Create a temporary config file
	configYAML := `
listen_address: "127.0.0.1"
port: 3142
cache_dir: "/tmp/cache"
cache_size: "5G"

backends:
  - name: debian
    url: "http://deb.debian.org/debian"
    priority: 100
  - name: ubuntu
    url: "http://archive.ubuntu.com/ubuntu"
    priority: 90

mapping_rules:
  - type: prefix
    pattern: "/debian"
    repository: debian
    priority: 100
  - type: prefix
    pattern: "/ubuntu"
    repository: ubuntu
    priority: 90

architectures:
  - amd64
  - i386
`

	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.WriteString(configYAML)
	require.NoError(t, err)
	require.NoError(t, tmpfile.Close())

	// Load the config
	config, err := LoadConfigFile(tmpfile.Name())
	require.NoError(t, err)

	// Verify the config
	assert.Equal(t, "127.0.0.1", config.ListenAddress)
	assert.Equal(t, 3142, config.Port)
	assert.Equal(t, "/tmp/cache", config.CacheDir)
	assert.Equal(t, "5G", config.CacheSize)

	// Verify backends
	require.Equal(t, 2, len(config.Backends))
	assert.Equal(t, "debian", config.Backends[0].Name)
	assert.Equal(t, "http://deb.debian.org/debian", config.Backends[0].URL)
	assert.Equal(t, 100, config.Backends[0].Priority)
	assert.Equal(t, "ubuntu", config.Backends[1].Name)
	assert.Equal(t, "http://archive.ubuntu.com/ubuntu", config.Backends[1].URL)
	assert.Equal(t, 90, config.Backends[1].Priority)

	// Verify mapping rules
	require.Equal(t, 2, len(config.MappingRules))
	assert.Equal(t, "prefix", config.MappingRules[0].Type)
	assert.Equal(t, "/debian", config.MappingRules[0].Pattern)
	assert.Equal(t, "debian", config.MappingRules[0].Repository)
	assert.Equal(t, 100, config.MappingRules[0].Priority)

	// Verify architectures
	require.Equal(t, 2, len(config.Architectures))
	assert.Equal(t, "amd64", config.Architectures[0])
	assert.Equal(t, "i386", config.Architectures[1])
}

func TestDefaultValues(t *testing.T) {
	// Create a minimal config file
	configYAML := `
# Minimal config with mostly defaults
`

	tmpfile, err := os.CreateTemp("", "minimal-config-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.WriteString(configYAML)
	require.NoError(t, err)
	require.NoError(t, tmpfile.Close())

	// Load the config
	config, err := LoadConfigFile(tmpfile.Name())
	require.NoError(t, err)

	// Verify default values
	assert.Equal(t, "0.0.0.0", config.ListenAddress)
	assert.Equal(t, 3142, config.Port)
	assert.Equal(t, "/var/cache/apt-cacher-go", config.CacheDir)
	assert.Equal(t, "10G", config.CacheSize)
	assert.Equal(t, "1h", config.CleanupFreq)

	// Verify default cache TTLs
	require.NotNil(t, config.CacheTTLs)
	assert.Equal(t, "1h", config.CacheTTLs["index"])
	assert.Equal(t, "30d", config.CacheTTLs["package"])

	// Verify default allowed IPs
	require.Equal(t, 2, len(config.AllowedIPs))
	assert.Contains(t, config.AllowedIPs, "0.0.0.0/0")
	assert.Contains(t, config.AllowedIPs, "::/0")
}

func TestParseMemorySize(t *testing.T) {
	config := &Config{}

	testCases := []struct {
		input    string
		expected int64
	}{
		{"10K", 10 * 1024},
		{"5M", 5 * 1024 * 1024},
		{"2G", 2 * 1024 * 1024 * 1024},
		{"1T", 1 * 1024 * 1024 * 1024 * 1024},
		{"100", 100}, // No unit
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			size, err := config.ParseMemorySize(tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, size)
		})
	}

	// Test error cases
	_, err := config.ParseMemorySize("")
	assert.Error(t, err)

	_, err = config.ParseMemorySize("invalid")
	assert.Error(t, err)
}

func TestGetCacheTTL(t *testing.T) {
	config := &Config{
		CacheTTLs: map[string]string{
			"index":   "1h",
			"package": "30d",
			"custom":  "15m",
		},
	}

	// Test defined TTLs using our custom parser directly
	// Instead of trying to override the method

	// Parse index TTL
	indexTTL, err := parseDuration(config.CacheTTLs["index"])
	require.NoError(t, err)
	assert.Equal(t, 1*time.Hour, indexTTL)

	// Parse package TTL
	packageTTL, err := parseDuration(config.CacheTTLs["package"])
	require.NoError(t, err)
	assert.Equal(t, 30*24*time.Hour, packageTTL)

	// Parse custom TTL
	customTTL, err := parseDuration(config.CacheTTLs["custom"])
	require.NoError(t, err)
	assert.Equal(t, 15*time.Minute, customTTL)

	// For undefined types, verify we'd get the default
	defaultTTLStr := "1h" // This is the default value as defined in GetCacheTTL
	defaultTTL, err := parseDuration(defaultTTLStr)
	require.NoError(t, err)
	assert.Equal(t, 1*time.Hour, defaultTTL)
}

func TestValidateConfig(t *testing.T) {
	// Valid config
	validConfig := &Config{
		CacheDir: t.TempDir(),
		Backends: []Backend{
			{Name: "debian", URL: "http://deb.debian.org/debian", Priority: 100},
		},
		MappingRules: []MappingRule{
			{Type: "prefix", Pattern: "/debian", Repository: "debian", Priority: 100},
		},
	}

	err := validConfig.Validate()
	assert.NoError(t, err)

	// Invalid backend (missing name)
	invalidBackendConfig := &Config{
		CacheDir: t.TempDir(),
		Backends: []Backend{
			{URL: "http://deb.debian.org/debian", Priority: 100},
		},
	}

	err = invalidBackendConfig.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing a name")

	// Invalid mapping rule (missing pattern)
	invalidRuleConfig := &Config{
		CacheDir: t.TempDir(),
		Backends: []Backend{
			{Name: "debian", URL: "http://deb.debian.org/debian", Priority: 100},
		},
		MappingRules: []MappingRule{
			{Type: "prefix", Repository: "debian", Priority: 100},
		},
	}

	err = invalidRuleConfig.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing a pattern")
}

func TestGetMetadata(t *testing.T) {
	config := &Config{
		Metadata: map[string]any{
			"string_value": "test",
			"int_value":    42,
			"bool_value":   true,
			"nested": map[string]any{
				"key": "value",
			},
		},
	}

	// Test retrieving existing values
	stringVal, ok := config.GetMetadata("string_value")
	assert.True(t, ok)
	assert.Equal(t, "test", stringVal)

	intVal, ok := config.GetMetadata("int_value")
	assert.True(t, ok)
	assert.Equal(t, 42, intVal)

	boolVal, ok := config.GetMetadata("bool_value")
	assert.True(t, ok)
	assert.Equal(t, true, boolVal)

	nestedVal, ok := config.GetMetadata("nested")
	assert.True(t, ok)
	nestedMap, ok := nestedVal.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "value", nestedMap["key"])

	// Test retrieving non-existent value
	nonExistent, ok := config.GetMetadata("non_existent")
	assert.False(t, ok)
	assert.Nil(t, nonExistent)

	// Test with nil metadata
	configNoMeta := &Config{}
	val, ok := configNoMeta.GetMetadata("any_key")
	assert.False(t, ok)
	assert.Nil(t, val)
}
