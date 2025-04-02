package backend

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"

	cachelib "github.com/jdfalk/apt-cacher-go/internal/cache"
	"github.com/jdfalk/apt-cacher-go/internal/config"
	"github.com/jdfalk/apt-cacher-go/internal/keymanager"
	"github.com/jdfalk/apt-cacher-go/internal/mapper"
	"github.com/jdfalk/apt-cacher-go/internal/parser"
	"github.com/jdfalk/apt-cacher-go/internal/queue"
)

// CacheProvider defines the interface for cache operations
type CacheProvider interface {
	Get(path string) ([]byte, error)
	Put(path string, data []byte) error
	PutWithExpiration(path string, data []byte, ttl time.Duration) error
	IsFresh(path string) bool
	Exists(path string) bool
	UpdatePackageIndex(packages []parser.PackageInfo) error
	SearchByPackageName(name string) ([]cachelib.CacheSearchResult, error)
	GetStats() cachelib.CacheStats
}

// PathMapperProvider defines the interface for path mapping operations
type PathMapperProvider interface {
	MapPath(path string) (mapper.MappingResult, error)
}

// PackageMapperProvider defines the interface for package mapping operations
type PackageMapperProvider interface {
	AddHashMapping(hash, packageName string)
	GetPackageNameForHash(path string) string
}

// Manager handles connections to upstream repositories
type Manager struct {
	backends       []*Backend
	cache          CacheProvider
	client         *http.Client
	mapper         PathMapperProvider
	packageMapper  PackageMapperProvider // Add this line
	downloadCtx    context.Context
	downloadCancel context.CancelFunc // Added field to store the cancel function
	downloadQ      *queue.Queue
	prefetcher     *Prefetcher
	cacheDir       string
	keyManager     *keymanager.KeyManager
}

// Backend represents a single upstream repository
type Backend struct {
	Name     string
	BaseURL  string
	Priority int
	client   *http.Client // Added client field
}

// Modify the New function to accept interfaces instead of concrete types
func New(cfg *config.Config, cache CacheProvider, mapper PathMapperProvider, packageMapper PackageMapperProvider) (*Manager, error) {
	// Create HTTP client for backends
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	backends := make([]*Backend, 0, len(cfg.Backends))

	for _, b := range cfg.Backends {
		backends = append(backends, &Backend{
			Name:     b.Name,
			BaseURL:  b.URL,
			Priority: b.Priority,
			client:   client, // Initialize client for each backend
		})
	}

	// Create download queue with desired concurrency
	downloadCtx, downloadCancel := context.WithCancel(context.Background())

	m := &Manager{
		backends:       backends,
		cache:          cache,
		client:         client,
		mapper:         mapper,        // Use the provided mapper instead of creating a new one
		packageMapper:  packageMapper, // Add this line
		downloadCtx:    downloadCtx,
		downloadCancel: downloadCancel, // Store the cancel function
		cacheDir:       cfg.CacheDir,
	}

	// Initialize key manager with cache directory for fallback
	km, err := keymanager.New(&cfg.KeyManagement, cfg.CacheDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize key manager: %w", err)
	}

	m.keyManager = km

	// Create the download queue
	m.downloadQ = queue.New(cfg.MaxConcurrentDownloads)

	// Create prefetcher if enabled
	if cfg.Prefetch.Enabled {
		// Use the proper config values from Prefetch struct
		maxConcurrent := cfg.Prefetch.MaxConcurrent
		if maxConcurrent <= 0 {
			maxConcurrent = 5 // Default if not specified
		}

		m.prefetcher = NewPrefetcher(m, maxConcurrent, cfg.Prefetch.Architectures)

		// Set batch size if specified in config
		if cfg.Prefetch.BatchSize > 0 {
			m.prefetcher.SetBatchSize(cfg.Prefetch.BatchSize)
		}
	}

	// Start the download queue with the handler function
	m.downloadQ.Start(func(task any) error {
		url, ok := task.(string)
		if !ok {
			return fmt.Errorf("invalid task type: %T", task)
		}

		// Download but don't store result since we're not using it
		_, err := m.downloadFromURL(url)
		return err
	})

	return m, nil
}

// Shutdown cleans up resources
func (m *Manager) Shutdown() {
	// Cancel the context to release resources
	if m.downloadCancel != nil {
		m.downloadCancel()
	}

	// Stop the download queue
	m.downloadQ.Stop()

	// Add this line to properly shutdown the prefetcher
	if m.prefetcher != nil {
		m.prefetcher.Shutdown()
	}
}

// Fix the Fetch method to handle the queue Submit properly
func (m *Manager) Fetch(requestPath string) ([]byte, error) {
	// Map the request path to a repository and cache path
	mappingResult, err := m.mapper.MapPath(requestPath)
	if err != nil {
		return nil, err
	}

	// Check if the file exists in cache first
	cacheKey := mappingResult.CachePath

	// Try to get from cache first
	data, err := m.cache.Get(cacheKey)
	found := err == nil && len(data) > 0

	// Handle repository signature files specially
	isSignatureFile := strings.HasSuffix(mappingResult.RemotePath, ".gpg") ||
		strings.HasSuffix(mappingResult.RemotePath, "InRelease") ||
		strings.HasSuffix(mappingResult.RemotePath, "Release")

	if found {
		// For regular files, always use cache if found
		if !mappingResult.IsIndex && !isSignatureFile {
			return data, nil
		}

		// For index and signature files, check if they are fresh
		if m.cache.IsFresh(cacheKey) {
			// If cache is fresh, use it
			log.Printf("Using cached %s", requestPath)
			return data, nil
		}

		// Otherwise, refresh from backend but still return cached data
		// if backend fails
		log.Printf("Refreshing %s", requestPath)
	}

	// Fetch from backend if not found in cache or needs refreshing
	fileType := "file"
	if isSignatureFile {
		fileType = "signature file"
	}
	log.Printf("Fetching %s: %s",
		fileType,
		mappingResult.RemotePath)

	// Select backend based on the repository name
	backend, err := m.selectBackendByName(mappingResult.Repository)
	if err != nil {
		if found {
			// If backend selection fails but we have cached data, use it
			log.Printf("Backend selection failed, using cached data: %v", err)
			return data, nil
		}
		return nil, err
	}

	// Construct full URL
	u, err := url.Parse(backend.BaseURL)
	if err != nil {
		if found {
			return data, nil // Use cached data if URL parsing fails
		}
		return nil, err
	}
	u.Path = path.Join(u.Path, mappingResult.RemotePath)
	fullURL := u.String()

	// Download directly for now instead of using queue
	result, err := m.downloadFromURL(fullURL)
	if err != nil {
		if found {
			// If download fails but we have cached data, use it
			log.Printf("Download failed, using cached data: %v", err)
			return data, nil
		}
		return nil, err
	}

	// Determine cache policy based on file type
	var expiration time.Duration
	if mappingResult.IsIndex || isSignatureFile {
		// Index files expire quickly (1 hour by default)
		expiration, _ = time.ParseDuration("1h")
	} else {
		// Package files are kept for a month
		expiration, _ = time.ParseDuration("720h")
	}

	// Store in cache with expiration
	err = m.cache.PutWithExpiration(cacheKey, result, expiration)
	if err != nil {
		log.Printf("Warning: failed to cache %s: %v", cacheKey, err)
	}

	// Process packages files and release files in the background
	if mappingResult.IsIndex && strings.Contains(mappingResult.RemotePath, "Packages") {
		go m.ProcessPackagesFile(mappingResult.Repository, mappingResult.RemotePath, result)
	}

	// Process Release files to find additional index files
	if mappingResult.IsIndex && (strings.HasSuffix(mappingResult.RemotePath, "Release") ||
		strings.HasSuffix(mappingResult.RemotePath, "InRelease")) {
		go m.ProcessReleaseFile(mappingResult.Repository, mappingResult.RemotePath, result)
	}

	return result, nil
}

// ProcessReleaseFile analyzes a Release file to find additional index files
// Changed from private 'processReleaseFile' to public 'ProcessReleaseFile' for testing
func (m *Manager) ProcessReleaseFile(repo string, path string, data []byte) {
	// Parse the Release file
	filenames, err := parser.ParseIndexFilenames(data)
	if err != nil {
		return
	}

	// Extract the base directory from the path
	basedir := filepath.Dir(path)

	// Get the configured architectures
	var architectures []string
	if m.prefetcher != nil && m.prefetcher.architectures != nil {
		for arch := range m.prefetcher.architectures {
			architectures = append(architectures, arch)
		}
	}

	// Prefetch important index files
	for _, filename := range filenames {
		// Only prefetch Packages or Sources files
		if !strings.Contains(filename, "Packages") && !strings.Contains(filename, "Sources") {
			continue
		}

		// Check if this is an architecture-specific file
		if strings.Contains(filename, "binary-") && len(architectures) > 0 {
			// Skip files for architectures we don't care about
			isRelevantArch := false
			for _, arch := range architectures {
				if strings.Contains(filename, "binary-"+arch) {
					isRelevantArch = true
					break
				}
			}
			if !isRelevantArch {
				continue
			}
		}

		fullPath := fmt.Sprintf("/%s/%s/%s", repo, basedir, filename)
		go func(path string) {
			if _, err := m.Fetch(path); err != nil {
				// Don't log 404 errors to reduce noise
				if !strings.Contains(err.Error(), "404") {
					log.Printf("Failed to prefetch %s: %v", path, err)
				}
			}
		}(fullPath)
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

// DownloadResult represents the result of a download operation
type DownloadResult struct {
	Success      bool
	BytesWritten int64
	Error        error
	Source       string
	StatusCode   int
}

// DownloadPackage downloads a package from the backend repository
func (b *Backend) DownloadPackage(ctx context.Context, path string, w io.Writer) DownloadResult {
	// Construct the full URL
	u, err := url.Parse(b.BaseURL)
	if err != nil {
		return DownloadResult{
			Success: false,
			Error:   fmt.Errorf("invalid backend URL: %w", err),
			Source:  b.Name,
		}
	}
	u.Path = filepath.Join(u.Path, path)
	fullURL := u.String()

	// Create the request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return DownloadResult{
			Success: false,
			Error:   fmt.Errorf("failed to create request: %w", err),
			Source:  b.Name,
		}
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return DownloadResult{
			Success: false,
			Error:   fmt.Errorf("failed to download from %s: %w", b.BaseURL, err),
			Source:  b.Name,
		}
	}
	defer resp.Body.Close()

	// Check response status code
	if resp.StatusCode != http.StatusOK {
		return DownloadResult{
			Success:    false,
			Error:      fmt.Errorf("received non-OK status: %d", resp.StatusCode),
			Source:     b.Name,
			StatusCode: resp.StatusCode,
		}
	}

	// Copy response body to writer
	written, err := io.Copy(w, resp.Body)
	if err != nil {
		return DownloadResult{
			Success:      false,
			BytesWritten: written,
			Error:        fmt.Errorf("error copying response body: %w", err),
			Source:       b.Name,
			StatusCode:   resp.StatusCode,
		}
	}

	// Return success result
	return DownloadResult{
		Success:      true,
		BytesWritten: written,
		Source:       b.Name,
		StatusCode:   resp.StatusCode,
	}
}

func (m *Manager) Download(ctx context.Context, path string, backend *Backend) (*DownloadResult, error) {
	// Create a new context with cancellation
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Create temporary buffer
	var buf bytes.Buffer

	// Download to buffer
	result := backend.DownloadPackage(ctx, path, &buf)

	// Handle download failure
	if !result.Success {
		return nil, result.Error
	}

	// Return the result
	return &result, nil
}

// ProcessPackagesFile processes package index files
func (m *Manager) ProcessPackagesFile(repo string, path string, data []byte) {
	packages, err := parser.ParsePackages(data)
	if err != nil {
		log.Printf("Error parsing packages file: %v", err)
		return
	}

	// Store package information in cache
	err = m.cache.UpdatePackageIndex(packages)
	if err != nil {
		log.Printf("Error updating package index in cache: %v", err)
	}

	// Populate package mapper with hash information
	if m.packageMapper != nil {
		for _, pkg := range packages {
			if pkg.SHA256 != "" {
				log.Printf("Added hash mapping: %s -> %s", pkg.SHA256, pkg.Package)
				m.packageMapper.AddHashMapping(pkg.SHA256, pkg.Package)
			}
		}
	}

	// If prefetcher is enabled, process the data for potential prefetching
	if m.prefetcher != nil {
		m.prefetcher.ProcessIndexFile(repo, path, data)
	}
}

// ForceCleanupPrefetcher forces cleanup of stale prefetch operations
func (m *Manager) ForceCleanupPrefetcher() int {
	if m.prefetcher != nil {
		return m.prefetcher.ForceCleanup()
	}
	return 0
}

// KeyManager returns the key manager instance
func (m *Manager) KeyManager() *keymanager.KeyManager {
	return m.keyManager
}

// GetAllBackends returns all configured backends
func (m *Manager) GetAllBackends() []*Backend {
	if m == nil || m.backends == nil {
		return []*Backend{}
	}
	return m.backends
}

// PrefetchOnStartup warms the cache by prefetching common files
func (m *Manager) PrefetchOnStartup(ctx context.Context) {
	if m.prefetcher != nil {
		m.prefetcher.RunStartupPrefetch(ctx)
	} else {
		log.Printf("Prefetcher not initialized, skipping startup prefetch")
	}
}

func (m *Manager) createHTTPClient() *http.Client {
	// Create a transport with more conservative settings
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  false,
		Dial: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).Dial,
		TLSHandshakeTimeout: 10 * time.Second,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
	}
}
