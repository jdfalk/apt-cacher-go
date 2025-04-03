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
	"os"
	"path/filepath"
	"regexp"
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

// Modify the New function to use createHTTPClient and add default key prefetching
func New(cfg *config.Config, cache CacheProvider, mapper PathMapperProvider, packageMapper PackageMapperProvider) (*Manager, error) {
	// Create manager first so we can use its methods
	m := &Manager{
		cache:         cache,
		mapper:        mapper,
		packageMapper: packageMapper,
		cacheDir:      cfg.CacheDir,
	}

	// Use the createHTTPClient method instead of creating it inline
	client := m.createHTTPClient()
	m.client = client

	// Create download context
	downloadCtx, downloadCancel := context.WithCancel(context.Background())
	m.downloadCtx = downloadCtx
	m.downloadCancel = downloadCancel

	// Create backends with the HTTP client
	backends := make([]*Backend, 0, len(cfg.Backends))
	for _, b := range cfg.Backends {
		backends = append(backends, &Backend{
			Name:     b.Name,
			BaseURL:  b.URL,
			Priority: b.Priority,
			client:   client,
		})
	}
	m.backends = backends

	// Initialize key manager with cache directory for fallback
	km, err := keymanager.New(&cfg.KeyManagement, cfg.CacheDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize key manager: %w", err)
	}
	m.keyManager = km

	// Prefetch common repository keys if key management is enabled
	if km != nil && cfg.KeyManagement.Enabled {
		// Define comprehensive list of common repository keys
		commonKeys := []string{
			"871920D1991BC93C", // Ubuntu archive
			"3B4FE6ACC0B21F32", // Debian archive
			"A1BD8E9D78F7FE5C", // Debian security
			"F6ECB3762474EDA9", // Docker release
			"54404762BBB6E853", // PostgreSQL
			"8507EFA5",         // Grafana
			"D94AA3F0",         // NodeJS
		}

		// Start prefetching in background to avoid blocking startup
		go func() {
			log.Printf("Starting prefetch of %d common repository keys", len(commonKeys))
			successful, failed, err := km.PrefetchDefaultKeys(commonKeys)
			if err != nil {
				log.Printf("Error during key prefetch: %v", err)
			} else {
				log.Printf("Key prefetch complete: %d successful, %d failed", successful, failed)
			}
		}()
	}

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

// Modify the Fetch method to enhance error handling for keys
func (m *Manager) Fetch(path string) ([]byte, error) {
	// Check if this is a keyserver request we need to handle
	if strings.Contains(path, "/keys/") || strings.Contains(path, "/pks/lookup") {
		return m.handleKeyRequest(path)
	}

	// Regular fetch process
	mappingResult, err := m.mapper.MapPath(path)
	if err != nil {
		return nil, fmt.Errorf("error mapping path %s: %w", path, err)
	}

	// Find the correct backend for this repository using our helper function
	selectedBackend, err := m.selectBackendByName(mappingResult.Repository)
	if err != nil {
		return nil, fmt.Errorf("no backend found for repository %s (path: %s)",
			mappingResult.Repository, path)
	}

	// Original fetch logic - using the selectedBackend
	data, err := m.downloadFromURL(filepath.Join(selectedBackend.BaseURL, mappingResult.RemotePath))
	if err != nil {
		return nil, err
	}

	// Enhanced key error detection and handling
	if m.keyManager != nil {
		keyID, hasKeyError := m.keyManager.DetectKeyError(data)
		if hasKeyError {
			log.Printf("Detected missing GPG key: %s for path %s, attempting to fetch", keyID, path)
			err := m.keyManager.FetchKey(keyID)
			if err != nil {
				log.Printf("Failed to fetch key %s: %v", keyID, err)
			} else {
				log.Printf("Successfully fetched key %s, retrying request", keyID)

				// Look for additional keys in the response
				keyIDs := extractKeyReferences(data)
				for _, additionalKey := range keyIDs {
					if additionalKey != keyID && len(additionalKey) >= 8 {
						log.Printf("Found additional key reference %s, prefetching", additionalKey)
						if fetchErr := m.keyManager.FetchKey(additionalKey); fetchErr != nil {
							log.Printf("Failed to fetch additional key %s: %v", additionalKey, fetchErr)
						}
					}
				}

				// Now we need to re-fetch the original content, which might
				// succeed if the backend can use the key we just fetched
				updatedData, retryErr := m.downloadFromURL(filepath.Join(selectedBackend.BaseURL, mappingResult.RemotePath))
				if retryErr == nil {
					// Check if key error is gone
					_, stillHasError := m.keyManager.DetectKeyError(updatedData)
					if !stillHasError {
						log.Printf("Successfully fetched content after retrieving key %s", keyID)

						// If this is a Release file, process it to find index files
						if strings.HasSuffix(path, "Release") || strings.HasSuffix(path, "InRelease") {
							m.ProcessReleaseFile(mappingResult.Repository, path, updatedData)
						}

						return updatedData, nil
					}
					log.Printf("Key error persists after fetching key %s, returning original response", keyID)
				} else {
					log.Printf("Retry failed after fetching key %s: %v", keyID, retryErr)
				}
			}
		}
	}

	// For release files, process them to find index files
	if strings.HasSuffix(path, "Release") || strings.HasSuffix(path, "InRelease") {
		m.ProcessReleaseFile(mappingResult.Repository, path, data)
	}

	return data, nil
}

// New method to handle keyserver requests
func (m *Manager) handleKeyRequest(path string) ([]byte, error) {
	// Extract key ID from various possible request formats
	var keyID string

	if strings.Contains(path, "/pks/lookup") {
		// Parse keyserver request: /pks/lookup?op=get&search=0xKEYID
		if strings.Contains(path, "search=0x") {
			parts := strings.Split(path, "search=0x")
			if len(parts) > 1 {
				keyID = strings.Split(parts[1], "&")[0]
			}
		}
	} else if strings.Contains(path, "/keys/") {
		// Direct key file request: /keys/KEYID.gpg
		fileName := filepath.Base(path)
		keyID = strings.TrimSuffix(fileName, ".gpg")
	}

	if keyID == "" {
		return nil, fmt.Errorf("could not extract key ID from request: %s", path)
	}

	// Check if we have the key
	if m.keyManager == nil {
		return nil, fmt.Errorf("key manager not available")
	}

	if !m.keyManager.HasKey(keyID) {
		// Try to fetch it
		err := m.keyManager.FetchKey(keyID)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch key %s: %w", keyID, err)
		}
	}

	// Get and return the key data
	keyPath := m.keyManager.GetKeyPath(keyID)
	if keyPath == "" {
		return nil, fmt.Errorf("key not available after fetch")
	}

	return os.ReadFile(keyPath)
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
		if b.Name == repoName {
			return b, nil
		}
	}

	// If no specific match, fall back to the default
	if len(m.backends) > 0 {
		return m.backends[0], nil
	}

	return nil, fmt.Errorf("no matching backend for %s", repoName)
}

// downloadFromURL downloads data from a URL with proper error handling.
//
// This function:
// - Ensures the URL is properly formatted with correct scheme
// - Makes an HTTP request to the given URL
// - Handles any network or HTTP errors
// - Returns the complete response body as a byte slice
//
// Parameters:
// - url: The URL to download from
//
// Returns:
// - The response body as a byte slice
// - Any error encountered during the download
func (m *Manager) downloadFromURL(url string) ([]byte, error) {
	// Ensure URL has proper format with double slashes after http:
	if strings.HasPrefix(url, "http:/") && !strings.HasPrefix(url, "http://") {
		url = strings.Replace(url, "http:/", "http://", 1)
	}

	// Create request with context for cancellation
	req, err := http.NewRequestWithContext(m.downloadCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add standard headers
	req.Header.Set("User-Agent", "apt-cacher-go/1.0")

	// Execute request
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("backend request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("backend returned status %d", resp.StatusCode)
	}

	// Read response body
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

// ProcessPackagesFile parses and stores package information from an index file
func (m *Manager) ProcessPackagesFile(repo, path string, data []byte) {
	// Parse packages from the file
	packages, err := parser.ParsePackagesFile(data)
	if err != nil {
		log.Printf("Error parsing packages file %s: %v", path, err)
		return
	}

	// Store packages in database
	beforeCount := 0
	packageList, err := m.cache.SearchByPackageName("")
	if err == nil {
		beforeCount = len(packageList)
	}

	err = m.cache.UpdatePackageIndex(packages)
	if err != nil {
		log.Printf("Error updating package index: %v", err)
		return
	}

	// Get the new count to calculate how many were added
	afterCount := 0
	packageList, err = m.cache.SearchByPackageName("")
	if err == nil {
		afterCount = len(packageList)
	}

	// Log the update
	newPackages := afterCount - beforeCount
	if newPackages > 0 {
		log.Printf("Added %d new packages from %s (index contains %d packages)",
			newPackages, path, afterCount)
	} else {
		log.Printf("Package index update: no new packages added (index contains %d packages)",
			afterCount)
	}

	// Process packages for prefetching if enabled
	if m.prefetcher != nil && len(packages) > 0 {
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

// RefreshReleaseData refreshes and reprocesses a release file after key changes.
//
// This method is called after a GPG key has been fetched to reprocess a release file
// that previously had signature verification issues. It performs the following steps:
//
// 1. Maps the path to a repository
// 2. Fetches fresh data for the path
// 3. Processes the release file with the updated data
//
// Parameters:
// - path: The path to the release file to refresh
//
// Returns:
// - Any error encountered during the refresh process
func (m *Manager) RefreshReleaseData(path string) error {
	// Map the path to determine the repository
	mappingResult, err := m.mapper.MapPath(path)
	if err != nil {
		return fmt.Errorf("error mapping path %s: %w", path, err)
	}

	// Fetch the data again (now that we have the key)
	data, err := m.Fetch(path)
	if err != nil {
		return fmt.Errorf("error fetching refreshed data: %w", err)
	}

	// Process the release file with updated data
	m.ProcessReleaseFile(mappingResult.Repository, path, data)
	return nil
}

// Helper function to extract key references
func extractKeyReferences(data []byte) []string {
	content := string(data)
	keyIDs := make([]string, 0)

	// Look for common key reference formats
	patterns := []string{
		`Key ID: ([0-9A-F]{8,})`,
		`keyid ([0-9A-F]{8,})`,
		`fingerprint: ([0-9A-F]{40})`,
		`NO_PUBKEY ([0-9A-F]{8,})`,
		`gpg: using RSA key ([0-9A-F]{8,})`,
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(content, -1)
		for _, match := range matches {
			if len(match) > 1 {
				// For fingerprints, use the last 8+ characters as keyID
				keyID := match[1]
				if len(keyID) > 16 {
					keyID = keyID[len(keyID)-16:]
				}
				keyIDs = append(keyIDs, keyID)
			}
		}
	}

	return keyIDs
}
