package main

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewCache verifies cache initialization
func TestNewCache(t *testing.T) {
	cache := NewCache(100, 5*time.Minute)
	require.NotNil(t, cache)
	assert.Equal(t, 0, cache.Len())
	assert.Equal(t, 100, cache.capacity)
	assert.Equal(t, 5*time.Minute, cache.ttl)
}

// TestCacheSetAndGet tests basic set/get operations
func TestCacheSetAndGet(t *testing.T) {
	cache := NewCache(100, 5*time.Minute)

	// Set a value
	cache.Set("key1", "value1")
	assert.Equal(t, 1, cache.Len())

	// Get the value
	value, found := cache.Get("key1")
	assert.True(t, found)
	assert.Equal(t, "value1", value)

	// Get non-existent key
	_, found = cache.Get("nonexistent")
	assert.False(t, found)
}

// TestCacheUpdate tests updating existing keys
func TestCacheUpdate(t *testing.T) {
	cache := NewCache(100, 5*time.Minute)

	cache.Set("key1", "value1")
	assert.Equal(t, 1, cache.Len())

	// Update the same key
	cache.Set("key1", "value2")
	assert.Equal(t, 1, cache.Len()) // Should still be 1

	value, found := cache.Get("key1")
	assert.True(t, found)
	assert.Equal(t, "value2", value)
}

// TestCacheLRUEviction tests LRU eviction when capacity is reached
func TestCacheLRUEviction(t *testing.T) {
	cache := NewCache(3, 5*time.Minute)

	// Fill cache to capacity
	cache.Set("key1", "value1")
	cache.Set("key2", "value2")
	cache.Set("key3", "value3")
	assert.Equal(t, 3, cache.Len())

	// Add one more - should evict key1 (oldest)
	cache.Set("key4", "value4")
	assert.Equal(t, 3, cache.Len())

	// key1 should be evicted
	_, found := cache.Get("key1")
	assert.False(t, found)

	// Others should still exist
	_, found = cache.Get("key2")
	assert.True(t, found)
	_, found = cache.Get("key3")
	assert.True(t, found)
	_, found = cache.Get("key4")
	assert.True(t, found)
}

// TestCacheLRUOrdering tests that access updates LRU order
func TestCacheLRUOrdering(t *testing.T) {
	cache := NewCache(3, 5*time.Minute)

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")
	cache.Set("key3", "value3")

	// Access key1 to make it recently used
	cache.Get("key1")

	// Add key4 - should evict key2 (now oldest), not key1
	cache.Set("key4", "value4")

	// key2 should be evicted
	_, found := cache.Get("key2")
	assert.False(t, found)

	// key1 should still exist
	_, found = cache.Get("key1")
	assert.True(t, found)
}

// TestCacheTTLExpiration tests that entries expire after TTL
func TestCacheTTLExpiration(t *testing.T) {
	cache := NewCache(100, 100*time.Millisecond)

	cache.Set("key1", "value1")

	// Should be accessible immediately
	value, found := cache.Get("key1")
	assert.True(t, found)
	assert.Equal(t, "value1", value)

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	// Should be expired now
	_, found = cache.Get("key1")
	assert.False(t, found)

	// Cache should be empty after accessing expired entry
	assert.Equal(t, 0, cache.Len())
}

// TestCacheTTLRefresh tests that updating an entry refreshes its TTL
func TestCacheTTLRefresh(t *testing.T) {
	cache := NewCache(100, 100*time.Millisecond)

	cache.Set("key1", "value1")

	// Wait 60ms (before expiration)
	time.Sleep(60 * time.Millisecond)

	// Update the entry to refresh TTL
	cache.Set("key1", "value1_updated")

	// Wait another 60ms (total 120ms from first Set, but only 60ms from update)
	time.Sleep(60 * time.Millisecond)

	// Should still be accessible because TTL was refreshed
	value, found := cache.Get("key1")
	assert.True(t, found)
	assert.Equal(t, "value1_updated", value)
}

// TestCacheDelete tests deleting entries
func TestCacheDelete(t *testing.T) {
	cache := NewCache(100, 5*time.Minute)

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")
	assert.Equal(t, 2, cache.Len())

	cache.Delete("key1")
	assert.Equal(t, 1, cache.Len())

	_, found := cache.Get("key1")
	assert.False(t, found)

	_, found = cache.Get("key2")
	assert.True(t, found)

	// Delete non-existent key should not panic
	cache.Delete("nonexistent")
	assert.Equal(t, 1, cache.Len())
}

// TestCacheClear tests clearing all entries
func TestCacheClear(t *testing.T) {
	cache := NewCache(100, 5*time.Minute)

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")
	cache.Set("key3", "value3")
	assert.Equal(t, 3, cache.Len())

	cache.Clear()
	assert.Equal(t, 0, cache.Len())

	_, found := cache.Get("key1")
	assert.False(t, found)
}

// TestCacheEvictExpired tests manual eviction of expired entries
func TestCacheEvictExpired(t *testing.T) {
	cache := NewCache(100, 100*time.Millisecond)

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")
	cache.Set("key3", "value3")
	assert.Equal(t, 3, cache.Len())

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	// Evict expired entries
	evicted := cache.EvictExpired()
	assert.Equal(t, 3, evicted)
	assert.Equal(t, 0, cache.Len())
}

// TestCacheEvictExpiredPartial tests evicting only some expired entries
func TestCacheEvictExpiredPartial(t *testing.T) {
	cache := NewCache(100, 100*time.Millisecond)

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")

	// Wait 60ms
	time.Sleep(60 * time.Millisecond)

	// Add key3 with fresh TTL
	cache.Set("key3", "value3")

	// Wait another 60ms (key1 and key2 expired, key3 still valid)
	time.Sleep(60 * time.Millisecond)

	evicted := cache.EvictExpired()
	assert.Equal(t, 2, evicted)
	assert.Equal(t, 1, cache.Len())

	// key3 should still be accessible
	_, found := cache.Get("key3")
	assert.True(t, found)
}

// TestCacheStats tests cache statistics
func TestCacheStats(t *testing.T) {
	cache := NewCache(100, 100*time.Millisecond)

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")

	stats := cache.Stats()
	assert.Equal(t, 2, stats.Size)
	assert.Equal(t, 100, stats.Capacity)
	assert.Equal(t, 0, stats.Expired)

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	stats = cache.Stats()
	assert.Equal(t, 2, stats.Size)
	assert.Equal(t, 2, stats.Expired)
}

// TestCacheConcurrency tests concurrent access to the cache
func TestCacheConcurrency(t *testing.T) {
	cache := NewCache(1000, 5*time.Minute)

	var wg sync.WaitGroup
	iterations := 1000
	goroutines := 10

	// Concurrent writes
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				key := "key_" + strconv.Itoa(id) + "_" + strconv.Itoa(j)
				value := "value_" + strconv.Itoa(j)
				cache.Set(key, value)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				key := "key_" + strconv.Itoa(id) + "_" + strconv.Itoa(j)
				cache.Get(key)
			}
		}(i)
	}

	// Concurrent deletes
	for i := 0; i < goroutines/2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations/2; j++ {
				key := "key_" + strconv.Itoa(id) + "_" + strconv.Itoa(j)
				cache.Delete(key)
			}
		}(i)
	}

	wg.Wait()

	// Cache should be accessible and not panic
	stats := cache.Stats()
	assert.GreaterOrEqual(t, stats.Size, 0)
	assert.LessOrEqual(t, stats.Size, cache.capacity)
}

// TestCacheStressLRU tests LRU behavior under stress
func TestCacheStressLRU(t *testing.T) {
	cache := NewCache(10, 5*time.Minute)

	// Add 100 items (will cause many evictions)
	for i := 0; i < 100; i++ {
		key := "key_" + strconv.Itoa(i)
		value := "value_" + strconv.Itoa(i)
		cache.Set(key, value)
	}

	// Should only have 10 items (capacity)
	assert.Equal(t, 10, cache.Len())

	// The last 10 items should be present
	for i := 90; i < 100; i++ {
		key := "key_" + strconv.Itoa(i)
		_, found := cache.Get(key)
		assert.True(t, found, "Expected %s to be in cache", key)
	}

	// Earlier items should be evicted
	for i := 0; i < 90; i++ {
		key := "key_" + strconv.Itoa(i)
		_, found := cache.Get(key)
		assert.False(t, found, "Expected %s to be evicted", key)
	}
}

// TestCacheZeroCapacity tests cache with zero capacity
func TestCacheZeroCapacity(t *testing.T) {
	cache := NewCache(0, 5*time.Minute)

	cache.Set("key1", "value1")
	
	// Should not store anything with zero capacity
	assert.Equal(t, 0, cache.Len())
	
	_, found := cache.Get("key1")
	assert.False(t, found)
}
