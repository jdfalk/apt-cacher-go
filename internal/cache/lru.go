package cache

import (
	"container/list"
	"sync"
)

// LRUItem represents an item in the LRU cache
type lruItem struct {
	Key   string
	Value int64 // Size of the item for our use case
	Hits  int   // Track number of hits for popularity metrics
}

// LRUCache implements a thread-safe LRU cache
type LRUCache struct {
	capacity  int
	items     map[string]*list.Element
	evictList *list.List
	mutex     sync.RWMutex
}

// NewLRUCache creates a new LRU cache with the specified capacity
func NewLRUCache(capacity int) *LRUCache {
	return &LRUCache{
		capacity:  capacity,
		items:     make(map[string]*list.Element),
		evictList: list.New(),
	}
}

// Add adds an item to the cache or updates its position if it already exists
func (c *LRUCache) Add(key string, value int64) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Check if the item already exists
	if element, exists := c.items[key]; exists {
		// Move to front (most recently used)
		c.evictList.MoveToFront(element)
		// Update the item's value and increment hits
		item := element.Value.(*lruItem)
		item.Value = value
		item.Hits++
		return
	}

	// Add the new item
	item := &lruItem{
		Key:   key,
		Value: value,
		Hits:  1,
	}
	element := c.evictList.PushFront(item)
	c.items[key] = element

	// Evict items if we're over capacity
	c.evictIfNeeded()
}

// Get checks if an item exists and marks it as recently used
func (c *LRUCache) Get(key string) bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	element, exists := c.items[key]
	if !exists {
		return false
	}

	// Move to front and increment hits
	c.evictList.MoveToFront(element)
	item := element.Value.(*lruItem)
	item.Hits++
	return true
}

// Remove removes an item from the cache
func (c *LRUCache) Remove(key string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	if element, exists := c.items[key]; exists {
		c.evictList.Remove(element)
		delete(c.items, key)
	}
}

// GetLRUItems returns the n least recently used items
func (c *LRUCache) GetLRUItems(n int) []lruItem {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	// Start from the back (least recently used)
	result := make([]lruItem, 0, n)
	element := c.evictList.Back()

	// Iterate from back to front (least recently used to most recently used)
	for i := 0; i < n && element != nil; i++ {
		item := element.Value.(*lruItem)
		result = append(result, *item)
		element = element.Prev()
	}

	return result
}

// GetMostPopularItems returns the n most frequently accessed items
func (c *LRUCache) GetMostPopularItems(n int) []lruItem {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	// Copy all items to a slice for sorting
	allItems := make([]lruItem, 0, len(c.items))
	for _, element := range c.items {
		item := element.Value.(*lruItem)
		allItems = append(allItems, *item)
	}

	// Sort by hit count (simple insertion sort for clarity)
	for i := 1; i < len(allItems); i++ {
		j := i
		for j > 0 && allItems[j-1].Hits < allItems[j].Hits {
			allItems[j-1], allItems[j] = allItems[j], allItems[j-1]
			j--
		}
	}

	// Return top n items
	if len(allItems) < n {
		return allItems
	}
	return allItems[:n]
}

// Size returns the current number of items in the cache
func (c *LRUCache) Size() int {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	return len(c.items)
}

// evictIfNeeded removes the least recently used item if capacity is exceeded
func (c *LRUCache) evictIfNeeded() {
	// We should already hold the lock when this is called

	// Special case for zero capacity: immediately evict the item we just added
	if c.capacity == 0 {
		if c.evictList.Len() > 0 {
			element := c.evictList.Front() // Remove the most recently added item
			if element != nil {
				item := element.Value.(*lruItem)
				delete(c.items, item.Key)
				c.evictList.Remove(element)
			}
		}
		return
	}

	// Normal case: evict least recently used items until we're at or under capacity
	for c.evictList.Len() > c.capacity {
		element := c.evictList.Back()
		if element != nil {
			item := element.Value.(*lruItem)
			delete(c.items, item.Key)
			c.evictList.Remove(element)
		} else {
			break // Shouldn't happen, but prevents infinite loop just in case
		}
	}
}
