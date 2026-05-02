package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/store"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
)

type failDeleteGraphStore struct {
	store.GraphStore
	err error
}

func (f *failDeleteGraphStore) DeleteGraph(ctx context.Context) error { return f.err }

func workspaceHandlers(t *testing.T) (*handlers, string) {
	t.Helper()
	root := t.TempDir()
	h := &handlers{
		deps: &Deps{
			WorkspaceRoot: root,
		},
	}
	return h, root
}

func TestUploadFile_Basic(t *testing.T) {
	h, root := workspaceHandlers(t)

	content := "# Hello World\nThis is a test."
	encoded := base64.StdEncoding.EncodeToString([]byte(content))

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "my-service",
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

func TestUploadFile_PathTraversal(t *testing.T) {
	h, _ := workspaceHandlers(t)

	encoded := base64.StdEncoding.EncodeToString([]byte("malicious"))

	tests := []struct {
		name      string
		workspace string
		filePath  string
	}{
		{"dotdot in workspace", "../evil", "file.md"},
		{"dotdot in file_path", "ok", "../../etc/passwd"},
		{"absolute workspace", "/etc", "passwd"},
		{"absolute file_path", "ok", "/etc/passwd"},
		{"dot workspace", ".", "file.md"},
		{"double-dot workspace", "..", "file.md"},
		{"path-like workspace slash", "service/api", "file.md"},
		{"path-like workspace separator", "service" + string(filepath.Separator) + "api", "file.md"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := makeCallToolRequest(t, map[string]any{
				"workspace": tt.workspace,
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

func TestUploadFile_RejectsEmptyWorkspace(t *testing.T) {
	h, _ := workspaceHandlers(t)
	encoded := base64.StdEncoding.EncodeToString([]byte("content"))

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "",
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
	h, _ := workspaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "my-service",
		"file_path": "file.md",
		"content":   "not-valid-base64!!!",
	})

	result, err := h.uploadFile(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func TestListWorkspaces_Empty(t *testing.T) {
	h, _ := workspaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{})
	result, err := h.listWorkspaces(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	text := extractText(result)
	var workspaces []string
	if err := json.Unmarshal([]byte(text), &workspaces); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(workspaces) != 0 {
		t.Errorf("expected empty list, got %v", workspaces)
	}
}

func TestListWorkspaces_WithData(t *testing.T) {
	h, root := workspaceHandlers(t)

	os.MkdirAll(filepath.Join(root, "service-a"), 0o755)
	os.MkdirAll(filepath.Join(root, "service-b"), 0o755)

	req := makeCallToolRequest(t, map[string]any{})
	result, err := h.listWorkspaces(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := extractText(result)
	var workspaces []string
	if err := json.Unmarshal([]byte(text), &workspaces); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(workspaces) != 2 {
		t.Errorf("expected 2 workspaces, got %d: %v", len(workspaces), workspaces)
	}
}

func TestListFiles_Basic(t *testing.T) {
	h, root := workspaceHandlers(t)

	wsDir := filepath.Join(root, "my-service")
	os.MkdirAll(filepath.Join(wsDir, "docs"), 0o755)
	os.WriteFile(filepath.Join(wsDir, "readme.md"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(wsDir, "docs", "api.md"), []byte("api"), 0o644)

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "my-service",
	})
	result, err := h.listFiles(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	text := extractText(result)
	var files []string
	if err := json.Unmarshal([]byte(text), &files); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d: %v", len(files), files)
	}
}

func TestListFiles_PathTraversal(t *testing.T) {
	h, _ := workspaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "../evil",
	})
	result, err := h.listFiles(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func TestDeleteFile_Basic(t *testing.T) {
	h, root := workspaceHandlers(t)

	wsDir := filepath.Join(root, "my-service")
	os.MkdirAll(wsDir, 0o755)
	filePath := filepath.Join(wsDir, "to-delete.md")
	os.WriteFile(filePath, []byte("bye"), 0o644)

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "my-service",
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
	h, _ := workspaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "my-service",
		"file_path": "nonexistent.md",
	})
	result, err := h.deleteFile(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func TestDeleteFile_PathTraversal(t *testing.T) {
	h, _ := workspaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "ok",
		"file_path": "../../etc/passwd",
	})
	result, err := h.deleteFile(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func TestUploadFiles_Basic(t *testing.T) {
	h, root := workspaceHandlers(t)

	file1 := base64.StdEncoding.EncodeToString([]byte("package main"))
	file2 := base64.StdEncoding.EncodeToString([]byte("package util"))

	filesJSON, _ := json.Marshal([]map[string]string{
		{"workspace": "svc-a", "file_path": "main.go", "content": file1},
		{"workspace": "svc-a", "file_path": "util.go", "content": file2},
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
	h, _ := workspaceHandlers(t)

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
	h, _ := workspaceHandlers(t)

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
	h, _ := workspaceHandlers(t)

	file1 := base64.StdEncoding.EncodeToString([]byte("data"))
	filesJSON, _ := json.Marshal([]map[string]string{
		{"workspace": "../evil", "file_path": "file.go", "content": file1},
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

func TestUploadFiles_MultipleWorkspaces(t *testing.T) {
	h, root := workspaceHandlers(t)

	file1 := base64.StdEncoding.EncodeToString([]byte("package a"))
	file2 := base64.StdEncoding.EncodeToString([]byte("package b"))

	filesJSON, _ := json.Marshal([]map[string]string{
		{"workspace": "svc-a", "file_path": "a.go", "content": file1},
		{"workspace": "svc-b", "file_path": "b.go", "content": file2},
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

func TestDeleteWorkspace_Basic(t *testing.T) {
	h, root := workspaceHandlers(t)

	wsDir := filepath.Join(root, "to-delete")
	os.MkdirAll(filepath.Join(wsDir, "subdir"), 0o755)
	os.WriteFile(filepath.Join(wsDir, "file.md"), []byte("content"), 0o644)
	os.WriteFile(filepath.Join(wsDir, "subdir", "nested.md"), []byte("nested"), 0o644)

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "to-delete",
	})
	result, err := h.deleteWorkspace(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	if _, err := os.Stat(wsDir); !os.IsNotExist(err) {
		t.Error("workspace directory should have been deleted")
	}
}

func TestDeleteWorkspace_PurgesNamespaceGraphRAGAndCache(t *testing.T) {
	workspaceRoot := t.TempDir()
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
			WorkspaceRoot: workspaceRoot,
			RagIndexDir:   ragRoot,
			Store:         st,
			DB:            db,
		},
		cache: cache,
	}

	wsDir := filepath.Join(workspaceRoot, "svc")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "file.go"), []byte("package svc"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
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

	req := makeCallToolRequest(t, map[string]any{"workspace": "svc"})
	result, err := h.deleteWorkspace(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultNotError(t, result)

	if _, err := os.Stat(wsDir); !os.IsNotExist(err) {
		t.Fatal("workspace directory should have been deleted")
	}
	if _, err := os.Stat(ragIndexPath); !os.IsNotExist(err) {
		t.Fatal("workspace rag index should have been deleted")
	}
	if _, ok := cache.Get("search:svc"); ok {
		t.Fatal("cache should have been flushed")
	}

	node, err := st.GetNode(ctx, "svc.Handler")
	if err != nil {
		t.Fatalf("get purged node: %v", err)
	}
	if node != nil {
		t.Fatal("workspace namespace graph should have been purged")
	}

	otherNode, err := st.GetNode(context.Background(), "other.Handler")
	if err != nil {
		t.Fatalf("get untouched node: %v", err)
	}
	if otherNode == nil {
		t.Fatal("out-of-workspace graph should remain")
	}

	var svcCommunityCount, svcFlowCount, otherCommunityCount, otherFlowCount int64
	if err := db.Model(&model.Community{}).Where("namespace = ?", "svc").Count(&svcCommunityCount).Error; err != nil {
		t.Fatalf("count svc communities: %v", err)
	}
	if err := db.Model(&model.Flow{}).Where("namespace = ?", "svc").Count(&svcFlowCount).Error; err != nil {
		t.Fatalf("count svc flows: %v", err)
	}
	if err := db.Model(&model.Community{}).Where("namespace = ?", "other").Count(&otherCommunityCount).Error; err != nil {
		t.Fatalf("count other communities: %v", err)
	}
	if err := db.Model(&model.Flow{}).Where("namespace = ?", "other").Count(&otherFlowCount).Error; err != nil {
		t.Fatalf("count other flows: %v", err)
	}
	if svcCommunityCount != 0 {
		t.Fatalf("workspace communities should have been purged, got %d", svcCommunityCount)
	}
	if svcFlowCount != 0 {
		t.Fatalf("workspace flows should have been purged, got %d", svcFlowCount)
	}
	if otherCommunityCount != 1 {
		t.Fatalf("control community should remain, got %d", otherCommunityCount)
	}
	if otherFlowCount != 1 {
		t.Fatalf("control flow should remain, got %d", otherFlowCount)
	}
}

func TestDeleteWorkspace_PreservesFilesWhenDBPurgeFails(t *testing.T) {
	workspaceRoot := t.TempDir()
	h := &handlers{
		deps: &Deps{
			WorkspaceRoot: workspaceRoot,
			Store:         &failDeleteGraphStore{err: errors.New("boom")},
		},
	}

	wsDir := filepath.Join(workspaceRoot, "svc")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "file.go"), []byte("package svc"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}

	req := makeCallToolRequest(t, map[string]any{"workspace": "svc"})
	result, err := h.deleteWorkspace(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)

	if _, err := os.Stat(wsDir); err != nil {
		t.Fatalf("workspace directory should remain on DB purge failure: %v", err)
	}
}

func TestDeleteWorkspace_NotFound(t *testing.T) {
	h, _ := workspaceHandlers(t)

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "nonexistent",
	})
	result, err := h.deleteWorkspace(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)
}

func TestDeleteWorkspace_PathTraversal(t *testing.T) {
	h, _ := workspaceHandlers(t)

	for _, workspace := range []string{"../evil", ".", "..", "service/api"} {
		t.Run(workspace, func(t *testing.T) {
			req := makeCallToolRequest(t, map[string]any{
				"workspace": workspace,
			})
			result, err := h.deleteWorkspace(t.Context(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertToolResultIsError(t, result)
		})
	}
}

func TestDeleteWorkspace_NeverDeletesWorkspaceRoot(t *testing.T) {
	h, root := workspaceHandlers(t)
	marker := filepath.Join(root, "marker.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	req := makeCallToolRequest(t, map[string]any{
		"workspace": ".",
	})
	result, err := h.deleteWorkspace(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("workspace root marker should remain: %v", err)
	}
}

func TestUploadFile_RejectsWorkspaceSymlinkEscape(t *testing.T) {
	h, root := workspaceHandlers(t)

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "svc")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	encoded := base64.StdEncoding.EncodeToString([]byte("malicious"))
	req := makeCallToolRequest(t, map[string]any{
		"workspace": "svc",
		"file_path": "docs/readme.md",
		"content":   encoded,
	})

	result, err := h.uploadFile(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToolResultIsError(t, result)

	if _, err := os.Stat(filepath.Join(outside, "docs", "readme.md")); !os.IsNotExist(err) {
		t.Fatalf("expected no file to be written outside workspace root")
	}
}

func TestUploadFiles_RejectsIntermediateSymlinkEscape(t *testing.T) {
	h, root := workspaceHandlers(t)

	wsDir := filepath.Join(root, "svc")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(wsDir, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	content := base64.StdEncoding.EncodeToString([]byte("package main"))
	filesJSON, _ := json.Marshal([]map[string]string{{
		"workspace": "svc",
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

func TestGetDocContent_RejectsWorkspaceSymlinkEscape(t *testing.T) {
	h, root := workspaceHandlers(t)

	outside := t.TempDir()
	secretPath := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(secretPath, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "svc")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	req := makeCallToolRequest(t, map[string]any{
		"workspace": "svc",
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
