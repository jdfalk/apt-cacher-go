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
	manager       *Manager
	active        sync.Map // Track active prefetch operations
	maxActive     int
	cleanupTick   *time.Ticker
	wg            sync.WaitGroup
	stopCh        chan struct{}
	architectures map[string]bool // Add this field
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
	p.wg.Add(1)
	defer p.wg.Done()

	for {
		select {
		case <-p.cleanupTick.C:
			p.cleanupStalePrefetches(2 * time.Minute)
		case <-p.stopCh:
			p.cleanupTick.Stop()
			return
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

	// Count active prefetch operations
	activeCount := 0
	p.active.Range(func(_, _ interface{}) bool {
		activeCount++
		return true
	})

	// Skip if too many active prefetch operations
	if activeCount >= p.maxActive {
		log.Printf("Skipping prefetch, too many active operations: %d", activeCount)
		return
	}

	// Set a timeout for the entire prefetch operation
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)

	// Prefetch packages in background
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer cancel()

		startTime := time.Now()
		prefetchCount := 0
		completedCount := 0

		for _, url := range urls {
			// Check if context is done
			if ctx.Err() != nil {
				break
			}

			// Check if we have too many active operations now
			currentActiveCount := 0
			p.active.Range(func(_, _ interface{}) bool {
				currentActiveCount++
				return true
			})

			if currentActiveCount >= p.maxActive {
				log.Printf("Reached max active operations during prefetch: %d", currentActiveCount)
				break
			}

			// Create operation tracking
			op := &PrefetchOperation{
				URL:       url,
				StartTime: time.Now(),
				Done:      make(chan struct{}),
			}

			// Check if already being prefetched
			if _, exists := p.active.LoadOrStore(url, op); exists {
				continue
			}

			prefetchCount++
			log.Printf("Prefetching %s", url)

			// Fetch with timeout
			go func(operation *PrefetchOperation, u string) {
				_, fetchCancel := context.WithTimeout(ctx, 30*time.Second)
				defer fetchCancel()
				defer close(operation.Done)
				defer p.active.Delete(u)

				// Fetch the file
				_, err := p.manager.Fetch(u)
				if err != nil {
					if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "not found") {
						// 404 errors are expected for some files - not a real error
						operation.Result = "not_found"
						log.Printf("Failed to prefetch %s: backend returned non-OK status: 404", u)
					} else {
						operation.Result = "error"
						log.Printf("Error prefetching %s: %v", u, err)
					}
				} else {
					operation.Result = "success"
				}

				// Clean up this operation
				p.active.Delete(u)
				completedCount++
			}(op, url)

			// Avoid hammering the backend
			select {
			case <-time.After(100 * time.Millisecond):
				// Rate limiting
			case <-ctx.Done():
				break
			}
		}

		// Wait for all operations to complete or time out
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer waitCancel()

		activeOps := make([]*PrefetchOperation, 0)
		p.active.Range(func(k, v interface{}) bool {
			if op, ok := v.(*PrefetchOperation); ok {
				activeOps = append(activeOps, op)
			}
			return true
		})

		// Wait for each operation
		for _, op := range activeOps {
			select {
			case <-op.Done:
				// Operation completed
			case <-waitCtx.Done():
				// Timeout waiting for operations
				log.Printf("Timed out waiting for prefetch operations to complete")
				break
			}
		}

		// Final cleanup
		p.cleanupStalePrefetches(10 * time.Second)

		log.Printf("Prefetch operation completed: processed %d/%d URLs in %v",
			completedCount, prefetchCount, time.Since(startTime))
	}()
}

// cleanupStalePrefetches removes prefetch operations that have been running too long
func (p *Prefetcher) cleanupStalePrefetches(maxAge time.Duration) {
	now := time.Now()
	cleaned := 0

	p.active.Range(func(key, value interface{}) bool {
		if op, ok := value.(*PrefetchOperation); ok {
			if now.Sub(op.StartTime) > maxAge {
				p.active.Delete(key)
				cleaned++
				log.Printf("Cleaned up stale prefetch: %v (age: %v)", key, now.Sub(op.StartTime))

				// Close the done channel if it hasn't been closed yet
				select {
				case <-op.Done:
					// Already closed
				default:
					close(op.Done)
				}
			}
		} else {
			// Old format data in the map, just clean it up
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
