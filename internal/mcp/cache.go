package mcp

import (
	"sync"
	"time"
)

// entry stores a cached string value with its expiration time.
// @intent 캐시 항목의 값과 TTL 만료 시점을 함께 보관한다.
type entry struct {
	value     string
	expiresAt time.Time
}

// Cache is a simple in-memory TTL cache safe for concurrent use.
// @intent 반복 조회가 많은 MCP 읽기 도구의 응답을 메모리에 재사용한다.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]entry
	ttl     time.Duration
	stopCh  chan struct{}
}

// NewCache creates a Cache with the given TTL and starts a background cleanup goroutine.
// Call Close() when the cache is no longer needed to stop the goroutine.
// @intent TTL 기반 메모리 캐시를 생성하고 만료 항목 정리 루프를 시작한다.
// @param ttl 각 항목이 유효한 최대 시간이다.
// @ensures 반환된 Cache는 빈 엔트리 맵과 활성 cleanup goroutine을 가진다.
// @sideEffect 백그라운드 cleanup goroutine을 시작한다.
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
// @intent 유효 기간이 남아 있는 캐시 응답만 반환한다.
// @param key 조회할 캐시 키다.
// @return 키가 존재하고 만료되지 않았을 때 값과 true를 반환한다.
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
// @intent 읽기 도구 결과를 TTL과 함께 캐시에 저장한다.
// @param key 저장할 캐시 키다.
// @mutates c.entries
func (c *Cache) Set(key string, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = entry{value: value, expiresAt: time.Now().Add(c.ttl)}
}

// Flush removes all entries from the cache.
// @intent 그래프나 인덱스 갱신 후 오래된 읽기 결과를 모두 무효화한다.
// @mutates c.entries
func (c *Cache) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]entry)
}

// Close stops the background cleanup goroutine.
// @intent 캐시 사용 종료 시 정리 goroutine을 안전하게 멈춘다.
// @sideEffect stopCh를 닫아 cleanup 루프를 종료시킨다.
// @mutates c.stopCh
func (c *Cache) Close() {
	close(c.stopCh)
}

// cleanup runs every 30s and removes expired entries to prevent unbounded growth.
// @intent 만료된 캐시 항목을 주기적으로 제거해 메모리 사용량을 제한한다.
// @sideEffect 주기적인 타이머 대기와 캐시 엔트리 삭제를 수행한다.
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
