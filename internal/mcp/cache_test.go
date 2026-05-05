package mcp

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
)

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	return db
}

func makeToolRequest(toolName string, args map[string]any) mcp.CallToolRequest {
	var req mcp.CallToolRequest
	req.Params.Name = toolName
	req.Params.Arguments = args
	return req
}

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
	c.Close() // Stop the goroutine; this must not panic.
	// Existing Get calls should still work after Close.
	val, ok := c.Get("k")
	if !ok || val != "v" {
		t.Fatal("expected cache to still work after Close")
	}
}

func TestCache_CloseIsIdempotent(t *testing.T) {
	c := NewCache(5 * time.Minute)
	c.Close()
	c.Close()
}

func TestCache_EvictsSoonestExpiringEntryOnOverflow(t *testing.T) {
	c := NewCache(time.Hour)
	now := time.Now()
	for i := 0; i < maxCacheEntries; i++ {
		key := fmt.Sprintf("key-%d", i)
		c.entries[key] = entry{
			value:     key,
			expiresAt: now.Add(time.Duration(i+1) * time.Minute),
		}
	}

	c.mu.Lock()
	c.entries["overflow"] = entry{value: "overflow", expiresAt: now.Add(2 * time.Hour)}
	c.evictOneLocked()
	_, hasFirst := c.entries["key-0"]
	_, hasOverflow := c.entries["overflow"]
	entryCount := len(c.entries)
	c.mu.Unlock()

	if hasFirst {
		t.Fatal("expected soonest-expiring entry to be evicted")
	}
	if !hasOverflow {
		t.Fatal("expected newest overflow entry to remain in cache")
	}
	if entryCount != maxCacheEntries {
		t.Fatalf("entry count = %d, want %d", entryCount, maxCacheEntries)
	}
}

func TestGetNode_CacheHit(t *testing.T) {
	db := openTestDB(t)
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}

	node := model.Node{
		Namespace:     ctxns.DefaultNamespace,
		QualifiedName: "pkg.CacheHitFunc",
		Name:          "CacheHitFunc",
		Kind:          model.NodeKindFunction,
		FilePath:      "pkg/cache_hit.go",
		Language:      "go",
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}

	cache := NewCache(5 * time.Minute)
	h := &handlers{deps: &Deps{Store: st, DB: db}, cache: cache}
	req := makeToolRequest("get_node", map[string]any{"qualified_name": "pkg.CacheHitFunc"})

	res1, err := h.getNode(context.Background(), req)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}
	if res1.IsError {
		t.Fatal("first call: unexpected error result")
	}

	if err := db.Unscoped().Delete(&model.Node{}, node.ID).Error; err != nil {
		t.Fatal(err)
	}

	res2, err := h.getNode(context.Background(), req)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}
	if res2.IsError {
		t.Fatal("second call (cache hit): unexpected error result")
	}
}

func TestGetNode_NoCache(t *testing.T) {
	db := openTestDB(t)
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}

	h := &handlers{deps: &Deps{Store: st, DB: db}, cache: nil}
	req := makeToolRequest("get_node", map[string]any{"qualified_name": "pkg.NotExist"})

	res, err := h.getNode(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error response for missing node without cache")
	}
}

func TestBuildOrUpdateGraph_FlushesCache(t *testing.T) {
	deps := setupTestDeps(t)
	cache := NewCache(5 * time.Minute)
	cache.Set(`get_node:{"qualified_name":"pkg.Foo"}`, `{"id":1}`)
	h := &handlers{deps: deps, cache: cache}

	dir := t.TempDir()
	goFile := fmt.Sprintf("%s/test.go", dir)
	if err := os.WriteFile(goFile, []byte("package main\n\nfunc TestFunc() {\n\treturn\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := makeToolRequest("build_or_update_graph", map[string]any{
		"path":         dir,
		"full_rebuild": true,
		"postprocess":  "none",
	})

	_, err := h.buildOrUpdateGraph(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := cache.Get(`get_node:{"qualified_name":"pkg.Foo"}`); ok {
		t.Fatal("expected cache to be flushed after buildOrUpdateGraph")
	}
}

func TestParseProject_FlushesCache(t *testing.T) {
	deps := setupTestDeps(t)
	cache := NewCache(5 * time.Minute)
	cache.Set(`get_node:{"qualified_name":"pkg.Foo"}`, `{"id":1}`)
	h := &handlers{deps: deps, cache: cache}

	dir := t.TempDir()
	if err := os.WriteFile(fmt.Sprintf("%s/test.go", dir), []byte("package main\n\nfunc TestFunc() {\n\treturn\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := makeToolRequest("parse_project", map[string]any{"path": dir})

	_, err := h.parseProject(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := cache.Get(`get_node:{"qualified_name":"pkg.Foo"}`); ok {
		t.Fatal("expected cache to be flushed after parseProject")
	}
}

func TestCachedExecute_UsesContextNamespaceForNamespaceFallback(t *testing.T) {
	deps := setupTestDeps(t)
	cache := NewCache(5 * time.Minute)
	h := &handlers{deps: deps, cache: cache}
	ctx := ctxns.WithNamespace(context.Background(), "alpha")

	callCount := 0
	result, err := h.cachedExecute(ctx, "test:", map[string]any{"namespace": ""}, func() (string, error) {
		callCount++
		return "alpha-result", nil
	})
	if err != nil || result != "alpha-result" {
		t.Fatalf("first call = (%q, %v), want alpha-result, nil", result, err)
	}

	if _, ok := cache.Get(`test:{"namespace":"alpha"}`); !ok {
		t.Fatal("expected effective namespace to be used as cache key namespace")
	}

	result, err = h.cachedExecute(ctx, "test:", map[string]any{"namespace": ""}, func() (string, error) {
		callCount++
		return "miss", nil
	})
	if err != nil || result != "alpha-result" {
		t.Fatalf("second call = (%q, %v), want alpha-result, nil", result, err)
	}
	if callCount != 1 {
		t.Fatalf("fn called %d times, want 1", callCount)
	}
}

func TestRunPostprocess_FlushesCache(t *testing.T) {
	deps := setupTestDeps(t)
	cache := NewCache(5 * time.Minute)
	cache.Set(`get_node:{"qualified_name":"pkg.Foo"}`, `{"id":1}`)
	h := &handlers{deps: deps, cache: cache}
	req := makeToolRequest("run_postprocess", map[string]any{
		"flows":       false,
		"communities": false,
		"fts":         false,
	})

	_, err := h.runPostprocess(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := cache.Get(`get_node:{"qualified_name":"pkg.Foo"}`); ok {
		t.Fatal("expected cache to be flushed after runPostprocess")
	}
}
