package backend

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/cache"
	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/mapper"
	"github.com/jdfalk/apt-cacher-go/internal/parser"
	"github.com/jdfalk/apt-cacher-go/internal/queue"
)

// Manager handles connections to upstream repositories
type Manager struct {
	backends    []*Backend
	cache       *cache.Cache
	client      *http.Client
	mapper      *mapper.PathMapper
	downloadCtx context.Context
	downloadQ   *queue.Queue
	prefetcher  *Prefetcher
}

// Backend represents a single upstream repository
type Backend struct {
	Name     string
	BaseURL  string
	Priority int
}

// New creates a new backend manager
func New(cfg *config.Config, cache *cache.Cache) *Manager {
	backends := make([]*Backend, 0, len(cfg.Backends))

	for _, b := range cfg.Backends {
		backends = append(backends, &Backend{
			Name:     b.Name,
			BaseURL:  b.URL,
			Priority: b.Priority,
		})
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Create download queue with desired concurrency
	downloadCtx, _ := context.WithCancel(context.Background())

	m := &Manager{
		backends:    backends,
		cache:       cache,
		client:      client,
		mapper:      mapper.New(),
		downloadCtx: downloadCtx,
	}

	// Create the download queue with a downloader function
	m.downloadQ = queue.New(cfg.MaxConcurrentDownloads, func(url string) ([]byte, error) {
		return m.downloadFromURL(url)
	})

	// Create prefetcher
	m.prefetcher = NewPrefetcher(m, cfg.MaxConcurrentDownloads/2)

	// Start the download queue
	m.downloadQ.Start(downloadCtx)

	return m
}

// Shutdown cleans up resources
func (m *Manager) Shutdown() {
	// Stop the download queue
	m.downloadQ.Stop()
}

// Fetch retrieves a package from the appropriate backend
func (m *Manager) Fetch(requestPath string) ([]byte, error) {
	// Map the path to determine which backend to use
	mappingResult, err := m.mapper.MapPath(requestPath)
	if err != nil {
		return nil, fmt.Errorf("path mapping error: %w", err)
	}

	cachePath := mappingResult.CachePath

	// Try to get from cache first
	data, found, err := m.cache.Get(cachePath)
	if err != nil {
		return nil, fmt.Errorf("cache error: %w", err)
	}

	// For index files, we might need to refresh even if found
	if found && !mappingResult.IsIndex {
		return data, nil
	} else if found && mappingResult.IsIndex {
		// For index files, check if they are fresh enough
		if m.cache.IsFresh(cachePath) {
			return data, nil
		}
		// Otherwise fall through to backend fetch
	}

	// Select backend based on the repository name
	backend, err := m.selectBackendByName(mappingResult.Repository)
	if err != nil {
		return nil, err
	}

	// Construct full URL
	u, err := url.Parse(backend.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid backend URL: %w", err)
	}
	u.Path = path.Join(u.Path, mappingResult.RemotePath)
	fullURL := u.String()

	// Use the download queue to fetch
	priority := 1
	if mappingResult.IsIndex {
		priority = 2 // Higher priority for index files
	}

	resultCh := m.downloadQ.Submit(fullURL, priority)
	result := <-resultCh

	if result.Error != nil {
		return nil, result.Error
	}

	data = result.Data

	// Determine cache policy based on file type
	var expiration time.Duration
	if mappingResult.IsIndex {
		expiration = 30 * time.Minute // Index files expire quickly
	} else {
		expiration = 30 * 24 * time.Hour // Package files are kept for a month
	}

	// Store in cache with the appropriate expiration
	if err := m.cache.PutWithExpiration(cachePath, data, expiration); err != nil {
		return nil, fmt.Errorf("failed to cache data: %w", err)
	}

	// If this is an index file, trigger analysis and prefetch
	if mappingResult.IsIndex && strings.Contains(mappingResult.RemotePath, "Packages") {
		go m.prefetcher.ProcessIndexFile(mappingResult.Repository, mappingResult.RemotePath, data)
	}

	// Process Release files to find additional index files
	if mappingResult.IsIndex && (strings.HasSuffix(mappingResult.RemotePath, "Release") ||
		strings.HasSuffix(mappingResult.RemotePath, "InRelease")) {
		go m.processReleaseFile(mappingResult.Repository, mappingResult.RemotePath, data)
	}

	return data, nil
}

// processReleaseFile analyzes a Release file to find additional index files
func (m *Manager) processReleaseFile(repo string, path string, data []byte) {
	// Parse the Release file
	filenames, err := parser.ParseIndexFilenames(data)
	if err != nil {
		return
	}

	// Extract the base directory from the path
	basedir := filepath.Dir(path)

	// Prefetch important index files
	for _, filename := range filenames {
		if strings.Contains(filename, "Packages") || strings.Contains(filename, "Sources") {
			fullPath := fmt.Sprintf("/%s/%s/%s", repo, basedir, filename)
			go m.Fetch(fullPath)
		}
	}
}

// selectBackendByName finds a backend by repository name
func (m *Manager) selectBackendByName(repoName string) (*Backend, error) {
	for _, b := range m.backends {
		// Simple name matching - can be more sophisticated
		if strings.Contains(b.Name, repoName) {
			return b, nil
		}
	}

	// If no specific match, fall back to the default
	if len(m.backends) > 0 {
		return m.backends[0], nil
	}

	return nil, fmt.Errorf("no matching backend for %s", repoName)
}

// downloadFromURL downloads data from a URL
func (m *Manager) downloadFromURL(url string) ([]byte, error) {
	resp, err := m.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("backend request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("backend returned non-OK status: %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// ParseRepositoryPath extracts components from an apt request path
func ParseRepositoryPath(requestPath string) (string, error) {
	// This is a simplified version; apt-cacher-ng has complex path parsing logic
	// Remove leading slash if present
	path := strings.TrimPrefix(requestPath, "/")

	// Basic validation
	if path == "" {
		return "", fmt.Errorf("empty repository path")
	}

	return path, nil
}
