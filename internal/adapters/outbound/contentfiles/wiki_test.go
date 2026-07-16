package contentfiles

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tae2089/code-context-graph/internal/app/wiki"
)

func TestWikiIndexWriterRejectsUnsafeNamespaceBeforeWriting(t *testing.T) {
	outer := t.TempDir()
	root := filepath.Join(outer, "root")
	writer := NewWikiIndexWriter(root)

	tests := []struct {
		name      string
		namespace string
	}{
		{name: "parent traversal", namespace: "../escape"},
		{name: "nested namespace", namespace: "owner/repo"},
		{name: "absolute namespace", namespace: filepath.Join(string(filepath.Separator), "escape")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := writer.WriteWikiIndex(context.Background(), tt.namespace, &wiki.Index{}); err == nil {
				t.Fatalf("WriteWikiIndex(%q) = nil, want validation error", tt.namespace)
			}
		})
	}

	if _, err := os.Stat(filepath.Join(outer, "escape", "wiki-index.json")); !os.IsNotExist(err) {
		t.Fatalf("unsafe namespace wrote outside root: %v", err)
	}
}

func TestWikiIndexWriterPreservesDefaultNamespace(t *testing.T) {
	root := t.TempDir()
	writer := NewWikiIndexWriter(root)
	if err := writer.WriteWikiIndex(context.Background(), "", &wiki.Index{}); err != nil {
		t.Fatalf("WriteWikiIndex(default): %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "wiki-index.json")); err != nil {
		t.Fatalf("default wiki index: %v", err)
	}
}
