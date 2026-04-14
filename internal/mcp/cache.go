package mcp

import (
	"sync"
	"time"
)

type entry struct {
	value     string
	expiresAt time.Time
}

// Cache is a simple in-memory TTL cache safe for concurrent use.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]entry
	ttl     time.Duration
	stopCh  chan struct{}
}

// NewCache creates a Cache with the given TTL and starts a background cleanup goroutine.
// Call Close() when the cache is no longer needed to stop the goroutine.
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
func (c *Cache) Set(key string, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = entry{value: value, expiresAt: time.Now().Add(c.ttl)}
}

// Flush removes all entries from the cache.
func (c *Cache) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]entry)
}

// Close stops the background cleanup goroutine.
func (c *Cache) Close() {
	close(c.stopCh)
}

// cleanup runs every 30s and removes expired entries to prevent unbounded growth.
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
