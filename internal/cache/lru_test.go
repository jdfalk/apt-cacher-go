package cache

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// This original test is incompatible with the current LRUCache implementation
// Replacing with a better implementation using string keys and boolean returns
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

func TestLRUCacheGet(t *testing.T) {
	cache := NewLRUCache(3)

	// Add three items
	cache.Add("key1", 100)
	cache.Add("key2", 200)
	cache.Add("key3", 300)

	// Get should return false for non-existent key
	assert.False(t, cache.Get("key4"))

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

	// Remove one item
	cache.Remove("key2")

	// Should have two items left
	assert.Equal(t, 2, cache.Size())
	assert.True(t, cache.Get("key1"))
	assert.False(t, cache.Get("key2"))
	assert.True(t, cache.Get("key3"))

	// Remove a non-existent key (should do nothing)
	cache.Remove("key4")
	assert.Equal(t, 2, cache.Size())
}

func TestLRUCacheUpdateItem(t *testing.T) {
	cache := NewLRUCache(2)

	// Add two items
	cache.Add("key1", 100)
	cache.Add("key2", 200)

	// Update the first item
	cache.Add("key1", 150)

	// Both should still be present
	assert.True(t, cache.Get("key1"))
	assert.True(t, cache.Get("key2"))

	// Add a third item
	cache.Add("key3", 300)

	// key2 should be evicted (key1 was more recently used due to the update)
	assert.True(t, cache.Get("key1"))
	assert.False(t, cache.Get("key2"))
	assert.True(t, cache.Get("key3"))
}

func TestGetLRUItems(t *testing.T) {
	cache := NewLRUCache(3)

	// Add three items
	cache.Add("key1", 100)
	time.Sleep(1 * time.Millisecond)
	cache.Add("key2", 200)
	time.Sleep(1 * time.Millisecond)
	cache.Add("key3", 300)

	// Get LRU items (should be in order key1, key2, key3)
	items := cache.GetLRUItems(3)

	assert.Equal(t, 3, len(items))
	assert.Equal(t, "key1", items[0].key)
	assert.Equal(t, "key2", items[1].key)
	assert.Equal(t, "key3", items[2].key)

	// Access key1 to make it most recently used
	cache.Get("key1")

	// Get LRU items again (should be in order key2, key3, key1)
	items = cache.GetLRUItems(3)

	assert.Equal(t, 3, len(items))
	assert.Equal(t, "key2", items[0].key)
	assert.Equal(t, "key3", items[1].key)
	assert.Equal(t, "key1", items[2].key)

	// Test limit
	items = cache.GetLRUItems(2)
	assert.Equal(t, 2, len(items))
	assert.Equal(t, "key2", items[0].key)
	assert.Equal(t, "key3", items[1].key)
}

func TestGetMostPopularItems(t *testing.T) {
	cache := NewLRUCache(3)

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
	assert.Equal(t, "key1", items[0].key) // Most popular with 4 accesses
	assert.Equal(t, 4, items[0].accessCount)
	assert.Equal(t, "key2", items[1].key) // 2 accesses
	assert.Equal(t, 2, items[1].accessCount)
	assert.Equal(t, "key3", items[2].key) // 1 access
	assert.Equal(t, 1, items[2].accessCount)

	// Test limit
	items = cache.GetMostPopularItems(2)
	assert.Equal(t, 2, len(items))
	assert.Equal(t, "key1", items[0].key)
	assert.Equal(t, "key2", items[1].key)
}

func TestCapacityZero(t *testing.T) {
	cache := NewLRUCache(0)

	// Add an item - should still work, but be immediately evicted
	cache.Add("key1", 100)
	assert.Equal(t, 0, cache.Size())
	assert.False(t, cache.Get("key1"))
}
