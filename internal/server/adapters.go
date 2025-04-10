package server

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/jdfalk/apt-cacher-go/internal/backend"
	"github.com/jdfalk/apt-cacher-go/internal/cache"
	"github.com/jdfalk/apt-cacher-go/internal/keymanager"
	"github.com/jdfalk/apt-cacher-go/internal/mapper"
	"github.com/jdfalk/apt-cacher-go/internal/metrics"
)

// CacheAdapter adapts *cache.Cache to Cache interface
type CacheAdapter struct {
	*cache.Cache
}

// GetStats adapts the GetStats method to match the interface
func (c *CacheAdapter) GetStats() cache.CacheStats {
	// Fix: GetStats() returns only one value, not two
	stats := c.Cache.GetStats()
	return stats
}

// Search adapts the Search method if available
func (c *CacheAdapter) Search(query string) ([]string, error) {
	// Instead of attempting type assertion on c.Cache, implement a simple search
	// that calls other methods on the concrete Cache type

	// Implement a basic file system search using the cache directory
	dir := c.Cache.RootDir()
	var results []string

	// Use filepath.Walk to scan for matching files
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		if !info.IsDir() && strings.Contains(path, query) {
			// Convert absolute path to relative path from cache root
			relPath, err := filepath.Rel(dir, path)
			if err == nil {
				results = append(results, relPath)
			}
		}
		return nil
	})

	return results, err
}

// MapperAdapter adapts *mapper.PathMapper to PathMapper interface
type MapperAdapter struct {
	*mapper.PathMapper
}

// MapPath adapts the MapPath method to match the interface
func (m *MapperAdapter) MapPath(path string) (mapper.MappingResult, error) {
	result, err := m.PathMapper.MapPath(path)
	if err != nil {
		return mapper.MappingResult{}, err
	}
	if result == nil {
		return mapper.MappingResult{}, nil
	}
	return *result, nil
}

// BackendManagerAdapter adapts backend.Manager to BackendManager interface
type BackendManagerAdapter struct {
	*backend.Manager
}

// KeyManager returns the key manager instance
func (b *BackendManagerAdapter) KeyManager() interface{} {
	return b.Manager.KeyManager()
}

// MetricsAdapter adapts metrics.Collector to MetricsCollector interface
type MetricsAdapter struct {
	*metrics.Collector
}

// GetTopPackages adapts the GetTopPackages method to match the interface
func (m *MetricsAdapter) GetTopPackages(limit int) []metrics.PackageStats {
	topPackages := m.Collector.GetTopPackages(limit)
	result := make([]metrics.PackageStats, len(topPackages))

	for i, pkg := range topPackages {
		result[i] = metrics.PackageStats(pkg)
	}

	return result
}

// GetTopClients adapts the GetTopClients method to match the interface
func (m *MetricsAdapter) GetTopClients(limit int) []metrics.ClientStats {
	topClients := m.Collector.GetTopClients(limit)
	result := make([]metrics.ClientStats, len(topClients))

	for i, client := range topClients {
		result[i] = metrics.ClientStats(client)
	}

	return result
}

// SetLastClientIP implements the MetricsCollector interface
func (m *MetricsAdapter) SetLastClientIP(ip string) {
	m.Collector.SetLastClientIP(ip)
}

// SetLastFileSize implements the MetricsCollector interface
func (m *MetricsAdapter) SetLastFileSize(size int64) {
	m.Collector.SetLastFileSize(size)
}

// KeyManagerAdapter adapts keymanager.KeyManager to KeyManager interface
type KeyManagerAdapter struct {
	*keymanager.KeyManager
}

// HasKey implements the KeyManager interface
func (k *KeyManagerAdapter) HasKey(keyID string) bool {
	return k.KeyManager.HasKey(keyID)
}

// GetKeyPath implements the KeyManager interface
func (k *KeyManagerAdapter) GetKeyPath(keyID string) string {
	return k.KeyManager.GetKeyPath(keyID)
}

// FetchKey implements the KeyManager interface
func (k *KeyManagerAdapter) FetchKey(keyID string) error {
	return k.KeyManager.FetchKey(keyID)
}

// DetectKeyError implements the KeyManager interface
func (k *KeyManagerAdapter) DetectKeyError(data []byte) (string, bool) {
	return k.KeyManager.DetectKeyError(data)
}

// MemoryMonitorAdapter adapts MemoryMonitor to MemoryMonitorInterface
type MemoryMonitorAdapter struct {
	*MemoryMonitor
}

// GetMemoryUsage adapts the memory usage method to match the interface
func (m *MemoryMonitorAdapter) GetMemoryUsage() map[string]any {
	return m.MemoryMonitor.GetMemoryUsage()
}
