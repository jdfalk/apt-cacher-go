package cache

import (
	"container/list"
	"sync"
	"time"
)

// LRUItem represents an item in the LRU cache
type lruItem struct {
	Key         string
	Size        int64
	LastAccess  time.Time
	AccessCount int
}

// LRUCache implements a simple LRU cache
type LRUCache struct {
	mutex   sync.Mutex
	maxSize int
	items   map[string]*list.Element
	lruList *list.List
}

// NewLRUCache creates a new LRU cache
func NewLRUCache(maxSize int) *LRUCache {
	return &LRUCache{
		maxSize: maxSize,
		items:   make(map[string]*list.Element),
		lruList: list.New(),
	}
}

// Add adds or updates an item in the LRU cache
func (c *LRUCache) Add(key string, size int64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Check if already exists
	if element, exists := c.items[key]; exists {
		// Update existing item
		item := element.Value.(*lruItem)
		item.LastAccess = time.Now()
		item.AccessCount++
		item.Size = size
		// Move to front
		c.lruList.MoveToFront(element)
		return
	}

	// Create new item
	item := &lruItem{
		Key:         key,
		Size:        size,
		LastAccess:  time.Now(),
		AccessCount: 1,
	}

	// Add to LRU list and map
	element := c.lruList.PushFront(item)
	c.items[key] = element

	// Trim the cache if it exceeds the max size
	if c.lruList.Len() > c.maxSize {
		c.removeLRU()
	}
}

// Get returns whether an item exists in the cache and updates its position
func (c *LRUCache) Get(key string) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	element, exists := c.items[key]
	if !exists {
		return false
	}

	// Update last accessed time and count
	item := element.Value.(*lruItem)
	item.LastAccess = time.Now()
	item.AccessCount++

	// Move to front
	c.lruList.MoveToFront(element)

	return true
}

// Remove removes an item from the LRU cache
func (c *LRUCache) Remove(key string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if element, exists := c.items[key]; exists {
		c.lruList.Remove(element)
		delete(c.items, key)
	}
}

// removeLRU removes the least recently used item
func (c *LRUCache) removeLRU() {
	if element := c.lruList.Back(); element != nil {
		item := element.Value.(*lruItem)
		c.lruList.Remove(element)
		delete(c.items, item.Key)
	}
}

// GetLRUItems returns the least recently used items
func (c *LRUCache) GetLRUItems(count int) []lruItem {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	result := make([]lruItem, 0, count)
	element := c.lruList.Back()

	for i := 0; i < count && element != nil; i++ {
		item := element.Value.(*lruItem)
		result = append(result, *item)
		element = element.Prev()
	}

	return result
}
