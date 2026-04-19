package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/model"
	"github.com/imtaebin/code-context-graph/internal/parse/treesitter"
	"github.com/imtaebin/code-context-graph/internal/store/gormstore"
)

func setupBuildTest(t *testing.T) (*Deps, *bytes.Buffer, *bytes.Buffer, *gorm.DB) {
	t.Helper()
	deps, stdout, stderr := newTestDeps()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}

	st := gormstore.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}

	deps.Store = st
	deps.Walkers = map[string]*treesitter.Walker{
		".go": treesitter.NewWalker(treesitter.GoSpec),
	}

	return deps, stdout, stderr, db
}

func TestBuildCommand_ParsesDirectory(t *testing.T) {
	deps, stdout, stderr, db := setupBuildTest(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "hello.go", `package hello

func Hello() string {
	return "hello"
}
`)

	err := executeCmd(deps, stdout, stderr, "build", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var nodes []model.Node
	if err := db.Find(&nodes).Error; err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected nodes to be stored in DB")
	}

	foundFunc := false
	for _, n := range nodes {
		if n.Kind == model.NodeKindFunction && n.Name == "Hello" {
			foundFunc = true
		}
	}
	if !foundFunc {
		t.Fatal("expected to find Hello function node")
	}

	var edges []model.Edge
	db.Find(&edges)

	var count int64
	db.Model(&model.Node{}).Where("file_path LIKE ?", "%hello.go").Count(&count)
	if count == 0 {
		t.Fatal("expected nodes with hello.go file_path")
	}
}

func writeGoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildCommand_DefaultCurrentDir(t *testing.T) {
	deps, stdout, stderr, db := setupBuildTest(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "main.go", `package main

func main() {}
`)

	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	err := executeCmd(deps, stdout, stderr, "build")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var count int64
	db.Model(&model.Node{}).Count(&count)
	if count == 0 {
		t.Fatal("expected nodes when building current directory")
	}
}

func TestBuildCommand_SkipsDotGitVendor(t *testing.T) {
	deps, stdout, stderr, db := setupBuildTest(t)

	dir := t.TempDir()

	writeGoFile(t, dir, "good.go", `package good
func Good() {}
`)

	gitDir := filepath.Join(dir, ".git")
	os.MkdirAll(gitDir, 0755)
	writeGoFile(t, gitDir, "bad.go", `package bad
func Bad() {}
`)

	vendorDir := filepath.Join(dir, "vendor")
	os.MkdirAll(vendorDir, 0755)
	writeGoFile(t, vendorDir, "vendored.go", `package vendored
func Vendored() {}
`)

	nodeModDir := filepath.Join(dir, "node_modules")
	os.MkdirAll(nodeModDir, 0755)
	writeGoFile(t, nodeModDir, "npm.go", `package npm
func Npm() {}
`)

	err := executeCmd(deps, stdout, stderr, "build", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var nodes []model.Node
	db.Find(&nodes)

	for _, n := range nodes {
		if n.Name == "Bad" || n.Name == "Vendored" || n.Name == "Npm" {
			t.Fatalf("should have skipped node %s from excluded directory", n.Name)
		}
	}

	foundGood := false
	for _, n := range nodes {
		if n.Name == "Good" {
			foundGood = true
		}
	}
	if !foundGood {
		t.Fatal("expected Good function to be parsed")
	}
}

func TestBuildCommand_ReportsStats(t *testing.T) {
	deps, stdout, stderr, _ := setupBuildTest(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "stats.go", `package stats

func Alpha() {}
func Beta() {}
`)

	err := executeCmd(deps, stdout, stderr, "build", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if len(out) == 0 {
		t.Fatal("expected stats output on stdout")
	}

	ctx := context.Background()
	_ = ctx
}

func TestBuildCommand_Namespace_StoresWithNamespace(t *testing.T) {
	deps, stdout, stderr, db := setupBuildTest(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "hello.go", `package hello
func Hello() {}
`)

	err := executeCmd(deps, stdout, stderr, "build", "--namespace", "backend", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var nodes []model.Node
	db.Where("namespace = ?", "backend").Find(&nodes)
	if len(nodes) == 0 {
		t.Fatal("expected nodes with namespace 'backend'")
	}

	for _, n := range nodes {
		if n.Namespace != "backend" {
			t.Errorf("expected namespace 'backend', got %q", n.Namespace)
		}
	}
}

func TestBuildCommand_Path_OnlyIncludedPathsParsed(t *testing.T) {
	deps, stdout, stderr, db := setupBuildTest(t)

	dir := t.TempDir()

	apiDir := filepath.Join(dir, "src", "api")
	os.MkdirAll(apiDir, 0755)
	writeGoFile(t, apiDir, "handler.go", `package api
func Handler() {}
`)

	authDir := filepath.Join(dir, "src", "auth")
	os.MkdirAll(authDir, 0755)
	writeGoFile(t, authDir, "auth.go", `package auth
func Authenticate() {}
`)

	otherDir := filepath.Join(dir, "src", "other")
	os.MkdirAll(otherDir, 0755)
	writeGoFile(t, otherDir, "other.go", `package other
func Other() {}
`)

	writeGoFile(t, dir, "root.go", `package root
func Root() {}
`)

	err := executeCmd(deps, stdout, stderr, "build", "--path", "src/api", "--path", "src/auth", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var nodes []model.Node
	db.Find(&nodes)

	foundHandler := false
	foundAuth := false
	for _, n := range nodes {
		switch n.Name {
		case "Handler":
			foundHandler = true
		case "Authenticate":
			foundAuth = true
		case "Other", "Root":
			t.Errorf("--path should exclude %s, but it was parsed", n.Name)
		}
	}
	if !foundHandler {
		t.Error("expected Handler function to be parsed (in --path src/api)")
	}
	if !foundAuth {
		t.Error("expected Authenticate function to be parsed (in --path src/auth)")
	}
}

func TestBuildCommand_NoRecursive_SkipsSubdirs(t *testing.T) {
	deps, stdout, stderr, db := setupBuildTest(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "root.go", `package root
func Root() {}
`)

	subDir := filepath.Join(dir, "sub")
	os.MkdirAll(subDir, 0755)
	writeGoFile(t, subDir, "deep.go", `package sub
func Deep() {}
`)

	if err := executeCmd(deps, stdout, stderr, "build", "--no-recursive", dir); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var nodes []model.Node
	db.Find(&nodes)

	for _, n := range nodes {
		if n.Name == "Deep" {
			t.Errorf("--no-recursive should not parse subdirectory files, but found Deep")
		}
	}

	foundRoot := false
	for _, n := range nodes {
		if n.Name == "Root" {
			foundRoot = true
		}
	}
	if !foundRoot {
		t.Error("expected Root function in root dir to be parsed")
	}
}
