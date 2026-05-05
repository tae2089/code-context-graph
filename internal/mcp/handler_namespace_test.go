package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	nsfs "github.com/tae2089/code-context-graph/internal/namespacefs"
	"github.com/tae2089/code-context-graph/internal/store"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
	storesearch "github.com/tae2089/code-context-graph/internal/store/search"
)

type failDeleteGraphStore struct {
	store.GraphStore
	err error
}

func (f *failDeleteGraphStore) DeleteGraph(ctx context.Context) error { return f.err }

type spySearchBackend struct {
	storesearch.Backend
	purgeCalls []string
	purgeErr   error
	lastDB     *gorm.DB
}

func (s *spySearchBackend) PurgeNamespace(ctx context.Context, db *gorm.DB) error {
	s.purgeCalls = append(s.purgeCalls, ctxns.FromContext(ctx))
	s.lastDB = db
	return s.purgeErr
}

func (s *spySearchBackend) RebuildNodes(ctx context.Context, db *gorm.DB, nodeIDs []uint) error {
	return nil
}

func namespaceHandlers(t *testing.T) (*handlers, string) {
	t.Helper()
	root := t.TempDir()
	h := &handlers{
		deps: &Deps{
			NamespaceRoot: root,
		},
	}
	return h, root
}

func TestUploadFile_Basic(t *testing.T) {
	h, root := namespaceHandlers(t)

	content := "# Hello World\nThis is a test."
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	req := makeCallToolRequest(t, map[string]any{
		"namespace": "my-service",
		"file_path": "docs/readme.md",
		"content":   encoded,
	})

	result, err := h.uploadFile(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	written, err := os.ReadFile(filepath.Join(root, "my-service", "docs", "readme.md"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(written) != content {
		t.Errorf("content mismatch: got %q, want %q", string(written), content)
	}
}

func TestUploadFile_AcceptsNamespace(t *testing.T) {
	h, root := namespaceHandlers(t)
	encoded := base64.StdEncoding.EncodeToString([]byte("hello"))

	req := makeCallToolRequest(t, map[string]any{
		"namespace": "my-service",
		"file_path": "docs/readme.md",
		"content":   encoded,
	})

	result, err := h.uploadFile(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)
	if _, err := os.Stat(filepath.Join(root, "my-service", "docs", "readme.md")); err != nil {
		t.Fatalf("file not written via namespace: %v", err)
	}
}

func TestUploadFile_PathTraversal(t *testing.T) {
	h, _ := namespaceHandlers(t)

	encoded := base64.StdEncoding.EncodeToString([]byte("malicious"))

	tests := []struct {
		name      string
		namespace string
		filePath  string
	}{
		{"dotdot in namespace", "../evil", "file.md"},
		{"dotdot in file_path", "ok", "../../etc/passwd"},
		{"absolute namespace", "/etc", "passwd"},
		{"absolute file_path", "ok", "/etc/passwd"},
		{"dot namespace", ".", "file.md"},
		{"double-dot namespace", "..", "file.md"},
		{"path-like namespace slash", "service/api", "file.md"},
		{"path-like namespace separator", "service" + string(filepath.Separator) + "api", "file.md"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := makeCallToolRequest(t, map[string]any{
				"namespace": tt.namespace,
				"file_path": tt.filePath,
				"content":   encoded,
			})
			result, err := h.uploadFile(t.Context(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertToolResultIsError(t, result)
		})
	}
}

func TestUploadFile_RejectsEmptyNamespace(t *testing.T) {
	h, _ := namespaceHandlers(t)
	encoded := base64.StdEncoding.EncodeToString([]byte("content"))

	req := makeCallToolRequest(t, map[string]any{
		"namespace": "",
		"file_path": "file.md",
		"content":   encoded,
	})
	result, err := h.uploadFile(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func TestUploadFile_InvalidBase64(t *testing.T) {
	h, _ := namespaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{
		"namespace": "my-service",
		"file_path": "file.md",
		"content":   "not-valid-base64!!!",
	})

	result, err := h.uploadFile(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func TestListNamespaces_Empty(t *testing.T) {
	h, _ := namespaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{})
	result, err := h.listNamespaces(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	text := extractText(result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if _, ok := resp["files"]; ok {
		t.Fatalf("expected files field to be omitted, got %v", resp["files"])
	}
	if items := resp["items"].([]any); len(items) != 0 {
		t.Errorf("expected empty items list, got %v", items)
	}
	if count := resp["count"].(float64); count != 0 {
		t.Errorf("expected count 0, got %v", count)
	}
	if _, ok := resp["pagination"].(map[string]any); !ok {
		t.Fatal("expected pagination object")
	}
}

func TestListNamespaces_WithData(t *testing.T) {
	h, root := namespaceHandlers(t)

	os.MkdirAll(filepath.Join(root, "service-a"), 0o755)
	os.MkdirAll(filepath.Join(root, "service-b"), 0o755)

	req := makeCallToolRequest(t, map[string]any{})
	result, err := h.listNamespaces(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := extractText(result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	namespaces := resp["namespaces"].([]any)
	if len(namespaces) != 2 {
		t.Errorf("expected 2 namespaces, got %d: %v", len(namespaces), namespaces)
	}
}

func TestListNamespaces_Pagination(t *testing.T) {
	h, root := namespaceHandlers(t)
	for _, name := range []string{"service-a", "service-b", "service-c"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	req := makeCallToolRequest(t, map[string]any{"limit": 2, "offset": 1})
	result, err := h.listNamespaces(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	var resp map[string]any
	if err := json.Unmarshal([]byte(extractText(result)), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	items := resp["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	pagination := resp["pagination"].(map[string]any)
	if pagination["offset"].(float64) != 1 || pagination["returned"].(float64) != 2 {
		t.Fatalf("unexpected pagination: %v", pagination)
	}
}

func TestListFiles_Basic(t *testing.T) {
	h, root := namespaceHandlers(t)

	nsDir := filepath.Join(root, "my-service")
	os.MkdirAll(filepath.Join(nsDir, "docs"), 0o755)
	os.WriteFile(filepath.Join(nsDir, "readme.md"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(nsDir, "docs", "api.md"), []byte("api"), 0o644)

	req := makeCallToolRequest(t, map[string]any{
		"namespace": "my-service",
	})
	result, err := h.listFiles(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	text := extractText(result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if _, ok := resp["namespaces"]; ok {
		t.Fatalf("expected namespaces field to be omitted, got %v", resp["namespaces"])
	}
	files := resp["files"].([]any)
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d: %v", len(files), files)
	}
}

func TestListFiles_Pagination(t *testing.T) {
	h, root := namespaceHandlers(t)
	nsDir := filepath.Join(root, "my-service")
	if err := os.MkdirAll(filepath.Join(nsDir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"a.md", "b.md", "docs/c.md"} {
		if err := os.WriteFile(filepath.Join(nsDir, rel), []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	req := makeCallToolRequest(t, map[string]any{"namespace": "my-service", "limit": 2, "offset": 1})
	result, err := h.listFiles(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	var resp map[string]any
	if err := json.Unmarshal([]byte(extractText(result)), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	files := resp["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	pagination := resp["pagination"].(map[string]any)
	if pagination["offset"].(float64) != 1 || pagination["returned"].(float64) != 2 {
		t.Fatalf("unexpected pagination: %v", pagination)
	}
}

func TestListNamespaceAndFiles_InvalidPagination(t *testing.T) {
	h, _ := namespaceHandlers(t)
	for name, req := range map[string]mcp.CallToolRequest{
		"namespaces limit":  makeCallToolRequest(t, map[string]any{"limit": 0}),
		"namespaces offset": makeCallToolRequest(t, map[string]any{"offset": -1}),
		"files limit":       makeCallToolRequest(t, map[string]any{"namespace": "svc", "limit": 0}),
		"files offset":      makeCallToolRequest(t, map[string]any{"namespace": "svc", "offset": -1}),
	} {
		t.Run(name, func(t *testing.T) {
			var result *mcp.CallToolResult
			var err error
			if strings.HasPrefix(name, "namespaces") {
				result, err = h.listNamespaces(t.Context(), req)
			} else {
				result, err = h.listFiles(t.Context(), req)
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertToolResultIsError(t, result)
		})
	}
}

func TestListFiles_PathTraversal(t *testing.T) {
	h, _ := namespaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{
		"namespace": "../evil",
	})
	result, err := h.listFiles(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func TestDeleteFile_Basic(t *testing.T) {
	h, root := namespaceHandlers(t)

	nsDir := filepath.Join(root, "my-service")
	os.MkdirAll(nsDir, 0o755)
	filePath := filepath.Join(nsDir, "to-delete.md")
	os.WriteFile(filePath, []byte("bye"), 0o644)

	req := makeCallToolRequest(t, map[string]any{
		"namespace": "my-service",
		"file_path": "to-delete.md",
	})
	result, err := h.deleteFile(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("file should have been deleted")
	}
}

func TestDeleteFile_NotFound(t *testing.T) {
	h, _ := namespaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{
		"namespace": "my-service",
		"file_path": "nonexistent.md",
	})
	result, err := h.deleteFile(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func TestDeleteFile_PathTraversal(t *testing.T) {
	h, _ := namespaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{
		"namespace": "ok",
		"file_path": "../../etc/passwd",
	})
	result, err := h.deleteFile(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func TestUploadFiles_Basic(t *testing.T) {
	h, root := namespaceHandlers(t)

	file1 := base64.StdEncoding.EncodeToString([]byte("package main"))
	file2 := base64.StdEncoding.EncodeToString([]byte("package util"))

	filesJSON, _ := json.Marshal([]map[string]string{
		{"namespace": "svc-a", "file_path": "main.go", "content": file1},
		{"namespace": "svc-a", "file_path": "util.go", "content": file2},
	})

	req := makeCallToolRequest(t, map[string]any{
		"files": string(filesJSON),
	})

	result, err := h.uploadFiles(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	text := extractText(result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	uploaded := resp["uploaded"].(float64)
	if uploaded != 2 {
		t.Errorf("expected 2 uploaded, got %v", uploaded)
	}

	if _, err := os.Stat(filepath.Join(root, "svc-a", "main.go")); os.IsNotExist(err) {
		t.Error("main.go not written")
	}
	if _, err := os.Stat(filepath.Join(root, "svc-a", "util.go")); os.IsNotExist(err) {
		t.Error("util.go not written")
	}
}

func TestUploadFiles_EmptyArray(t *testing.T) {
	h, _ := namespaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{
		"files": "[]",
	})

	result, err := h.uploadFiles(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func TestUploadFiles_InvalidJSON(t *testing.T) {
	h, _ := namespaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{
		"files": "not-json",
	})

	result, err := h.uploadFiles(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func TestUploadFiles_PathTraversal(t *testing.T) {
	h, _ := namespaceHandlers(t)

	file1 := base64.StdEncoding.EncodeToString([]byte("data"))
	filesJSON, _ := json.Marshal([]map[string]string{
		{"namespace": "../evil", "file_path": "file.go", "content": file1},
	})

	req := makeCallToolRequest(t, map[string]any{
		"files": string(filesJSON),
	})

	result, err := h.uploadFiles(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func TestUploadFiles_RejectsOversizedRawRequest(t *testing.T) {
	h, _ := namespaceHandlers(t)
	req := makeCallToolRequest(t, map[string]any{
		"files": strings.Repeat("x", nsfs.DefaultMaxRequestBytes+1),
	})

	result, err := h.uploadFiles(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
	if got := getTextContent(result); !strings.Contains(got, "total upload request exceeds") {
		t.Fatalf("expected total request size error, got %q", got)
	}
}

func TestUploadFiles_RejectsOversizedTotalDecodedContent(t *testing.T) {
	h, root := namespaceHandlers(t)
	first := base64.StdEncoding.EncodeToString(make([]byte, nsfs.DefaultMaxFileBytes))
	second := base64.StdEncoding.EncodeToString(make([]byte, nsfs.DefaultMaxFileBytes))
	third := base64.StdEncoding.EncodeToString([]byte("a"))
	filesJSON, _ := json.Marshal([]map[string]string{
		{"namespace": "svc-a", "file_path": "a.txt", "content": first},
		{"namespace": "svc-b", "file_path": "b.txt", "content": second},
		{"namespace": "svc-c", "file_path": "c.txt", "content": third},
	})
	req := makeCallToolRequest(t, map[string]any{"files": string(filesJSON)})

	result, err := h.uploadFiles(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
	if got := getTextContent(result); !strings.Contains(got, "total decoded upload exceeds") {
		t.Fatalf("expected total decoded size error, got %q", got)
	}
	for _, path := range []string{
		filepath.Join(root, "svc-a", "a.txt"),
		filepath.Join(root, "svc-b", "b.txt"),
		filepath.Join(root, "svc-c", "c.txt"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("expected oversized bulk upload to leave no files, path=%s stat err=%v", path, statErr)
		}
	}
}

func TestUploadFiles_MultipleNamespaces(t *testing.T) {
	h, root := namespaceHandlers(t)

	file1 := base64.StdEncoding.EncodeToString([]byte("package a"))
	file2 := base64.StdEncoding.EncodeToString([]byte("package b"))

	filesJSON, _ := json.Marshal([]map[string]string{
		{"namespace": "svc-a", "file_path": "a.go", "content": file1},
		{"namespace": "svc-b", "file_path": "b.go", "content": file2},
	})

	req := makeCallToolRequest(t, map[string]any{
		"files": string(filesJSON),
	})

	result, err := h.uploadFiles(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	if _, err := os.Stat(filepath.Join(root, "svc-a", "a.go")); os.IsNotExist(err) {
		t.Error("svc-a/a.go not written")
	}
	if _, err := os.Stat(filepath.Join(root, "svc-b", "b.go")); os.IsNotExist(err) {
		t.Error("svc-b/b.go not written")
	}
}

func TestDeleteNamespace_Basic(t *testing.T) {
	h, root := namespaceHandlers(t)

	nsDir := filepath.Join(root, "to-delete")
	os.MkdirAll(filepath.Join(nsDir, "subdir"), 0o755)
	os.WriteFile(filepath.Join(nsDir, "file.md"), []byte("content"), 0o644)
	os.WriteFile(filepath.Join(nsDir, "subdir", "nested.md"), []byte("nested"), 0o644)

	req := makeCallToolRequest(t, map[string]any{
		"namespace": "to-delete",
	})
	result, err := h.deleteNamespace(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	if _, err := os.Stat(nsDir); !os.IsNotExist(err) {
		t.Error("namespace directory should have been deleted")
	}
}

func TestDeleteNamespace_PurgesNamespaceGraphRAGAndCache(t *testing.T) {
	namespaceRoot := t.TempDir()
	ragRoot := t.TempDir()
	cache := NewCache(time.Minute)
	t.Cleanup(cache.Close)
	cache.Set("search:svc", "stale")

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}, &model.Flow{}, &model.FlowMembership{}); err != nil {
		t.Fatalf("migrate extras: %v", err)
	}
	if err := db.AutoMigrate(&model.Community{}, &model.CommunityMembership{}); err != nil {
		t.Fatalf("migrate communities: %v", err)
	}

	h := &handlers{
		deps: &Deps{
			NamespaceRoot: namespaceRoot,
			RagIndexDir:   ragRoot,
			Store:         st,
			DB:            db,
			SearchBackend: &spySearchBackend{},
		},
		cache: cache,
	}

	nsDir := filepath.Join(namespaceRoot, "svc")
	if err := os.MkdirAll(nsDir, 0o755); err != nil {
		t.Fatalf("mkdir namespace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nsDir, "file.go"), []byte("package svc"), 0o644); err != nil {
		t.Fatalf("write namespace file: %v", err)
	}

	ragIndexPath := filepath.Join(ragRoot, "svc", "doc-index.json")
	if err := os.MkdirAll(filepath.Dir(ragIndexPath), 0o755); err != nil {
		t.Fatalf("mkdir rag dir: %v", err)
	}
	if err := os.WriteFile(ragIndexPath, []byte(`{"community_count":1}`), 0o644); err != nil {
		t.Fatalf("write rag index: %v", err)
	}

	ctx := ctxns.WithNamespace(context.Background(), "svc")
	if err := st.UpsertNodes(ctx, []model.Node{{QualifiedName: "svc.Handler", Kind: model.NodeKindFunction, Name: "Handler", FilePath: "file.go", StartLine: 1, EndLine: 2, Language: "go"}}); err != nil {
		t.Fatalf("seed namespaced node: %v", err)
	}
	svcNode, err := st.GetNode(ctx, "svc.Handler")
	if err != nil || svcNode == nil {
		t.Fatalf("load namespaced node: %v", err)
	}
	if err := st.UpsertNodes(context.Background(), []model.Node{{QualifiedName: "other.Handler", Kind: model.NodeKindFunction, Name: "Handler", FilePath: "other.go", StartLine: 1, EndLine: 2, Language: "go"}}); err != nil {
		t.Fatalf("seed legacy node: %v", err)
	}

	svcCommunity := model.Community{Namespace: "svc", Key: "svc/core", Label: "svc/core", Strategy: "directory"}
	if err := db.Create(&svcCommunity).Error; err != nil {
		t.Fatalf("seed svc community: %v", err)
	}
	if err := db.Create(&model.CommunityMembership{CommunityID: svcCommunity.ID, NodeID: svcNode.ID}).Error; err != nil {
		t.Fatalf("seed svc community membership: %v", err)
	}
	svcFlow := model.Flow{Namespace: "svc", Name: "svc-flow"}
	if err := db.Create(&svcFlow).Error; err != nil {
		t.Fatalf("seed svc flow: %v", err)
	}
	if err := db.Create(&model.FlowMembership{Namespace: "svc", FlowID: svcFlow.ID, NodeID: svcNode.ID, Ordinal: 0}).Error; err != nil {
		t.Fatalf("seed svc flow membership: %v", err)
	}

	otherCommunity := model.Community{Namespace: "other", Key: "other/core", Label: "other/core", Strategy: "directory"}
	if err := db.Create(&otherCommunity).Error; err != nil {
		t.Fatalf("seed control community: %v", err)
	}
	otherFlow := model.Flow{Namespace: "other", Name: "other-flow"}
	if err := db.Create(&otherFlow).Error; err != nil {
		t.Fatalf("seed control flow: %v", err)
	}

	req := makeCallToolRequest(t, map[string]any{"namespace": "svc"})
	result, err := h.deleteNamespace(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	if _, err := os.Stat(nsDir); !os.IsNotExist(err) {
		t.Fatal("namespace directory should have been deleted")
	}
	if _, err := os.Stat(ragIndexPath); !os.IsNotExist(err) {
		t.Fatal("namespace rag index should have been deleted")
	}
	if _, ok := cache.Get("search:svc"); ok {
		t.Fatal("cache should have been flushed")
	}

	node, err := st.GetNode(ctx, "svc.Handler")
	if err != nil {
		t.Fatalf("get purged node: %v", err)
	}
	if node != nil {
		t.Fatal("namespace graph should have been purged")
	}

	otherNode, err := st.GetNode(context.Background(), "other.Handler")
	if err != nil {
		t.Fatalf("get untouched node: %v", err)
	}
	if otherNode == nil {
		t.Fatal("out-of-namespace graph should remain")
	}

	var svcCommunityCount, svcFlowCount, svcCommunityMembershipCount, svcFlowMembershipCount, otherCommunityCount, otherFlowCount int64
	if err := db.Model(&model.Community{}).Where("namespace = ?", "svc").Count(&svcCommunityCount).Error; err != nil {
		t.Fatalf("count svc communities: %v", err)
	}
	if err := db.Model(&model.Flow{}).Where("namespace = ?", "svc").Count(&svcFlowCount).Error; err != nil {
		t.Fatalf("count svc flows: %v", err)
	}
	if err := db.Model(&model.CommunityMembership{}).Where("community_id = ?", svcCommunity.ID).Count(&svcCommunityMembershipCount).Error; err != nil {
		t.Fatalf("count svc community memberships: %v", err)
	}
	if err := db.Model(&model.FlowMembership{}).Where("flow_id = ?", svcFlow.ID).Count(&svcFlowMembershipCount).Error; err != nil {
		t.Fatalf("count svc flow memberships: %v", err)
	}
	if err := db.Model(&model.Community{}).Where("namespace = ?", "other").Count(&otherCommunityCount).Error; err != nil {
		t.Fatalf("count other communities: %v", err)
	}
	if err := db.Model(&model.Flow{}).Where("namespace = ?", "other").Count(&otherFlowCount).Error; err != nil {
		t.Fatalf("count other flows: %v", err)
	}
	if svcCommunityCount != 0 {
		t.Fatalf("namespace communities should have been purged, got %d", svcCommunityCount)
	}
	if svcFlowCount != 0 {
		t.Fatalf("namespace flows should have been purged, got %d", svcFlowCount)
	}
	if svcCommunityMembershipCount != 0 {
		t.Fatalf("namespace community memberships should have been purged, got %d", svcCommunityMembershipCount)
	}
	if svcFlowMembershipCount != 0 {
		t.Fatalf("namespace flow memberships should have been purged, got %d", svcFlowMembershipCount)
	}
	if otherCommunityCount != 1 {
		t.Fatalf("control community should remain, got %d", otherCommunityCount)
	}
	if otherFlowCount != 1 {
		t.Fatalf("control flow should remain, got %d", otherFlowCount)
	}
}

func TestDeleteNamespace_PreservesFilesWhenDBPurgeFails(t *testing.T) {
	namespaceRoot := t.TempDir()
	h := &handlers{
		deps: &Deps{
			NamespaceRoot: namespaceRoot,
			Store:         &failDeleteGraphStore{err: errors.New("boom")},
		},
	}

	nsDir := filepath.Join(namespaceRoot, "svc")
	if err := os.MkdirAll(nsDir, 0o755); err != nil {
		t.Fatalf("mkdir namespace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nsDir, "file.go"), []byte("package svc"), 0o644); err != nil {
		t.Fatalf("write namespace file: %v", err)
	}

	req := makeCallToolRequest(t, map[string]any{"namespace": "svc"})
	result, err := h.deleteNamespace(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)

	if _, err := os.Stat(nsDir); err != nil {
		t.Fatalf("namespace directory should remain on DB purge failure: %v", err)
	}
}

func TestDeleteNamespace_PurgesOrphanMembershipsAndSearchIndex(t *testing.T) {
	namespaceRoot := t.TempDir()
	ragRoot := t.TempDir()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := db.AutoMigrate(&model.SearchDocument{}, &model.Flow{}, &model.FlowMembership{}, &model.Community{}, &model.CommunityMembership{}); err != nil {
		t.Fatalf("migrate extras: %v", err)
	}
	backend := &spySearchBackend{}

	h := &handlers{deps: &Deps{NamespaceRoot: namespaceRoot, RagIndexDir: ragRoot, Store: st, DB: db, SearchBackend: backend}}
	nsDir := filepath.Join(namespaceRoot, "svc")
	if err := os.MkdirAll(nsDir, 0o755); err != nil {
		t.Fatalf("mkdir namespace: %v", err)
	}

	svcCommunity := model.Community{Namespace: "svc", Key: "svc/core", Label: "svc/core", Strategy: "directory"}
	svcFlow := model.Flow{Namespace: "svc", Name: "svc-flow"}
	if err := db.Create(&svcCommunity).Error; err != nil {
		t.Fatalf("seed svc community: %v", err)
	}
	if err := db.Create(&svcFlow).Error; err != nil {
		t.Fatalf("seed svc flow: %v", err)
	}
	if err := db.Create(&model.CommunityMembership{CommunityID: svcCommunity.ID, NodeID: 424242}).Error; err != nil {
		t.Fatalf("seed orphan community membership: %v", err)
	}
	if err := db.Create(&model.FlowMembership{Namespace: ctxns.DefaultNamespace, FlowID: svcFlow.ID, NodeID: 353535, Ordinal: 0}).Error; err != nil {
		t.Fatalf("seed orphan flow membership: %v", err)
	}

	req := makeCallToolRequest(t, map[string]any{"namespace": "svc"})
	result, err := h.deleteNamespace(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	var communityMembershipCount, flowMembershipCount int64
	if err := db.Model(&model.CommunityMembership{}).Where("community_id = ?", svcCommunity.ID).Count(&communityMembershipCount).Error; err != nil {
		t.Fatalf("count orphan community memberships: %v", err)
	}
	if err := db.Model(&model.FlowMembership{}).Where("flow_id = ?", svcFlow.ID).Count(&flowMembershipCount).Error; err != nil {
		t.Fatalf("count orphan flow memberships: %v", err)
	}
	if communityMembershipCount != 0 {
		t.Fatalf("expected orphan community memberships purged, got %d", communityMembershipCount)
	}
	if flowMembershipCount != 0 {
		t.Fatalf("expected orphan flow memberships purged, got %d", flowMembershipCount)
	}
	if len(backend.purgeCalls) != 1 || backend.purgeCalls[0] != "svc" {
		t.Fatalf("expected one purge call for svc, got %#v", backend.purgeCalls)
	}
	if backend.lastDB == nil || backend.lastDB == db {
		t.Fatal("expected search purge to run inside namespace transaction handle")
	}
}

func TestDeleteNamespace_PreservesFilesWhenSearchPurgeFails(t *testing.T) {
	namespaceRoot := t.TempDir()
	ragRoot := t.TempDir()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	ctx := ctxns.WithNamespace(t.Context(), "svc")
	if err := st.UpsertNodes(ctx, []model.Node{{QualifiedName: "svc.Keep", Kind: model.NodeKindFunction, Name: "Keep", FilePath: "file.go", StartLine: 1, EndLine: 1, Language: "go"}}); err != nil {
		t.Fatalf("seed node: %v", err)
	}
	backend := &spySearchBackend{purgeErr: errors.New("fts purge boom")}
	h := &handlers{deps: &Deps{NamespaceRoot: namespaceRoot, RagIndexDir: ragRoot, Store: st, DB: db, SearchBackend: backend}}

	nsDir := filepath.Join(namespaceRoot, "svc")
	if err := os.MkdirAll(nsDir, 0o755); err != nil {
		t.Fatalf("mkdir namespace: %v", err)
	}

	req := makeCallToolRequest(t, map[string]any{"namespace": "svc"})
	result, err := h.deleteNamespace(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)

	if _, err := os.Stat(nsDir); err != nil {
		t.Fatalf("namespace directory should remain on search purge failure: %v", err)
	}
	if got, getErr := st.GetNode(ctx, "svc.Keep"); getErr != nil || got == nil {
		t.Fatalf("namespace graph should remain on search purge failure, node=%+v err=%v", got, getErr)
	}
}

func TestDeleteNamespace_NotFound(t *testing.T) {
	h, _ := namespaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{
		"namespace": "nonexistent",
	})
	result, err := h.deleteNamespace(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func TestDeleteNamespace_PathTraversal(t *testing.T) {
	h, _ := namespaceHandlers(t)

	for _, namespace := range []string{"../evil", ".", "..", "service/api"} {
		t.Run(namespace, func(t *testing.T) {
			req := makeCallToolRequest(t, map[string]any{
				"namespace": namespace,
			})
			result, err := h.deleteNamespace(t.Context(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertToolResultIsError(t, result)
		})
	}
}

func TestDeleteNamespace_NeverDeletesNamespaceRoot(t *testing.T) {
	h, root := namespaceHandlers(t)
	marker := filepath.Join(root, "marker.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	req := makeCallToolRequest(t, map[string]any{
		"namespace": ".",
	})
	result, err := h.deleteNamespace(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("namespace root marker should remain: %v", err)
	}
}

func TestUploadFile_RejectsNamespaceSymlinkEscape(t *testing.T) {
	h, root := namespaceHandlers(t)

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "svc")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	encoded := base64.StdEncoding.EncodeToString([]byte("malicious"))
	req := makeCallToolRequest(t, map[string]any{
		"namespace": "svc",
		"file_path": "docs/readme.md",
		"content":   encoded,
	})

	result, err := h.uploadFile(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)

	if _, err := os.Stat(filepath.Join(outside, "docs", "readme.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no file to be written outside namespace root")
	}
}

func TestUploadFiles_RejectsIntermediateSymlinkEscape(t *testing.T) {
	h, root := namespaceHandlers(t)

	nsDir := filepath.Join(root, "svc")
	if err := os.MkdirAll(nsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(nsDir, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	content := base64.StdEncoding.EncodeToString([]byte("package main"))
	filesJSON, _ := json.Marshal([]map[string]string{{
		"namespace": "svc",
		"file_path": "link/main.go",
		"content":   content,
	}})

	req := makeCallToolRequest(t, map[string]any{"files": string(filesJSON)})
	result, err := h.uploadFiles(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)

	if _, err := os.Stat(filepath.Join(outside, "main.go")); !os.IsNotExist(err) {
		t.Fatalf("expected no file to be written through symlink")
	}
}

func TestGetDocContent_RejectsNamespaceSymlinkEscape(t *testing.T) {
	h, root := namespaceHandlers(t)

	outside := t.TempDir()
	secretPath := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(secretPath, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "svc")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	req := makeCallToolRequest(t, map[string]any{
		"namespace": "svc",
		"file_path": "secret.md",
	})
	result, err := h.getDocContent(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func makeCallToolRequest(t *testing.T, args map[string]any) mcp.CallToolRequest {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

func assertToolResultNotError(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()
	if result.IsError {
		t.Errorf("expected success, got error: %v", result.Content)
	}
}

func assertToolResultIsError(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()
	if !result.IsError {
		t.Errorf("expected error result, got success: %v", result.Content)
	}
}
