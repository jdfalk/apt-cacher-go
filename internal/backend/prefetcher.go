package backend

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/metrics"
)

// PrefetcherManager defines the interface needed by the prefetcher
type PrefetcherManager interface {
	Fetch(url string) ([]byte, error)
	GetAllBackends() []*Backend
}

// Prefetcher manages background prefetching of packages
type Prefetcher struct {
	manager        PrefetcherManager // Changed from *Manager to interface
	active         sync.Map          // Track active prefetch operations
	maxActive      int
	cleanupTick    *time.Ticker
	wg             sync.WaitGroup
	stopCh         chan struct{}
	architectures  map[string]bool          // Filter by architecture
	verboseLogging bool                     // Control logging of 404s
	inProgress     int32                    // Atomic counter for tracking operations
	startupDone    bool                     // Track whether startup prefetch has completed
	failureCount   map[string]int           // Track failures by URL
	failureMutex   sync.RWMutex             // Mutex for failure map
	memoryPressure int32                    // Atomic value for memory pressure (0-100)
	metrics        *metrics.PrefetchMetrics // Track metrics
}

// PrefetchOperation tracks a single prefetch operation
type PrefetchOperation struct {
	URL       string
	StartTime time.Time
	Done      chan struct{}
	Result    string
}

// NewPrefetcher creates a new prefetcher
func NewPrefetcher(manager PrefetcherManager, maxActive int, architectures []string) *Prefetcher {
	// Create map for fast lookup
	archMap := make(map[string]bool)
	for _, arch := range architectures {
		archMap[arch] = true
	}

	p := &Prefetcher{
		manager:        manager,
		maxActive:      maxActive,
		cleanupTick:    time.NewTicker(30 * time.Second),
		stopCh:         make(chan struct{}),
		architectures:  archMap,
		failureCount:   make(map[string]int),
		metrics:        metrics.RegisterPrefetchMetrics(),
		memoryPressure: 0,
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
	// Only process if we're not under memory pressure
	if atomic.LoadInt32(&p.memoryPressure) > 85 {
		log.Printf("Skipping prefetch due to high memory pressure")
		return
	}

	// Check if we already have too many operations
	if atomic.LoadInt32(&p.inProgress) >= int32(p.maxActive) {
		log.Printf("Skipping prefetch, too many active operations: %d", p.maxActive)
		return
	}

	// Parse URLs from the index but don't store the full data
	urls := extractURLsFromIndexEfficient(data)
	if len(urls) == 0 {
		// Empty batch, just return
		return
	}

	// Filter URLs by architecture
	filteredURLs := p.filterURLsByArchitecture(urls)
	if len(filteredURLs) == 0 {
		return
	}

	// Process in smaller batches to reduce memory pressure
	batchSize := 10
	totalBatches := (len(filteredURLs) + batchSize - 1) / batchSize

	if p.verboseLogging {
		log.Printf("Processing %d URLs from %s in %d batches",
			len(filteredURLs), path, totalBatches)
	}

	// Process each batch in a separate goroutine
	for i := 0; i < len(filteredURLs); i += batchSize {
		end := i + batchSize
		if end > len(filteredURLs) {
			end = len(filteredURLs)
		}

		batch := filteredURLs[i:end]

		// Don't launch more goroutines if we're already at max
		if atomic.LoadInt32(&p.inProgress) >= int32(p.maxActive) {
			continue
		}

		go p.processBatch(repo, batch)
	}
}

// processBatch handles a batch of URLs
func (p *Prefetcher) processBatch(repo string, urls []string) {
	startTime := time.Now()
	processed := 0

	for _, url := range urls {
		// Check if we're under memory pressure (skip if memory pressure is high)
		if atomic.LoadInt32(&p.memoryPressure) > 85 {
			log.Printf("Stopping batch processing due to memory pressure")
			break
		}

		// Skip if we already have too many operations
		if atomic.LoadInt32(&p.inProgress) >= int32(p.maxActive) {
			log.Printf("Reached max active operations during prefetch: %d", p.maxActive)
			continue
		}

		// Update metrics if available
		if p.metrics != nil && p.metrics.PrefetchAttempts != nil {
			p.metrics.PrefetchAttempts.WithLabelValues(repo).Inc()
		}

		// Track as in-progress
		urlID := fmt.Sprintf("%s-%d", url, time.Now().UnixNano())

		// Skip if already in progress
		if _, loaded := p.active.LoadOrStore(urlID, time.Now()); loaded {
			continue
		}

		atomic.AddInt32(&p.inProgress, 1)

		p.wg.Add(1)
		go func(u string, id string) {
			defer p.wg.Done()
			defer p.active.Delete(id)
			defer atomic.AddInt32(&p.inProgress, -1)

			// Use context with timeout instead of FetchWithContext
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			// Use a channel to implement timeout with existing Fetch method
			resultCh := make(chan struct {
				data []byte
				err  error
			}, 1)

			go func() {
				data, err := p.manager.Fetch(u)
				resultCh <- struct {
					data []byte
					err  error
				}{data, err}
			}()

			// Wait for either result or timeout
			var err error
			select {
			case result := <-resultCh:
				err = result.err
			case <-ctx.Done():
				err = ctx.Err()
			}

			// Handle errors more efficiently
			if err != nil {
				if p.metrics != nil && p.metrics.PrefetchFailures != nil {
					reason := "error"
					if strings.Contains(err.Error(), "404") {
						reason = "404"
					}
					p.metrics.PrefetchFailures.WithLabelValues(repo, reason).Inc()
				}

				// Record the failure to track consecutive failures
				failCount := p.recordFailure(u)

				// Log with different detail levels based on failure count
				if failCount > 3 {
					// After multiple failures, reduce logging frequency
					if failCount%10 == 0 {
						log.Printf("URL %s has failed %d times", u, failCount)
					}
				} else if !strings.Contains(err.Error(), "404") || p.verboseLogging {
					// Only log non-404 errors to reduce noise
					log.Printf("Failed to prefetch %s: %v", u, err)
				}
				return
			}

			// On success, reset the failure counter
			p.resetFailureCount(u)

			// Success
			processed++
			if p.metrics != nil && p.metrics.PrefetchSuccesses != nil {
				p.metrics.PrefetchSuccesses.WithLabelValues(repo).Inc()
			}
		}(url, urlID)
	}

	// Log completion after batch is done
	go func() {
		// Wait for all fetches to complete with timeout
		done := make(chan struct{})
		go func() {
			p.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			// Normal completion
		case <-time.After(30 * time.Second):
			// Timeout - fetches took too long
		}

		duration := time.Since(startTime)
		if processed > 0 || p.verboseLogging {
			log.Printf("Prefetch operation completed: processed %d/%d URLs in %s",
				processed, len(urls), duration)
		}
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

// Fix the PrefetchOnStartup method

// PrefetchOnStartup warms the cache by fetching common index files
func (p *Prefetcher) PrefetchOnStartup(ctx context.Context) {
	if p.startupDone {
		return
	}

	log.Printf("Starting initial cache warm-up...")
	startTime := time.Now()

	// Create a derived context that can be cancelled
	prefetchCtx, cancel := context.WithCancel(ctx)
	defer cancel() // Ensure we cancel before returning

	// Create a goroutine to watch for shutdown signal
	go func() {
		select {
		case <-p.stopCh:
			log.Printf("Prefetch operation cancelled by shutdown request")
			cancel() // Cancel our context which will terminate all operations
		case <-prefetchCtx.Done():
			// Parent context was cancelled, nothing to do
		}
	}()

	// Get all configured backends
	backends := p.manager.GetAllBackends()
	if len(backends) == 0 {
		log.Printf("No backends configured for prefetch, skipping warm-up")
		p.startupDone = true
		return
	}

	log.Printf("Warming up cache for %d backends", len(backends))
	var wg sync.WaitGroup

	// Launch prefetch operations for each backend
	for _, backend := range backends {
		// Check if context was cancelled
		if prefetchCtx.Err() != nil {
			log.Printf("Prefetch startup cancelled, stopping")
			break
		}

		wg.Add(1)
		go func(b *Backend) {
			defer wg.Done()

			// Check context again before starting work
			if prefetchCtx.Err() != nil {
				return
			}

			log.Printf("Warming up cache for backend: %s (%s)", b.Name, b.BaseURL)

			// Try fetching Release files for some common distributions
			for _, release := range []string{"stable", "testing", "unstable", "jammy", "focal"} {
				// Check context before each operation
				if prefetchCtx.Err() != nil {
					log.Printf("Prefetch for backend %s cancelled", b.Name)
					return
				}

				// Log what we're attempting
				log.Printf("Fetching signature file: dists/%s/Release", release)

				// Try to fetch with cancellation awareness
				resultCh := make(chan struct {
					data []byte
					err  error
				}, 1)

				go func() {
					data, err := p.manager.Fetch(fmt.Sprintf("/%s/dists/%s/Release", b.Name, release))
					resultCh <- struct {
						data []byte
						err  error
					}{data, err}
				}()

				// Wait with context awareness
				select {
				case <-resultCh:
					// Fetched successfully or with error, continue to next
				case <-prefetchCtx.Done():
					return // Context cancelled, exit
				}
			}
		}(backend)
	}

	// Wait for prefetch operations with timeout and cancellation
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		elapsed := time.Since(startTime)
		log.Printf("Initial cache warm-up completed in %v", elapsed)
	case <-time.After(30 * time.Second):
		log.Printf("Initial cache warm-up timed out after 30s")
	case <-prefetchCtx.Done():
		log.Printf("Initial cache warm-up cancelled")
	}

	p.startupDone = true
}

// FilterURLsByArchitecture filters URLs to only include configured architectures
func (p *Prefetcher) FilterURLsByArchitecture(urls []string) []string {
	// If no architectures configured, all are enabled
	if len(p.architectures) == 0 {
		return urls
	}

	result := make([]string, 0, len(urls))
	for _, url := range urls {
		shouldInclude := false

		// Check if URL contains any enabled architecture
		for arch := range p.architectures {
			if strings.Contains(url, "/binary-"+arch+"/") ||
				strings.Contains(url, "_"+arch+".deb") {
				shouldInclude = true
				break
			}
		}

		// Also include URLs that don't have an architecture specifier
		if !containsAnyArch(url) {
			shouldInclude = true
		}

		if shouldInclude {
			result = append(result, url)
		}
	}

	return result
}

// extractURLsFromIndexEfficient parses package URLs from index data without keeping full contents in memory
func extractURLsFromIndexEfficient(data []byte) []string {
	urls := make([]string, 0, 100) // Pre-allocate for efficiency

	// Convert to string but process line by line to avoid large memory allocation
	lines := strings.Split(string(data), "\n")

	for _, line := range lines {
		// Look for Filename: entries in Packages files
		if strings.HasPrefix(line, "Filename: ") {
			path := strings.TrimPrefix(line, "Filename: ")
			path = strings.TrimSpace(path)
			if path != "" {
				urls = append(urls, path)
			}
		}
	}

	return urls
}

// SetMemoryPressure allows external components to inform the prefetcher of memory pressure
func (p *Prefetcher) SetMemoryPressure(pressurePercent int) {
	atomic.StoreInt32(&p.memoryPressure, int32(pressurePercent))

	if pressurePercent > 90 {
		// Extreme memory pressure - force cleanup
		cleaned := p.ForceCleanup()
		log.Printf("Memory pressure critical (%d%%), cleaned up %d prefetch operations",
			pressurePercent, cleaned)
	}
}

// Add these methods to properly use the mutex

// recordFailure increments the failure count for a URL
func (p *Prefetcher) recordFailure(url string) int {
	p.failureMutex.Lock()
	defer p.failureMutex.Unlock()

	p.failureCount[url]++
	return p.failureCount[url]
}

// getFailureCount returns the number of failures for a URL
func (p *Prefetcher) getFailureCount(url string) int {
	p.failureMutex.RLock()
	defer p.failureMutex.RUnlock()

	return p.failureCount[url]
}

// resetFailureCount resets the failure count for a URL
func (p *Prefetcher) resetFailureCount(url string) {
	p.failureMutex.Lock()
	defer p.failureMutex.Unlock()

	delete(p.failureCount, url)
}

// Helper method to fetch with context awareness
func (p *Prefetcher) fetchWithContext(ctx context.Context, path string) ([]byte, error) {
	// Create a channel to capture the result
	type fetchResult struct {
		data []byte
		err  error
	}

	resultCh := make(chan fetchResult, 1)

	// Launch a goroutine to do the fetch
	go func() {
		data, err := p.manager.Fetch(path)
		resultCh <- fetchResult{data, err}
	}()

	// Wait with context awareness
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		return result.data, result.err
	}
}
