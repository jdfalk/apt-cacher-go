package cache

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLRUCacheBasic(t *testing.T) {
	cache := NewLRUCache(2)

	// Add two items
	cache.Add("key1", 100)
	cache.Add("key2", 200)

	// Check if they exist
	assert.True(t, cache.Get("key1"))
	assert.True(t, cache.Get("key2"))

	// Add a third item (should evict key1 since key2 was accessed more recently)
	cache.Add("key3", 300)

	// key1 should be evicted, key2 and key3 should exist
	assert.False(t, cache.Get("key1"), "key1 should have been evicted")
	assert.True(t, cache.Get("key2"), "key2 should still exist")
	assert.True(t, cache.Get("key3"), "key3 should exist")

	// Add a fourth item (should evict key2)
	cache.Add("key4", 400)

	// Now only key3 and key4 should exist
	assert.False(t, cache.Get("key1"), "key1 should have been evicted")
	assert.False(t, cache.Get("key2"), "key2 should have been evicted")
	assert.True(t, cache.Get("key3"), "key3 should still exist")
	assert.True(t, cache.Get("key4"), "key4 should exist")
}

func TestLRUCacheAdd(t *testing.T) {
	cache := NewLRUCache(2)

	// Add two items
	cache.Add("key1", 100)
	cache.Add("key2", 200)

	// Both should be present
	assert.Equal(t, 2, cache.Size())
	assert.True(t, cache.Get("key1"))
	assert.True(t, cache.Get("key2"))

	// Add a third item - should evict the least recently used
	cache.Add("key3", 300)

	// Size should still be 2
	assert.Equal(t, 2, cache.Size())

	// key1 should be evicted (since key2 was accessed more recently via Get)
	assert.False(t, cache.Get("key1"))
	assert.True(t, cache.Get("key2"))
	assert.True(t, cache.Get("key3"))
}

func TestLRUCacheUpdateItem(t *testing.T) {
	cache := NewLRUCache(2)

	// Add two items
	cache.Add("key1", 100)
	cache.Add("key2", 200)

	// Get key1 to make it most recently used
	cache.Get("key1")
	t.Logf("After Get(key1): %v", getLRUOrder(cache))

	// Update key2 - this makes it the most recently used item
	cache.Add("key2", 150)

	// Add a third item - this will evict the least recently used
	cache.Add("key3", 300)

	// After the assertions above, key1 is most recently used, key2 is updated
	// So when key3 is added, least recently used (key1) is evicted
	assert.False(t, cache.Get("key1"), "key1 should have been evicted")
	assert.True(t, cache.Get("key2"), "key2 should still exist")
	assert.True(t, cache.Get("key3"), "key3 should exist")
}

// Helper function to get LRU order
func getLRUOrder(cache *LRUCache) []string {
	// Front to back (most recent to least recent)
	items := cache.GetLRUItems(cache.capacity)
	keys := make([]string, len(items))
	for i, item := range items {
		keys[i] = item.Key
	}
	return keys
}

func TestLRUCacheGet(t *testing.T) {
	cache := NewLRUCache(3)

	// Add three items
	cache.Add("key1", 100)
	cache.Add("key2", 200)
	cache.Add("key3", 300)

	// Get should return false for non-existent key
	assert.False(t, cache.Get("nonexistent"))

	// Get should return true for existing key
	assert.True(t, cache.Get("key1"))

	// Add a fourth item
	cache.Add("key4", 400)

	// key2 should be evicted (key1 is now most recently used)
	assert.True(t, cache.Get("key1"))
	assert.False(t, cache.Get("key2"))
	assert.True(t, cache.Get("key3"))
	assert.True(t, cache.Get("key4"))
}

func TestLRUCacheRemove(t *testing.T) {
	cache := NewLRUCache(3)

	// Add three items
	cache.Add("key1", 100)
	cache.Add("key2", 200)
	cache.Add("key3", 300)

	assert.Equal(t, 3, cache.Size())

	// Remove one item
	cache.Remove("key2")

	// Should have two items left
	assert.Equal(t, 2, cache.Size())
	assert.True(t, cache.Get("key1"))
	assert.False(t, cache.Get("key2"))
	assert.True(t, cache.Get("key3"))

	// Remove a non-existent key (should do nothing)
	cache.Remove("nonexistent")
	assert.Equal(t, 2, cache.Size())
}

func TestGetLRUItems(t *testing.T) {
	cache := NewLRUCache(3)

	// Add three items
	cache.Add("key1", 100)
	cache.Add("key2", 200)
	cache.Add("key3", 300)

	// Get LRU items (should be in order key1, key2, key3 - where key1 is least recently used)
	items := cache.GetLRUItems(3)
	assert.Equal(t, 3, len(items))
	assert.Equal(t, "key1", items[0].Key) // Least recently used
	assert.Equal(t, "key2", items[1].Key)
	assert.Equal(t, "key3", items[2].Key) // Most recently used

	// Access key1 to make it most recently used
	cache.Get("key1")

	// Get LRU items again (should be in order key2, key3, key1 - where key2 is now least recently used)
	items = cache.GetLRUItems(3)
	assert.Equal(t, 3, len(items))
	assert.Equal(t, "key2", items[0].Key) // Least recently used
	assert.Equal(t, "key3", items[1].Key)
	assert.Equal(t, "key1", items[2].Key) // Now most recently used

	// Test limit
	items = cache.GetLRUItems(2)
	assert.Equal(t, 2, len(items))
}

func TestGetMostPopularItems(t *testing.T) {
	cache := NewLRUCache(5)

	// Add three items
	cache.Add("key1", 100)
	cache.Add("key2", 200)
	cache.Add("key3", 300)

	// Access key1 multiple times
	cache.Get("key1")
	cache.Get("key1")
	cache.Get("key1")

	// Access key2 once
	cache.Get("key2")

	// Don't access key3

	// Get most popular items
	items := cache.GetMostPopularItems(3)
	assert.Equal(t, 3, len(items))
	assert.Equal(t, "key1", items[0].Key) // Most popular with 4 accesses
	assert.Equal(t, "key2", items[1].Key) // 2 accesses
	assert.Equal(t, "key3", items[2].Key) // 1 access

	// Test limit
	items = cache.GetMostPopularItems(2)
	assert.Equal(t, 2, len(items))
}

func TestCapacityZero(t *testing.T) {
	cache := NewLRUCache(0)

	// Add an item - should still work, but be immediately evicted
	cache.Add("key1", 100)

	// Both of these should be true since capacity is 0
	assert.False(t, cache.Get("key1"), "Items in a zero-capacity cache should be immediately evicted")
	assert.Equal(t, 0, cache.Size(), "A zero-capacity cache should always have size 0")
}
