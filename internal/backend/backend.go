package backend

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
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
	backends       []*Backend
	cache          *cache.Cache
	client         *http.Client
	mapper         *mapper.PathMapper
	downloadCtx    context.Context
	downloadCancel context.CancelFunc // Added field to store the cancel function
	downloadQ      *queue.Queue
	prefetcher     *Prefetcher
	cacheDir       string
}

// Backend represents a single upstream repository
type Backend struct {
	Name     string
	BaseURL  string
	Priority int
	client   *http.Client // Added client field
}

// New creates a new backend manager
func New(cfg *config.Config, cache *cache.Cache, mapper *mapper.PathMapper) *Manager {
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
		mapper:         mapper, // Use the provided mapper instead of creating a new one
		downloadCtx:    downloadCtx,
		downloadCancel: downloadCancel, // Store the cancel function
		cacheDir:       cfg.CacheDir,
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
	// Cancel the context to release resources
	if m.downloadCancel != nil {
		m.downloadCancel()
	}

	// Stop the download queue
	m.downloadQ.Stop()
}

// Fetch retrieves a package from the appropriate backend
func (m *Manager) Fetch(requestPath string) ([]byte, error) {
    // Map the request path to a repository and cache path
    mappingResult, err := m.mapper.MapPath(requestPath)
    if (err != nil) {
        return nil, err
    }

    // Check if the file exists in cache first
    cacheKey := mappingResult.CachePath

    // Handle repository signature files specially
    isSignatureFile := strings.HasSuffix(mappingResult.RemotePath, ".gpg") ||
        strings.HasSuffix(mappingResult.RemotePath, "InRelease") ||
        strings.HasSuffix(mappingResult.RemotePath, "Release")

    // Try to get from cache first
    data, found, err := m.cache.Get(cacheKey)
    if err == nil && found {
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
            return data, nil  // Use cached data if URL parsing fails
        }
        return nil, err
    }
    u.Path = path.Join(u.Path, mappingResult.RemotePath)
    fullURL := u.String()

    // Use the download queue to fetch
    priority := 1
    if mappingResult.IsIndex || isSignatureFile {
        priority = 10  // Higher priority for index/signature files
    }

    resultCh := m.downloadQ.Submit(fullURL, priority)
    result := <-resultCh

    if result.Error != nil {
        if found {
            // If download fails but we have cached data, use it
            log.Printf("Download failed, using cached data: %v", result.Error)
            return data, nil
        }
        return nil, result.Error
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
    err = m.cache.PutWithExpiration(cacheKey, result.Data, expiration)
    if err != nil {
        log.Printf("Warning: failed to cache %s: %v", cacheKey, err)
    }

    // Process packages files and release files in the background
    if mappingResult.IsIndex && strings.Contains(mappingResult.RemotePath, "Packages") {
        go m.ProcessPackagesFile(mappingResult.Repository, mappingResult.RemotePath, result.Data)
    }

    // Process Release files to find additional index files
    if mappingResult.IsIndex && (strings.HasSuffix(mappingResult.RemotePath, "Release") ||
        strings.HasSuffix(mappingResult.RemotePath, "InRelease")) {
        go m.processReleaseFile(mappingResult.Repository, mappingResult.RemotePath, result.Data)
    }

    return result.Data, nil
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
			go func(path string) {
				if _, err := m.Fetch(path); err != nil {
					log.Printf("Failed to prefetch %s: %v", path, err)
				}
			}(fullPath)
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

// storeCachedFile stores a downloaded file in the cache
func (m *Manager) storeCachedFile(data []byte, cacheKey string) error {
	// Construct the complete cache path
	cachePath := filepath.Join(m.cacheDir, cacheKey)

	// Ensure the directory exists
	dir := filepath.Dir(cachePath)

	// Check if directory exists first
	if stat, err := os.Stat(dir); err == nil && stat.IsDir() {
		// Directory already exists, proceed to file creation
	} else {
		// Try to create the directory
		if err := os.MkdirAll(dir, 0755); err != nil {
			// Check again if it exists (could be a race condition)
			if stat, statErr := os.Stat(dir); statErr == nil && stat.IsDir() {
				// Directory now exists
			} else {
				return fmt.Errorf("failed to create cache directory: %w", err)
			}
		}
	}

	// Write the file
	return os.WriteFile(cachePath, data, 0644)
}

// ProcessPackagesFile processes package index files
func (m *Manager) ProcessPackagesFile(repo string, path string, data []byte) {
	if strings.Contains(path, "Packages") {
		packages, err := parser.ParsePackagesFile(data)
		if err == nil {
			// Update package index
			err := m.cache.UpdatePackageIndex(packages)
			if err != nil {
				log.Printf("Warning: failed to update package index: %v", err)
			}

			// Process packages for prefetching
			go m.prefetcher.ProcessIndexFile(repo, path, data)
		}
	}
}
