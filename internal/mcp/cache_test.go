package mcp

import (
	"fmt"
	"testing"
	"time"
)

func TestCache_GetMiss(t *testing.T) {
	c := NewCache(5 * time.Minute)
	_, ok := c.Get("nonexistent")
	if ok {
		t.Fatal("expected miss, got hit")
	}
}

func TestCache_SetGet(t *testing.T) {
	c := NewCache(5 * time.Minute)
	c.Set("key1", "value1")
	val, ok := c.Get("key1")
	if !ok {
		t.Fatal("expected hit, got miss")
	}
	if val != "value1" {
		t.Fatalf("expected %q, got %q", "value1", val)
	}
}

func TestCache_TTLExpiry(t *testing.T) {
	c := NewCache(50 * time.Millisecond)
	c.Set("key1", "value1")
	time.Sleep(100 * time.Millisecond)
	_, ok := c.Get("key1")
	if ok {
		t.Fatal("expected TTL expiry, but got hit")
	}
}

func TestCache_Flush(t *testing.T) {
	c := NewCache(5 * time.Minute)
	c.Set("k1", "v1")
	c.Set("k2", "v2")
	c.Flush()
	if _, ok := c.Get("k1"); ok {
		t.Fatal("expected miss after Flush")
	}
	if _, ok := c.Get("k2"); ok {
		t.Fatal("expected miss after Flush")
	}
}

func TestCache_Concurrent(t *testing.T) {
	c := NewCache(5 * time.Minute)
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(i int) {
			key := fmt.Sprintf("key%d", i)
			c.Set(key, key)
			c.Get(key)
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestCache_Close(t *testing.T) {
	c := NewCache(5 * time.Minute)
	c.Set("k", "v")
	c.Close() // goroutine 종료 — panic 없어야 함
	// Close 후에도 기존 Get은 정상 동작
	val, ok := c.Get("k")
	if !ok || val != "v" {
		t.Fatal("expected cache to still work after Close")
	}
}
