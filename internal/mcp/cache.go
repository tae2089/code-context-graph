// @index In-memory TTL cache for repeat MCP read-tool responses with background eviction.
package mcp

import (
	"sync"
	"time"
)

const maxCacheEntries = 1000

// entry stores a cached string value with its expiration time.
// @intent Stores the value of a cache entry along with its TTL expiration point.
type entry struct {
	value     string
	expiresAt time.Time
}

// Cache is a simple in-memory TTL cache safe for concurrent use.
// @intent Reuses MCP read-tool responses in memory for frequently repeated queries.
type Cache struct {
	mu        sync.RWMutex
	entries   map[string]entry
	ttl       time.Duration
	stopCh    chan struct{}
	closeOnce sync.Once
}

// NewCache creates a Cache with the given TTL and starts a background cleanup goroutine.
// Call Close() when the cache is no longer needed to stop the goroutine.
// @intent Creates a TTL-based memory cache and starts the expired entry cleanup loop.
// @param ttl The maximum duration each entry remains valid.
// @ensures The returned Cache has an empty entry map and an active cleanup goroutine.
// @sideEffect Starts a background cleanup goroutine.
func NewCache(ttl time.Duration) *Cache {
	c := &Cache{
		entries: make(map[string]entry),
		ttl:     ttl,
		stopCh:  make(chan struct{}),
	}
	go c.cleanup()
	return c
}

// Get returns the cached value and true if the key exists and has not expired.
// @intent Returns only cached responses that are still within their validity period.
// @param key The cache key to look up.
// @return Returns the value and true if the key exists and is not expired.
func (c *Cache) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	if !ok || time.Now().After(e.expiresAt) {
		return "", false
	}
	return e.value, true
}

// Set stores the value under key with the cache's configured TTL.
// @intent Stores read-tool results in the cache with the configured TTL.
// @param key The cache key to store under.
// @mutates c.entries
func (c *Cache) Set(key string, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = entry{value: value, expiresAt: time.Now().Add(c.ttl)}
	if len(c.entries) > maxCacheEntries {
		c.evictOneLocked()
	}
}

// Flush removes all entries from the cache.
// @intent Invalidates all cached read results after a graph or index update.
// @mutates c.entries
func (c *Cache) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]entry)
}

// Close stops the background cleanup goroutine.
// @intent Safely stops the cleanup goroutine when the cache is no longer used.
// @sideEffect Closes stopCh to terminate the cleanup loop.
// @mutates c.stopCh
func (c *Cache) Close() {
	c.closeOnce.Do(func() {
		close(c.stopCh)
	})
}

// @intent drop one cache entry to keep total size at or below the configured maximum.
// @requires c.mu is held for writing and c.entries is initialized.
// @mutates c.entries
func (c *Cache) evictOneLocked() {
	if len(c.entries) <= maxCacheEntries {
		return
	}
	var victimKey string
	var victimExpiry time.Time
	first := true
	for key, e := range c.entries {
		if first || e.expiresAt.Before(victimExpiry) {
			victimKey = key
			victimExpiry = e.expiresAt
			first = false
		}
	}
	if !first {
		delete(c.entries, victimKey)
	}
}

// cleanup runs every 30s and removes expired entries to prevent unbounded growth.
// @intent Periodically removes expired cache entries to limit memory usage.
// @sideEffect Performs periodic timer waits and deletes cache entries.
// @mutates c.entries
func (c *Cache) cleanup() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			now := time.Now()
			c.mu.Lock()
			for k, e := range c.entries {
				if now.After(e.expiresAt) {
					delete(c.entries, k)
				}
			}
			c.mu.Unlock()
		case <-c.stopCh:
			return
		}
	}
}
