package cache

import (
	"container/list"
	"sync"
	"time"
)

// LRUCache implements a thread-safe LRU cache for tracking file usage
type LRUCache struct {
	capacity int // Maximum number of items to keep
	items    map[string]*list.Element
	lruList  *list.List
	mutex    sync.RWMutex
}

// cacheItem represents an item in the LRU cache
type cacheItem struct {
	key          string
	size         int64
	lastAccessed time.Time
	accessCount  int
}

// NewLRUCache creates a new LRU cache with the specified capacity
func NewLRUCache(capacity int) *LRUCache {
	return &LRUCache{
		capacity: capacity,
		items:    make(map[string]*list.Element),
		lruList:  list.New(),
	}
}

// Add adds an item to the cache
func (c *LRUCache) Add(key string, size int64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Check if the item already exists
	if element, exists := c.items[key]; exists {
		// Move to front (most recently used)
		c.lruList.MoveToFront(element)
		item := element.Value.(*cacheItem)
		item.lastAccessed = time.Now()
		item.accessCount++
		item.size = size // Update size in case it changed
		return
	}

	// Create new item
	item := &cacheItem{
		key:          key,
		size:         size,
		lastAccessed: time.Now(),
		accessCount:  1,
	}

	// Add to front of list and store in map
	element := c.lruList.PushFront(item)
	c.items[key] = element

	// If over capacity, remove least recently used
	if c.lruList.Len() > c.capacity {
		c.removeLRU()
	}
}

// Get returns whether an item exists in the cache and updates its position
func (c *LRUCache) Get(key string) bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	element, exists := c.items[key]
	if !exists {
		return false
	}

	// Update last accessed time and count
	item := element.Value.(*cacheItem)
	item.lastAccessed = time.Now()
	item.accessCount++

	// Move to front
	c.lruList.MoveToFront(element)

	return true
}

// Remove removes an item from the cache
func (c *LRUCache) Remove(key string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if element, exists := c.items[key]; exists {
		c.lruList.Remove(element)
		delete(c.items, key)
	}
}

// GetLRUItems returns the least recently used items
func (c *LRUCache) GetLRUItems(count int) []cacheItem {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	if count > c.lruList.Len() {
		count = c.lruList.Len()
	}

	items := make([]cacheItem, 0, count)
	element := c.lruList.Back()

	for i := 0; i < count && element != nil; i++ {
		item := element.Value.(*cacheItem)
		items = append(items, *item)
		element = element.Prev()
	}

	return items
}

// GetMostPopularItems returns the most frequently accessed items
func (c *LRUCache) GetMostPopularItems(count int) []cacheItem {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	// Get all items
	allItems := make([]cacheItem, 0, len(c.items))
	for _, element := range c.items {
		item := element.Value.(*cacheItem)
		allItems = append(allItems, *item)
	}

	// Sort by access count (simple bubble sort for now)
	for i := 0; i < len(allItems)-1; i++ {
		for j := i + 1; j < len(allItems); j++ {
			if allItems[i].accessCount < allItems[j].accessCount {
				allItems[i], allItems[j] = allItems[j], allItems[i]
			}
		}
	}

	// Return top N
	if count > len(allItems) {
		count = len(allItems)
	}
	return allItems[:count]
}

// removeLRU removes the least recently used item
func (c *LRUCache) removeLRU() {
	if c.lruList.Len() == 0 {
		return
	}

	// Get the last element
	element := c.lruList.Back()
	if element == nil {
		return
	}

	// Remove it
	item := element.Value.(*cacheItem)
	c.lruList.Remove(element)
	delete(c.items, item.key)
}

// Size returns the number of items in the cache
func (c *LRUCache) Size() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return c.lruList.Len()
}
