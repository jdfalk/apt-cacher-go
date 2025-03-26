package backend

import (
	"context"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jdfalk/apt-cacher-go/internal/parser"
)

// Prefetcher manages background prefetching of packages
type Prefetcher struct {
	manager   *Manager
	active    sync.Map // Track active prefetch operations
	maxActive int
	// Add a WaitGroup to track active goroutines
	wg sync.WaitGroup
}

// NewPrefetcher creates a new prefetcher
func NewPrefetcher(manager *Manager, maxActive int) *Prefetcher {
	return &Prefetcher{
		manager:   manager,
		maxActive: maxActive,
	}
}

// ProcessIndexFile analyzes an index file and prefetches popular packages
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

	// Limit to most popular packages (could be more sophisticated with popularity data)
	if len(urls) > 20 {
		urls = urls[:20]
	}

	// Count active prefetch operations - use a more reliable approach
	activeCount := 0
	p.active.Range(func(_, _ interface{}) bool {
		activeCount++
		return activeCount < p.maxActive
	})

	// Skip if too many active prefetch operations
	if activeCount >= p.maxActive {
		log.Printf("Skipping prefetch, too many active operations: %d", activeCount)
		return
	}

	// Set a timeout for the entire prefetch operation
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)

	// Prefetch packages in background
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer cancel() // Ensure the context is always canceled

		// Track when this prefetch operation started
		startTime := time.Now()
		prefetchCount := 0

		for _, url := range urls {
			// Check if context is done (timeout or cancellation)
			if ctx.Err() != nil {
				log.Printf("Prefetch operation canceled or timed out after %v", time.Since(startTime))
				break
			}

			// Skip if already being prefetched
			if _, exists := p.active.LoadOrStore(url, startTime); exists {
				continue
			}

			// Always clean up at the end of this iteration
			defer p.active.Delete(url)

			// Add a timeout for each individual prefetch
			fetchCtx, fetchCancel := context.WithTimeout(ctx, 30*time.Second)

			log.Printf("Prefetching %s", url)
			prefetchCount++

			// Use a channel to track completion with timeout
			done := make(chan struct{})
			var fetchErr error

			go func() {
				_, fetchErr = p.manager.Fetch(url)
				close(done)
			}()

			// Wait for either completion or timeout
			select {
			case <-done:
				if fetchErr != nil {
					log.Printf("Error prefetching %s: %v", url, fetchErr)
				}
			case <-fetchCtx.Done():
				log.Printf("Prefetch timed out for %s", url)
			}

			// Clean up the context
			fetchCancel()

			// Remove from active map after processing (whether successful or not)
			p.active.Delete(url)
		}

		log.Printf("Prefetch operation completed: processed %d/%d URLs in %v",
			prefetchCount, len(urls), time.Since(startTime))

		// Do a final cleanup of any items lingering too long (over 5 minutes)
		p.cleanupStalePrefetches(5 * time.Minute)
	}()
}

// cleanupStalePrefetches removes prefetch operations that have been running too long
func (p *Prefetcher) cleanupStalePrefetches(maxAge time.Duration) {
	now := time.Now()
	cleaned := 0

	p.active.Range(func(key, value interface{}) bool {
		// Check if the start time is too old
		if startTime, ok := value.(time.Time); ok {
			if now.Sub(startTime) > maxAge {
				p.active.Delete(key)
				cleaned++
				log.Printf("Cleaned up stale prefetch: %v (age: %v)", key, now.Sub(startTime))
			}
		} else {
			// If the value isn't a time.Time for some reason, clean it up
			p.active.Delete(key)
			cleaned++
		}
		return true
	})

	if cleaned > 0 {
		log.Printf("Cleaned up %d stale prefetch operations", cleaned)
	}
}

// Shutdown waits for all prefetch operations to complete
func (p *Prefetcher) Shutdown() {
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
