package backend

import (
	"log"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jdfalk/apt-cacher-go/internal/parser"
)

// Prefetcher manages background prefetching of packages
type Prefetcher struct {
	manager   *Manager
	active    sync.Map // Track active prefetch operations
	maxActive int
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

	// Count active prefetch operations
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

	// Prefetch packages in background
	go func() {
		for _, url := range urls {
			// Skip if already being prefetched
			if _, exists := p.active.LoadOrStore(url, true); exists {
				continue
			}

			log.Printf("Prefetching %s", url)
			_, err := p.manager.Fetch(url)
			if err != nil {
				log.Printf("Error prefetching %s: %v", url, err)
			}

			p.active.Delete(url)
		}
	}()
}
