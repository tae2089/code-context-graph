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
}

// NewCache creates a Cache with the given TTL and starts a background cleanup goroutine.
func NewCache(ttl time.Duration) *Cache {
	c := &Cache{
		entries: make(map[string]entry),
		ttl:     ttl,
	}
	go c.cleanup()
	return c
}

// Get returns the cached value and true if the key exists and has not expired.
func (c *Cache) Get(key string) (string, bool) {
	c.mu.RLock()
	e, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(e.expiresAt) {
		return "", false
	}
	return e.value, true
}

// Set stores the value under key with the cache's configured TTL.
func (c *Cache) Set(key string, value string) {
	c.mu.Lock()
	c.entries[key] = entry{value: value, expiresAt: time.Now().Add(c.ttl)}
	c.mu.Unlock()
}

// Flush removes all entries from the cache.
func (c *Cache) Flush() {
	c.mu.Lock()
	c.entries = make(map[string]entry)
	c.mu.Unlock()
}

// cleanup runs every 30s and removes expired entries to prevent unbounded growth.
func (c *Cache) cleanup() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		c.mu.Lock()
		for k, e := range c.entries {
			if now.After(e.expiresAt) {
				delete(c.entries, k)
			}
		}
		c.mu.Unlock()
	}
}
