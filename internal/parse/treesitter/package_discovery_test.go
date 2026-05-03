package treesitter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestGoPackageDiscoveryHonorsCanceledContext(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module github.com/example/project\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (GoPackageDiscovery{}).DiscoverPackages(ctx, PackageDiscoveryOptions{
		RootDir: tmpDir,
		WalkFiles: func(func(path, relPath string) error) error {
			return nil
		},
		HasParser: func(ext string) bool {
			return ext == ".go"
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestGoPackageDiscoverySuppressesAmbiguousPackageClauses(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte("module github.com/example/project\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	dir := filepath.Join(tmpDir, "internal", "api")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package contracts\n"), 0o644); err != nil {
		t.Fatalf("write a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package other\n"), 0o644); err != nil {
		t.Fatalf("write b.go: %v", err)
	}

	ctx := context.Background()
	packages, err := (GoPackageDiscovery{}).DiscoverPackages(ctx, PackageDiscoveryOptions{
		RootDir: tmpDir,
		WalkFiles: func(fn func(path, relPath string) error) error {
			return filepath.Walk(tmpDir, func(path string, info os.FileInfo, err error) error {
				if err != nil || info == nil || info.IsDir() {
					return err
				}
				relPath, err := filepath.Rel(tmpDir, path)
				if err != nil {
					return err
				}
				return fn(path, relPath)
			})
		},
		HasParser: func(ext string) bool {
			return ext == ".go"
		},
	})
	if err != nil {
		t.Fatalf("DiscoverPackages: %v", err)
	}
	if _, ok := packages["github.com/example/project/internal/api"]; ok {
		t.Fatalf("expected conflicting package clauses to suppress package, got %+v", packages)
	}
}
