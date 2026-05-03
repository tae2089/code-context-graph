package treesitter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
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

func TestPythonPackageDiscovery_BuildsPackagesFromPythonFiles(t *testing.T) {
	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("pkg/sub")
	mustWrite("pkg/__init__.py", "")
	mustWrite("pkg/sub/module.py", "class Service: pass\n")

	packages, err := (PythonPackageDiscovery{}).DiscoverPackages(context.Background(), PackageDiscoveryOptions{
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
			return ext == ".py"
		},
	})
	if err != nil {
		t.Fatalf("DiscoverPackages: %v", err)
	}
	if len(packages) != 2 {
		t.Fatalf("expected 2 packages, got %+v", packages)
	}
	pkg, ok := packages["pkg"]
	if !ok {
		t.Fatalf("expected package pkg, got %+v", packages)
	}
	if pkg.Name != "pkg" || pkg.Dir != "pkg" || pkg.Language != "python" {
		t.Fatalf("unexpected pkg metadata: %+v", pkg)
	}
	if len(pkg.Files) != 1 || pkg.Files[0] != "pkg/__init__.py" {
		t.Fatalf("unexpected pkg files: %+v", pkg.Files)
	}
	sub, ok := packages["pkg.sub"]
	if !ok {
		t.Fatalf("expected package pkg.sub, got %+v", packages)
	}
	if sub.Name != "sub" || sub.Dir != "pkg/sub" || sub.Language != "python" {
		t.Fatalf("unexpected pkg.sub metadata: %+v", sub)
	}
	if len(sub.Files) != 1 || sub.Files[0] != "pkg/sub/module.py" {
		t.Fatalf("unexpected pkg.sub files: %+v", sub.Files)
	}
}

func TestPythonPackageDiscovery_UsesRepoRelativeSrcLayoutPackageNames(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "src", "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "src", "pkg", "module.py"), []byte("class Service: pass\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	packages, err := (PythonPackageDiscovery{}).DiscoverPackages(context.Background(), PackageDiscoveryOptions{
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
			return ext == ".py"
		},
	})
	if err != nil {
		t.Fatalf("DiscoverPackages: %v", err)
	}
	if _, ok := packages["src.pkg"]; !ok {
		t.Fatalf("expected src.pkg package, got %+v", packages)
	}
	if _, ok := packages["pkg"]; ok {
		t.Fatalf("did not expect runtime-style pkg package, got %+v", packages)
	}
}

func TestPythonPackageDiscovery_DoesNotSynthesizeParentNamespacePackages(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "pkg", "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "pkg", "sub", "module.py"), []byte("class Service: pass\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	packages, err := (PythonPackageDiscovery{}).DiscoverPackages(context.Background(), PackageDiscoveryOptions{
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
			return ext == ".py"
		},
	})
	if err != nil {
		t.Fatalf("DiscoverPackages: %v", err)
	}
	if _, ok := packages["pkg.sub"]; !ok {
		t.Fatalf("expected pkg.sub package, got %+v", packages)
	}
	if _, ok := packages["pkg"]; ok {
		t.Fatalf("did not expect synthesized parent pkg package, got %+v", packages)
	}
}

func TestTypeScriptPackageDiscovery_BuildsPackageAndAliasImportPaths(t *testing.T) {
	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("src/utils")
	mustWrite("package.json", `{"name":"@acme/app"}`)
	mustWrite("tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@app/*":["src/*"]}}}`)
	mustWrite("src/utils/math.ts", "export const add = (a: number, b: number) => a + b;\n")

	packages, err := (TypeScriptPackageDiscovery{}).DiscoverPackages(context.Background(), PackageDiscoveryOptions{
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
			return ext == ".ts"
		},
	})
	if err != nil {
		t.Fatalf("DiscoverPackages: %v", err)
	}
	got := sortedPackageKeys(packages)
	want := []string{"@acme/app/src/utils", "@app/utils", "@app/utils/math"}
	if !slices.Equal(got, want) {
		t.Fatalf("package keys = %v, want %v", got, want)
	}
	for _, key := range want {
		pkg := packages[key]
		if pkg.Name != "utils" || pkg.Dir != "src/utils" || pkg.Language != "typescript" {
			t.Fatalf("unexpected package metadata for %s: %+v", key, pkg)
		}
		if len(pkg.Files) != 1 || pkg.Files[0] != "src/utils/math.ts" {
			t.Fatalf("unexpected package files for %s: %+v", key, pkg.Files)
		}
	}
}

func TestTypeScriptPackageDiscovery_RespectsBaseURLAndJSONCPaths(t *testing.T) {
	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("src/utils")
	mustWrite("package.json", `{"name":"@acme/app"}`)
	mustWrite("tsconfig.json", "{\n  // comment\n  \"compilerOptions\": {\n    \"baseUrl\": \"src\",\n    \"paths\": {\n      \"@app/*\": [\"*\"]\n    }\n  }\n}\n")
	mustWrite("src/utils/math.ts", "export const add = (a: number, b: number) => a + b;\n")

	packages, err := (TypeScriptPackageDiscovery{}).DiscoverPackages(context.Background(), PackageDiscoveryOptions{
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
			return ext == ".ts"
		},
	})
	if err != nil {
		t.Fatalf("DiscoverPackages: %v", err)
	}
	got := sortedPackageKeys(packages)
	want := []string{"@acme/app/src/utils", "@app/utils", "@app/utils/math"}
	if !slices.Equal(got, want) {
		t.Fatalf("package keys = %v, want %v", got, want)
	}
}

func TestJavaScriptPackageDiscovery_BuildsPackageImportPathsFromPackageJSON(t *testing.T) {
	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("lib/core")
	mustWrite("package.json", `{"name":"acme-web"}`)
	mustWrite("lib/core/index.js", "export function run() {}\n")

	packages, err := (JavaScriptPackageDiscovery{}).DiscoverPackages(context.Background(), PackageDiscoveryOptions{
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
			return ext == ".js"
		},
	})
	if err != nil {
		t.Fatalf("DiscoverPackages: %v", err)
	}
	got := sortedPackageKeys(packages)
	want := []string{"acme-web/lib/core"}
	if !slices.Equal(got, want) {
		t.Fatalf("package keys = %v, want %v", got, want)
	}
	pkg := packages[want[0]]
	if pkg.Name != "core" || pkg.Dir != "lib/core" || pkg.Language != "javascript" {
		t.Fatalf("unexpected package metadata: %+v", pkg)
	}
	if len(pkg.Files) != 1 || pkg.Files[0] != "lib/core/index.js" {
		t.Fatalf("unexpected package files: %+v", pkg.Files)
	}
}

func TestTypeScriptPackageDiscovery_DiscoversWorkspacePackagesAndNestedAliases(t *testing.T) {
	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("packages/core/src/utils")
	mustMkdir("apps/web/src/components")
	mustWrite("package.json", `{"name":"monorepo-root","workspaces":["packages/*","apps/*"]}`)
	mustWrite("tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@root/*":["shared/*"]}}}`)
	mustWrite("packages/core/package.json", `{"name":"@acme/core"}`)
	mustWrite("packages/core/tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@core/*":["src/*"]}}}`)
	mustWrite("packages/core/src/utils/math.ts", "export const add = (a: number, b: number) => a + b;\n")
	mustWrite("apps/web/package.json", `{"name":"@acme/web"}`)
	mustWrite("apps/web/tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@web/*":["src/*"]}}}`)
	mustWrite("apps/web/src/components/button.ts", "export const button = true;\n")

	packages, err := (TypeScriptPackageDiscovery{}).DiscoverPackages(context.Background(), PackageDiscoveryOptions{
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
			return ext == ".ts"
		},
	})
	if err != nil {
		t.Fatalf("DiscoverPackages: %v", err)
	}
	got := sortedPackageKeys(packages)
	wantSubset := []string{
		"@acme/core/src/utils",
		"@core/utils",
		"@core/utils/math",
		"@acme/web/src/components",
		"@web/components",
		"@web/components/button",
	}
	for _, want := range wantSubset {
		if _, ok := packages[want]; !ok {
			t.Fatalf("expected package %q in %v", want, got)
		}
	}
}

func TestJavaScriptPackageDiscovery_UsesWorkspacePackageNames(t *testing.T) {
	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("packages/ui/lib/core")
	mustWrite("package.json", `{"name":"root","workspaces":["packages/*"]}`)
	mustWrite("packages/ui/package.json", `{"name":"@acme/ui"}`)
	mustWrite("packages/ui/lib/core/index.js", "export function run() {}\n")

	packages, err := (JavaScriptPackageDiscovery{}).DiscoverPackages(context.Background(), PackageDiscoveryOptions{
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
			return ext == ".js"
		},
	})
	if err != nil {
		t.Fatalf("DiscoverPackages: %v", err)
	}
	if _, ok := packages["@acme/ui/lib/core"]; !ok {
		t.Fatalf("expected workspace package import path, got %v", sortedPackageKeys(packages))
	}
}

func TestTypeScriptPackageDiscovery_RespectsPnpmWorkspacePackages(t *testing.T) {
	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("packages/shared/src")
	mustWrite("pnpm-workspace.yaml", "packages:\n  - 'packages/*'\n")
	mustWrite("package.json", `{"name":"root"}`)
	mustWrite("packages/shared/package.json", `{"name":"@acme/shared"}`)
	mustWrite("packages/shared/tsconfig.json", `{"compilerOptions":{"baseUrl":".","paths":{"@shared/*":["src/*"]}}}`)
	mustWrite("packages/shared/src/index.ts", "export const shared = true;\n")

	packages, err := (TypeScriptPackageDiscovery{}).DiscoverPackages(context.Background(), PackageDiscoveryOptions{
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
			return ext == ".ts"
		},
	})
	if err != nil {
		t.Fatalf("DiscoverPackages: %v", err)
	}
	if _, ok := packages["@acme/shared/src"]; !ok {
		t.Fatalf("expected pnpm workspace package import path, got %v", sortedPackageKeys(packages))
	}
	if _, ok := packages["@shared/index"]; !ok {
		t.Fatalf("expected nested tsconfig alias import path, got %v", sortedPackageKeys(packages))
	}
}

func TestJavaPackageDiscovery_GroupsByPackageDeclaration(t *testing.T) {
	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("src/main/java/com/example/auth")
	mustWrite("pom.xml", "<project/>\n")
	mustWrite("src/main/java/com/example/auth/User.java", "package com.example.auth;\npublic class User {}\n")

	packages, err := (JavaPackageDiscovery{}).DiscoverPackages(context.Background(), PackageDiscoveryOptions{
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
			return ext == ".java"
		},
	})
	if err != nil {
		t.Fatalf("DiscoverPackages: %v", err)
	}
	got := sortedPackageKeys(packages)
	want := []string{"com.example.auth"}
	if !slices.Equal(got, want) {
		t.Fatalf("package keys = %v, want %v", got, want)
	}
	pkg := packages[want[0]]
	if pkg.Name != "auth" || pkg.Dir != "com/example/auth" || pkg.Language != "java" {
		t.Fatalf("unexpected package metadata: %+v", pkg)
	}
	if len(pkg.Files) != 1 || pkg.Files[0] != "src/main/java/com/example/auth/User.java" {
		t.Fatalf("unexpected package files: %+v", pkg.Files)
	}
}

func TestJavaPackageDiscovery_MergesSamePackageAcrossSourceRoots(t *testing.T) {
	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("src/main/java/com/example/auth")
	mustMkdir("src/test/java/com/example/auth")
	mustWrite("src/main/java/com/example/auth/User.java", "package com.example.auth;\npublic class User {}\n")
	mustWrite("src/test/java/com/example/auth/UserTest.java", "package com.example.auth;\npublic class UserTest {}\n")

	packages, err := (JavaPackageDiscovery{}).DiscoverPackages(context.Background(), PackageDiscoveryOptions{
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
			return ext == ".java"
		},
	})
	if err != nil {
		t.Fatalf("DiscoverPackages: %v", err)
	}
	pkg, ok := packages["com.example.auth"]
	if !ok {
		t.Fatalf("expected merged package, got %+v", packages)
	}
	if got := sortedPackageKeys(packages); !slices.Equal(got, []string{"com.example.auth"}) {
		t.Fatalf("package keys = %v, want [com.example.auth]", got)
	}
	if got := append([]string(nil), pkg.Files...); !slices.Equal(got, []string{"src/main/java/com/example/auth/User.java", "src/test/java/com/example/auth/UserTest.java"}) {
		t.Fatalf("merged package files = %v", got)
	}
}

func TestKotlinPackageDiscovery_GroupsByPackageHeader(t *testing.T) {
	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("src/main/kotlin/com/example/auth")
	mustWrite("build.gradle.kts", "plugins {}\n")
	mustWrite("src/main/kotlin/com/example/auth/User.kt", "package com.example.auth\nclass User\n")

	packages, err := (KotlinPackageDiscovery{}).DiscoverPackages(context.Background(), PackageDiscoveryOptions{
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
			return ext == ".kt"
		},
	})
	if err != nil {
		t.Fatalf("DiscoverPackages: %v", err)
	}
	got := sortedPackageKeys(packages)
	want := []string{"com.example.auth"}
	if !slices.Equal(got, want) {
		t.Fatalf("package keys = %v, want %v", got, want)
	}
	pkg := packages[want[0]]
	if pkg.Name != "auth" || pkg.Dir != "com/example/auth" || pkg.Language != "kotlin" {
		t.Fatalf("unexpected package metadata: %+v", pkg)
	}
	if len(pkg.Files) != 1 || pkg.Files[0] != "src/main/kotlin/com/example/auth/User.kt" {
		t.Fatalf("unexpected package files: %+v", pkg.Files)
	}
}

func TestKotlinPackageDiscovery_MergesSamePackageAcrossSourceRoots(t *testing.T) {
	tmpDir := t.TempDir()
	mustMkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(tmpDir, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	mustWrite := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(tmpDir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mustMkdir("src/main/kotlin/com/example/auth")
	mustMkdir("src/test/kotlin/com/example/auth")
	mustWrite("src/main/kotlin/com/example/auth/User.kt", "package com.example.auth\nclass User\n")
	mustWrite("src/test/kotlin/com/example/auth/UserTest.kt", "package com.example.auth\nclass UserTest\n")

	packages, err := (KotlinPackageDiscovery{}).DiscoverPackages(context.Background(), PackageDiscoveryOptions{
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
			return ext == ".kt"
		},
	})
	if err != nil {
		t.Fatalf("DiscoverPackages: %v", err)
	}
	pkg, ok := packages["com.example.auth"]
	if !ok {
		t.Fatalf("expected merged package, got %+v", packages)
	}
	if got := sortedPackageKeys(packages); !slices.Equal(got, []string{"com.example.auth"}) {
		t.Fatalf("package keys = %v, want [com.example.auth]", got)
	}
	if got := append([]string(nil), pkg.Files...); !slices.Equal(got, []string{"src/main/kotlin/com/example/auth/User.kt", "src/test/kotlin/com/example/auth/UserTest.kt"}) {
		t.Fatalf("merged package files = %v", got)
	}
}

func TestPackageDiscoveryOrDefault_DefaultsForUnimplementedLanguages(t *testing.T) {
	tests := []struct {
		name string
		spec *LangSpec
	}{
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			discovery := PackageDiscoveryOrDefault(tc.spec)
			if discovery == nil {
				t.Fatal("expected non-nil package discovery")
			}

			packages, err := discovery.DiscoverPackages(context.Background(), PackageDiscoveryOptions{
				RootDir: t.TempDir(),
				WalkFiles: func(func(path, relPath string) error) error {
					return nil
				},
				HasParser: func(string) bool {
					return true
				},
			})
			if err != nil {
				t.Fatalf("DiscoverPackages: %v", err)
			}
			if packages != nil {
				t.Fatalf("expected nil packages for default discovery, got %+v", packages)
			}
		})
	}
	if len(tests) != 0 {
		return
	}
}

func sortedPackageKeys(packages map[string]PackageInfo) []string {
	keys := make([]string, 0, len(packages))
	for key := range packages {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
