package backend

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	architectures  map[string]bool // Filter by architecture
	verboseLogging bool            // Control logging of 404s
	inProgress     int32           // Atomic counter for tracking operations
	startupDone    bool            // Track whether startup prefetch has completed
	failureCount   map[string]int  // Track failures by URL
	failureMutex   sync.RWMutex    // Mutex for failure map
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
		failureCount:  make(map[string]int),
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
		log.Printf("No URLs found in index file: %s", path)
		return
	}

	// Filter URLs by architecture
	filteredURLs := p.filterByArchitecture(urls)

	// Skip if no URLs after filtering
	if len(filteredURLs) == 0 {
		log.Printf("No URLs matching configured architectures in: %s", path)
		return
	}

	// Check if we're over the limit of active operations
	activeCount := int(atomic.LoadInt32(&p.inProgress))
	if activeCount >= p.maxActive {
		log.Printf("Skipping prefetch, too many active operations: %d", activeCount)
		return
	}

	// Start timer for metrics
	startTime := time.Now()

	// Create a context with cancellation
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Process each URL
	go func() {
		processed := 0
		skipped := 0

		for _, url := range filteredURLs {
			// Check if we should stop
			select {
			case <-ctx.Done():
				return
			case <-p.stopCh:
				return
			default:
				// Continue processing
			}

			// Don't exceed max concurrent operations
			if atomic.LoadInt32(&p.inProgress) >= int32(p.maxActive) {
				skipped++
				continue
			}

			// Create a unique ID for this operation
			urlID := fmt.Sprintf("%s-%d", url, time.Now().UnixNano())

			// Mark as in progress and increment counter
			if _, loaded := p.active.LoadOrStore(urlID, time.Now()); loaded {
				// Someone else is already prefetching this URL
				skipped++
				continue
			}

			atomic.AddInt32(&p.inProgress, 1)

			// Process in a separate goroutine
			p.wg.Add(1)
			go func(u string, id string) {
				defer p.wg.Done()
				defer p.active.Delete(id)
				defer atomic.AddInt32(&p.inProgress, -1)

				// Fetch the file
				_, err := p.manager.Fetch(u)

				// Handle result with improved error handling
				if err != nil {
					if strings.Contains(err.Error(), "404") {
						if p.verboseLogging {
							log.Printf("Debug: Prefetch 404 for %s", u)
							p.failureMutex.Lock()
							p.failureCount[u]++
							p.failureMutex.Unlock()
						}
					} else {
						log.Printf("Failed to prefetch %s: %v", u, err)
					}
					return
				}

				// Success case
				processed++
			}(url, urlID)
		}

		// Report stats for this batch
		duration := time.Since(startTime)
		log.Printf("Prefetch operation completed: processed %d/%d URLs in %v",
			processed, len(filteredURLs), duration)
	}()
}

// Filter URLs by configured architectures
func (p *Prefetcher) filterURLsByArchitecture(urls []string) []string {
	// If no architectures specified, allow all
	if len(p.architectures) == 0 {
		return urls
	}

	result := make([]string, 0, len(urls))
	for _, url := range urls {
		// Simple architecture detection, could be improved
		for arch := range p.architectures {
			if strings.Contains(url, "/binary-"+arch+"/") ||
				strings.Contains(url, "_"+arch+".deb") ||
				!containsAnyArch(url) { // URLs without arch specifier should pass
				result = append(result, url)
				break
			}
		}
	}

	return result
}

// Helper to check if a URL contains any architecture specifier
func containsAnyArch(url string) bool {
	commonArchs := []string{"amd64", "i386", "arm64", "armhf", "ppc64el", "s390x", "riscv64"}
	for _, arch := range commonArchs {
		if strings.Contains(url, "/binary-"+arch+"/") || strings.Contains(url, "_"+arch+".deb") {
			return true
		}
	}
	return false
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
	if len(backends) == 0 {
		log.Printf("No backends configured for prefetch, skipping warm-up")
		p.startupDone = true
		return
	}

	log.Printf("Warming up cache for %d backends", len(backends))
	var wg sync.WaitGroup

	// Track successes
	successCount := 0
	var successMutex sync.Mutex

	// Common distributions to try
	releases := []string{"stable", "testing", "unstable", "jammy", "focal", "noble", "oracular"}

	// Only try a subset of releases for each backend to avoid excessive 404s
	for _, backend := range backends {
		wg.Add(1)
		go func(b *Backend) {
			defer wg.Done()
			localSuccesses := 0

			log.Printf("Warming up cache for backend: %s (%s)", b.Name, b.BaseURL)

			// Common index file patterns to fetch for each repository
			indexPaths := []string{
				"dists/%s/InRelease",
				"dists/%s/Release",
				"dists/%s/Release.gpg",
			}

			// Try each release for this backend, but stop after finding a valid one
			foundValidRelease := false
			for _, release := range releases {
				if foundValidRelease {
					break
				}

				// Try to fetch the Release file first to see if this distribution exists
				testPath := fmt.Sprintf("/%s/dists/%s/Release", b.Name, release)
				data, err := p.manager.Fetch(testPath)
				if err == nil && len(data) > 0 {
					// Found a valid release for this backend
					foundValidRelease = true
					log.Printf("Found valid release '%s' for backend '%s'", release, b.Name)

					// Now fetch all index files for this release
					for _, pathPattern := range indexPaths {
						path := fmt.Sprintf(pathPattern, release)
						fullPath := fmt.Sprintf("/%s/%s", b.Name, path)

						_, err := p.manager.Fetch(fullPath)
						if err == nil {
							localSuccesses++
						}
					}

					// Fetch architecture-specific files for valid architectures
					for arch := range p.architectures {
						archPaths := []string{
							fmt.Sprintf("dists/%s/main/binary-%s/Packages", release, arch),
							fmt.Sprintf("dists/%s/universe/binary-%s/Packages", release, arch),
							fmt.Sprintf("dists/%s/restricted/binary-%s/Packages", release, arch),
							fmt.Sprintf("dists/%s/multiverse/binary-%s/Packages", release, arch),
						}

						for _, archPath := range archPaths {
							fullPath := fmt.Sprintf("/%s/%s", b.Name, archPath)
							_, err := p.manager.Fetch(fullPath)
							if err == nil {
								localSuccesses++
							}
						}
					}
				}
			}

			// Update global success count
			successMutex.Lock()
			successCount += localSuccesses
			successMutex.Unlock()

			log.Printf("Finished warming up backend '%s' with %d successful fetches",
				b.Name, localSuccesses)
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
		log.Printf("Initial cache warm-up completed in %v with %d successful fetches",
			elapsed, successCount)
	case <-time.After(30 * time.Second): // Reduced timeout for faster startup
		log.Printf("Initial cache warm-up timed out after 30s")
	case <-ctx.Done():
		log.Printf("Initial cache warm-up cancelled")
	}

	p.startupDone = true
}
