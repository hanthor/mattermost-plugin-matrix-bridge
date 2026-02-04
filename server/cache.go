package main

import (
	"container/list"
	"sync"
	"time"
)

// CacheEntry represents a single cached item with expiration
type cacheEntry struct {
	key       string
	value     string
	expiresAt time.Time
}

// Cache is a thread-safe LRU cache with TTL support
type Cache struct {
	mu       sync.RWMutex
	capacity int
	ttl      time.Duration
	items    map[string]*list.Element
	lru      *list.List
}

// NewCache creates a new LRU cache with the specified capacity and TTL
func NewCache(capacity int, ttl time.Duration) *Cache {
	return &Cache{
		capacity: capacity,
		ttl:      ttl,
		items:    make(map[string]*list.Element),
		lru:      list.New(),
	}
}

// Get retrieves a value from the cache if it exists and hasn't expired
func (c *Cache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, exists := c.items[key]
	if !exists {
		return "", false
	}

	entry := elem.Value.(*cacheEntry)

	// Check if expired
	if time.Now().After(entry.expiresAt) {
		c.removeElement(elem)
		return "", false
	}

	// Move to front (most recently used)
	c.lru.MoveToFront(elem)
	return entry.value, true
}

// Set adds or updates a value in the cache
func (c *Cache) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Don't store anything if capacity is zero
	if c.capacity <= 0 {
		return
	}

	// Update existing entry
	if elem, exists := c.items[key]; exists {
		entry := elem.Value.(*cacheEntry)
		entry.value = value
		entry.expiresAt = time.Now().Add(c.ttl)
		c.lru.MoveToFront(elem)
		return
	}

	// Evict oldest if at capacity
	if c.lru.Len() >= c.capacity {
		oldest := c.lru.Back()
		if oldest != nil {
			c.removeElement(oldest)
		}
	}

	// Add new entry
	entry := &cacheEntry{
		key:       key,
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
	elem := c.lru.PushFront(entry)
	c.items[key] = elem
}

// Delete removes a key from the cache
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, exists := c.items[key]; exists {
		c.removeElement(elem)
	}
}

// Clear removes all entries from the cache
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*list.Element)
	c.lru = list.New()
}

// Len returns the current number of items in the cache
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lru.Len()
}

// removeElement removes an element from both the list and map (caller must hold lock)
func (c *Cache) removeElement(elem *list.Element) {
	entry := elem.Value.(*cacheEntry)
	delete(c.items, entry.key)
	c.lru.Remove(elem)
}

// EvictExpired removes all expired entries from the cache
func (c *Cache) EvictExpired() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	evicted := 0

	// Walk the list and remove expired entries
	var next *list.Element
	for elem := c.lru.Front(); elem != nil; elem = next {
		next = elem.Next()
		entry := elem.Value.(*cacheEntry)
		if now.After(entry.expiresAt) {
			c.removeElement(elem)
			evicted++
		}
	}

	return evicted
}

// Stats returns cache statistics
func (c *Cache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := CacheStats{
		Size:     c.lru.Len(),
		Capacity: c.capacity,
	}

	// Count expired entries
	now := time.Now()
	for elem := c.lru.Front(); elem != nil; elem = elem.Next() {
		entry := elem.Value.(*cacheEntry)
		if now.After(entry.expiresAt) {
			stats.Expired++
		}
	}

	return stats
}

// CacheStats holds cache statistics
type CacheStats struct {
	Size     int
	Capacity int
	Expired  int
}
