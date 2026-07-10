package safepath_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tae2089/code-context-graph/internal/safepath"
)

func TestEnsureNoSymlinkInPath_AllowsPlainNestedPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := safepath.EnsureNoSymlinkInPath(root, filepath.Join("a", "b"), false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != filepath.Join(root, "a", "b") {
		t.Fatalf("unexpected path %q", got)
	}
}

func TestEnsureNoSymlinkInPath_DotReturnsRoot(t *testing.T) {
	root := t.TempDir()
	got, err := safepath.EnsureNoSymlinkInPath(root, ".", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != root {
		t.Fatalf("expected root, got %q", got)
	}
}

func TestEnsureNoSymlinkInPath_RejectsSymlinkSegment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := safepath.EnsureNoSymlinkInPath(root, filepath.Join("link", "secret"), false)
	if err == nil {
		t.Fatal("expected symlink segment to be rejected")
	}
}

func TestEnsureNoSymlinkInPath_MissingLeafToleratedWhenAllowed(t *testing.T) {
	root := t.TempDir()
	rel := filepath.Join("not", "there.txt")
	got, err := safepath.EnsureNoSymlinkInPath(root, rel, true)
	if err != nil {
		t.Fatalf("expected missing leaf tolerated, got %v", err)
	}
	if got != filepath.Join(root, rel) {
		t.Fatalf("unexpected path %q", got)
	}
}

func TestEnsureNoSymlinkInPath_MissingLeafErrorsWhenNotAllowed(t *testing.T) {
	root := t.TempDir()
	if _, err := safepath.EnsureNoSymlinkInPath(root, "missing.txt", false); err == nil {
		t.Fatal("expected error for missing leaf when allowMissingLeaf is false")
	}
}

func TestCanonical_StrictRequiresExistence(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "nope")
	if _, err := safepath.Canonical(missing, false); err == nil {
		t.Fatal("expected strict Canonical to error on missing path")
	}
	got, err := safepath.Canonical(root, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	real, _ := filepath.EvalSymlinks(root)
	if got != real {
		t.Fatalf("expected canonical %q, got %q", real, got)
	}
}

func TestCanonical_TolerantResolvesParentForMissingLeaf(t *testing.T) {
	root := t.TempDir()
	real, _ := filepath.EvalSymlinks(root)
	got, err := safepath.Canonical(filepath.Join(root, "newleaf"), true)
	if err != nil {
		t.Fatalf("expected tolerant Canonical to succeed, got %v", err)
	}
	if got != filepath.Join(real, "newleaf") {
		t.Fatalf("unexpected path %q", got)
	}
}

func TestIsWithinRoot(t *testing.T) {
	root := "/srv/data"
	cases := []struct {
		target string
		want   bool
	}{
		{"/srv/data", true},
		{"/srv/data/sub/file", true},
		{"/srv/dataother", false},
		{"/srv", false},
		{"/etc/passwd", false},
	}
	for _, c := range cases {
		got, err := safepath.IsWithinRoot(root, c.target)
		if err != nil {
			t.Fatalf("IsWithinRoot(%q,%q) error: %v", root, c.target, err)
		}
		if got != c.want {
			t.Errorf("IsWithinRoot(%q,%q)=%v want %v", root, c.target, got, c.want)
		}
	}
}
