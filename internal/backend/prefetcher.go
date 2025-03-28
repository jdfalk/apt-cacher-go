package backend

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/parser"
)

// Prefetcher manages background prefetching of packages
type Prefetcher struct {
	manager        *Manager
	active         sync.Map // Track active prefetch operations
	maxActive      int
	cleanupTick    *time.Ticker
	wg             sync.WaitGroup
	stopCh         chan struct{}
	architectures  map[string]bool
	verboseLogging bool // Add this field to control 404 logging
	startupDone    bool // Track whether startup prefetch has completed
}

// PrefetchOperation tracks a single prefetch operation
type PrefetchOperation struct {
	URL       string
	StartTime time.Time
	Done      chan struct{}
	Result    string
}

// NewPrefetcher creates a new prefetcher
func NewPrefetcher(manager *Manager, maxActive int, architectures []string) *Prefetcher {
	// Create map for fast lookup
	archMap := make(map[string]bool)
	for _, arch := range architectures {
		archMap[arch] = true
	}

	p := &Prefetcher{
		manager:       manager,
		maxActive:     maxActive,
		cleanupTick:   time.NewTicker(30 * time.Second),
		stopCh:        make(chan struct{}),
		architectures: archMap,
	}

	// Start background cleanup goroutine
	go p.cleanupRoutine()

	return p
}

// cleanupRoutine periodically checks for stale operations
func (p *Prefetcher) cleanupRoutine() {
	for {
		select {
		case <-p.stopCh:
			return
		case <-p.cleanupTick.C:
			p.cleanupStalePrefetches()
		}
	}
}

// Add a method to filter URLs by architecture:
func (p *Prefetcher) filterByArchitecture(urls []string) []string {
	// If no architectures specified, don't filter
	if len(p.architectures) == 0 {
		return urls
	}

	filtered := make([]string, 0, len(urls))
	for _, url := range urls {
		// Don't filter non-architecture-specific files
		if !strings.Contains(url, "binary-") && !strings.Contains(url, "-installer/") {
			filtered = append(filtered, url)
			continue
		}

		// Check if this URL matches one of our configured architectures
		matchesArch := false
		for arch := range p.architectures {
			if strings.Contains(url, "binary-"+arch) || strings.Contains(url, "-installer/binary-"+arch) {
				matchesArch = true
				break
			}
		}

		if matchesArch {
			filtered = append(filtered, url)
		} else {
			log.Printf("Skipping prefetch for non-configured architecture: %s", url)
		}
	}

	return filtered
}

// ProcessIndexFile processes package index files and prefetches packages
func (p *Prefetcher) ProcessIndexFile(repo string, path string, data []byte) {
	// Don't process if not a Packages file
	if !strings.HasPrefix(filepath.Base(path), "Packages") {
		return
	}

	// Extract package URLs
	urls, err := parser.ExtractPackageURLs(repo, data)
	if err != nil {
		log.Printf("Error extracting package URLs: %v", err)
		return
	}

	// Skip if no URLs found
	if len(urls) == 0 {
		log.Printf("Prefetch operation completed: processed 0/0 URLs in %v", time.Duration(0))
		return
	}

	// Filter URLs by architecture
	urls = p.filterByArchitecture(urls)

	// Skip if no URLs after filtering
	if len(urls) == 0 {
		log.Printf("No URLs left after architecture filtering")
		return
	}

	// Limit to most popular packages
	if len(urls) > 20 {
		urls = urls[:20]
	}

	// Check if we're over the limit of active operations
	activeCount := 0
	p.active.Range(func(_, _ interface{}) bool {
		activeCount++
		return true
	})

	if activeCount >= p.maxActive {
		log.Printf("Skipping prefetch, too many active operations: %d", activeCount)
		return
	}

	// Use a WaitGroup to track all URL fetches
	var wg sync.WaitGroup
	resultsCh := make(chan string, len(urls))
	fetchTime := time.Now()

	// Use sync.Once to ensure the channel is only closed once
	var closeOnce sync.Once

	// Create a context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // Ensure context is cancelled when we return

	// Create an operation ID
	opID := fmt.Sprintf("%s:%d", path, time.Now().UnixNano())

	// Register this operation
	p.active.Store(opID, fetchTime)

	// Start a goroutine to track completion and cleanup
	go func() {
		// Wait for all fetches to complete
		wg.Wait()

		// Get elapsed time
		elapsed := time.Since(fetchTime)

		// Clean up resources
		p.active.Delete(opID)

		// Close the results channel safely
		closeOnce.Do(func() {
			close(resultsCh)
		})

		// Log completion
		successCount := 0
		p.active.Range(func(k, v interface{}) bool {
			if strings.HasPrefix(k.(string), opID+":") {
				successCount++
			}
			return true
		})

		log.Printf("Prefetch operation completed: processed %d/%d URLs in %v", successCount, len(urls), elapsed)
	}()

	// Process each URL
	for i, u := range urls {
		wg.Add(1)

		// Create unique ID for this URL fetch
		urlID := fmt.Sprintf("%s:%d", opID, i)

		// Store it in active map
		p.active.Store(urlID, time.Now())

		// Launch goroutine to fetch
		go func(url string, id string) {
			defer wg.Done()
			defer p.active.Delete(id)

			// Skip if context is already cancelled
			select {
			case <-ctx.Done():
				return
			default:
				// Continue processing
			}

			// Fetch the file
			_, err := p.manager.Fetch(url)

			// Handle result with improved error handling
			if err != nil {
				if strings.Contains(err.Error(), "404") {
					// Lower log level for 404 errors - they're expected
					if p.verboseLogging {
						log.Printf("Debug: Prefetch 404 for %s", url)
					}
				} else {
					// Log other errors normally
					log.Printf("Failed to prefetch %s: %v", url, err)
				}
				return
			}

			// Send success - safely handle channel send
			select {
			case resultsCh <- url:
				// Successfully sent result
			case <-ctx.Done():
				// Context cancelled, don't try to send
			default:
				// Channel full or closed, don't block
			}
		}(u, urlID)
	}
}

// cleanupStalePrefetches removes prefetch operations that have been running too long
func (p *Prefetcher) cleanupStalePrefetches() {
	now := time.Now()
	toDelete := make([]string, 0)

	// First pass - identify stale operations
	p.active.Range(func(key, value interface{}) bool {
		k := key.(string)
		v := value.(time.Time)

		age := now.Sub(v)
		if age > 15*time.Second {
			toDelete = append(toDelete, k)
		}
		return true
	})

	// Second pass - delete them
	for _, key := range toDelete {
		if val, ok := p.active.Load(key); ok {
			startTime := val.(time.Time)
			p.active.Delete(key)
			log.Printf("Cleaned up stale prefetch: %s (age: %v)", key, now.Sub(startTime))
		} else {
			p.active.Delete(key)
		}
	}

	if len(toDelete) > 0 {
		log.Printf("Cleaned up %d stale prefetch operations", len(toDelete))
	}
}

// Shutdown waits for all prefetch operations to complete
func (p *Prefetcher) Shutdown() {
	// Signal the cleanup routine to stop
	close(p.stopCh)

	// Wait with a timeout
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Printf("All prefetch operations completed gracefully")
	case <-time.After(5 * time.Second):
		log.Printf("Timed out waiting for prefetch operations to complete")
	}
}

// ForceCleanup immediately cleans up all prefetch operations
// Can be called from admin endpoints to unstick a stuck prefetcher
func (p *Prefetcher) ForceCleanup() int {
	cleaned := 0
	p.active.Range(func(key, _ interface{}) bool {
		p.active.Delete(key)
		cleaned++
		return true
	})
	log.Printf("Force-cleaned %d prefetch operations", cleaned)
	return cleaned
}

// AddToConfig adds a set of architectures to filter
func (p *Prefetcher) AddArchitecture(architectures ...string) {
	for _, arch := range architectures {
		p.architectures[arch] = true
	}
}

// IsArchitectureEnabled checks if a specific architecture is enabled
func (p *Prefetcher) IsArchitectureEnabled(arch string) bool {
	// If no architectures specified, all are enabled
	if len(p.architectures) == 0 {
		return true
	}
	return p.architectures[arch]
}

// SetVerboseLogging controls whether to log 404 errors
func (p *Prefetcher) SetVerboseLogging(verbose bool) {
	p.verboseLogging = verbose
}

// PrefetchOnStartup warms the cache by fetching common index files
func (p *Prefetcher) PrefetchOnStartup(ctx context.Context) {
	if p.startupDone {
		return
	}

	log.Printf("Starting initial cache warm-up...")
	startTime := time.Now()

	// Get all configured backends
	backends := p.manager.GetAllBackends()
	var wg sync.WaitGroup

	// Common index file patterns to fetch for each repository
	indexPaths := []string{
		"dists/%s/InRelease",
		"dists/%s/Release",
		"dists/%s/Release.gpg",
	}

	// For each enabled architecture, add architecture-specific indexes
	archPaths := []string{
		"dists/%s/main/binary-%s/Packages",
		"dists/%s/main/binary-%s/Packages.gz",
		"dists/%s/main/binary-%s/Packages.xz",
		"dists/%s/universe/binary-%s/Packages",
		"dists/%s/universe/binary-%s/Packages.gz",
		"dists/%s/universe/binary-%s/Packages.xz",
		"dists/%s/restricted/binary-%s/Packages",
		"dists/%s/multiverse/binary-%s/Packages",
	}

	// Common releases to try (this can be made configurable)
	releases := []string{"stable", "testing", "unstable", "jammy", "focal", "noble", "oracular"}

	for _, backend := range backends {
		wg.Add(1)
		go func(b *Backend) {
			defer wg.Done()

			for _, release := range releases {
				// Fetch distribution-independent indexes
				for _, pathPattern := range indexPaths {
					path := fmt.Sprintf(pathPattern, release)
					fullPath := fmt.Sprintf("/%s/%s", b.Name, path)

					// Don't log 404s during startup
					oldVerbose := p.verboseLogging
					p.verboseLogging = false
					_, _ = p.manager.Fetch(fullPath) // Ignore errors
					p.verboseLogging = oldVerbose
				}

				// Fetch architecture-specific indexes
				for arch := range p.architectures {
					for _, pathPattern := range archPaths {
						path := fmt.Sprintf(pathPattern, release, arch)
						fullPath := fmt.Sprintf("/%s/%s", b.Name, path)
						_, _ = p.manager.Fetch(fullPath) // Ignore errors
					}
				}
			}
		}(backend)
	}

	// Wait for all prefetch operations with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		elapsed := time.Since(startTime)
		log.Printf("Initial cache warm-up completed in %v", elapsed)
	case <-time.After(60 * time.Second):
		log.Printf("Initial cache warm-up timed out after 60s")
	case <-ctx.Done():
		log.Printf("Initial cache warm-up cancelled")
	}

	p.startupDone = true
}
