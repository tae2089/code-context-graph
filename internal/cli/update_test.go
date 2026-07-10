package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
	storesearch "github.com/tae2089/code-context-graph/internal/store/search"
)

func setupUpdateTest(t *testing.T) (*Deps, *bytes.Buffer, *bytes.Buffer, *gorm.DB) {
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
	if err := db.AutoMigrate(&model.SearchDocument{}); err != nil {
		t.Fatal(err)
	}
	sb := storesearch.NewSQLiteBackend()
	if err := sb.Migrate(db); err != nil {
		if errors.Is(err, storesearch.ErrFTS5NotAvailable) {
			t.Skip("fts5 module not available, skipping test")
		}
		t.Fatal(err)
	}

	walker := treesitter.NewWalker(treesitter.GoSpec)
	deps.Store = st
	deps.Walkers = map[string]*treesitter.Walker{".go": walker}
	deps.Syncer = incremental.New(st, walker)
	deps.SearchBackend = sb
	deps.DB = db

	return deps, stdout, stderr, db
}

func setupUpdateGraphOnlyTest(t *testing.T) (*Deps, *bytes.Buffer, *bytes.Buffer, *gorm.DB) {
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

	walker := treesitter.NewWalker(treesitter.GoSpec)
	deps.Store = st
	deps.Walkers = map[string]*treesitter.Walker{".go": walker}
	deps.Syncer = incremental.New(st, walker)
	deps.DB = db

	return deps, stdout, stderr, db
}

func TestUpdateCommand_IncrementalSync(t *testing.T) {
	deps, stdout, stderr, db := setupUpdateTest(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "a.go", `package a
func A() {}
`)

	err := executeCmd(deps, stdout, stderr, "build", dir)
	if err != nil {
		t.Fatalf("initial build: %v", err)
	}

	var countBefore int64
	db.Model(&model.Node{}).Count(&countBefore)
	if countBefore == 0 {
		t.Fatal("expected nodes after initial build")
	}

	writeGoFile(t, dir, "b.go", `package a
func B() {}
`)

	stdout.Reset()
	stderr.Reset()

	err = executeCmd(deps, stdout, stderr, "update", dir)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	var countAfter int64
	db.Model(&model.Node{}).Count(&countAfter)
	if countAfter <= countBefore {
		t.Fatalf("expected more nodes after update: before=%d after=%d", countBefore, countAfter)
	}

	foundB := false
	var nodes []model.Node
	db.Find(&nodes)
	for _, n := range nodes {
		if n.Name == "B" {
			foundB = true
		}
	}
	if !foundB {
		t.Fatal("expected to find newly added function B")
	}
}

func TestUpdateCommand_DeletesRemovedFiles(t *testing.T) {
	deps, stdout, stderr, db := setupUpdateGraphOnlyTest(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "a.go", `package a
func A() {}
`)
	writeGoFile(t, dir, "b.go", `package a
func B() {}
`)

	if err := executeCmd(deps, stdout, stderr, "build", dir); err != nil {
		t.Fatalf("initial build: %v", err)
	}

	var before int64
	if err := db.Model(&model.Node{}).Where("file_path = ?", "b.go").Count(&before).Error; err != nil {
		t.Fatalf("count deleted file nodes before update: %v", err)
	}
	if before == 0 {
		t.Fatal("expected nodes for b.go after initial build")
	}

	if err := os.Remove(filepath.Join(dir, "b.go")); err != nil {
		t.Fatalf("remove b.go: %v", err)
	}

	stdout.Reset()
	stderr.Reset()

	if err := executeCmd(deps, stdout, stderr, "update", dir); err != nil {
		t.Fatalf("update: %v", err)
	}

	var after int64
	if err := db.Model(&model.Node{}).Where("file_path = ?", "b.go").Count(&after).Error; err != nil {
		t.Fatalf("count deleted file nodes after update: %v", err)
	}
	if after != 0 {
		t.Fatalf("expected b.go nodes to be removed after update, got %d", after)
	}
}

func TestUpdateCommand_ReportsAddedModifiedDeleted(t *testing.T) {
	deps, stdout, stderr, _ := setupUpdateTest(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "x.go", `package x
func X() {}
`)

	err := executeCmd(deps, stdout, stderr, "build", dir)
	if err != nil {
		t.Fatalf("initial build: %v", err)
	}

	writeGoFile(t, dir, "x.go", `package x
func X2() {}
`)
	writeGoFile(t, dir, "y.go", `package x
func Y() {}
`)

	stdout.Reset()
	stderr.Reset()

	err = executeCmd(deps, stdout, stderr, "update", dir)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Update complete") {
		t.Fatalf("expected 'Update complete' in output, got: %s", out)
	}
	if !strings.Contains(out, "added=") || !strings.Contains(out, "modified=") {
		t.Fatalf("expected added/modified stats in output, got: %s", out)
	}
}

func TestUpdateCommand_HonorsExcludePatterns(t *testing.T) {
	deps, stdout, stderr, db := setupUpdateGraphOnlyTest(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "a.go", `package a
func A() {}
`)

	if err := executeCmd(deps, stdout, stderr, "build", dir); err != nil {
		t.Fatalf("initial build: %v", err)
	}

	writeGoFile(t, dir, "ignored.gen.go", `package a
func Ignored() {}
`)

	stdout.Reset()
	stderr.Reset()
	if err := executeCmd(deps, stdout, stderr, "update", "--exclude", ".*\\.gen\\.go$", dir); err != nil {
		t.Fatalf("update: %v", err)
	}

	var ignoredCount int64
	if err := db.Model(&model.Node{}).Where("file_path = ?", "ignored.gen.go").Count(&ignoredCount).Error; err != nil {
		t.Fatalf("count ignored nodes: %v", err)
	}
	if ignoredCount != 0 {
		t.Fatalf("expected excluded file to remain out of graph, got %d nodes", ignoredCount)
	}
}

func TestUpdateCommand_RefreshesSearchIndex(t *testing.T) {
	deps, stdout, stderr, _ := setupUpdateTest(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "a.go", `package a
func Searchable() {}
`)

	if err := executeCmd(deps, stdout, stderr, "build", dir); err != nil {
		t.Fatalf("initial build: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	writeGoFile(t, dir, "a.go", `package a
func SearchableUpdated() {}
`)

	if err := executeCmd(deps, stdout, stderr, "update", dir); err != nil {
		t.Fatalf("update: %v", err)
	}

	nodes, err := deps.SearchBackend.Query(context.Background(), deps.DB, "SearchableUpdated", 10)
	if err != nil {
		t.Fatalf("search after update: %v", err)
	}
	if len(nodes) == 0 || nodes[0].Name != "SearchableUpdated" {
		t.Fatalf("expected updated symbol in search results, got %#v", nodes)
	}
}

func TestUpdateCommand_NamespaceSearchIsolation(t *testing.T) {
	deps, stdout, stderr, db := setupUpdateTest(t)

	legacyNode := model.Node{Namespace: "other", QualifiedName: "other.Legacy", Kind: model.NodeKindFunction, Name: "Legacy", FilePath: "legacy.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&legacyNode).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: "other", NodeID: legacyNode.ID, Content: "legacy symbol", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := deps.SearchBackend.Rebuild(ctxns.WithNamespace(context.Background(), "other"), db); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	writeGoFile(t, dir, "a.go", `package a
func Scoped() {}
`)

	if err := executeCmd(deps, stdout, stderr, "build", "--namespace", "target", dir); err != nil {
		t.Fatalf("initial build: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	writeGoFile(t, dir, "a.go", `package a
func ScopedUpdated() {}
`)
	if err := executeCmd(deps, stdout, stderr, "update", "--namespace", "target", dir); err != nil {
		t.Fatalf("update: %v", err)
	}

	otherResults, err := deps.SearchBackend.Query(ctxns.WithNamespace(context.Background(), "other"), deps.DB, "legacy", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(otherResults) != 1 || otherResults[0].Namespace != "other" {
		t.Fatalf("expected other namespace search to remain intact, got %#v", otherResults)
	}
}

func TestUpdateCommand_HonorsCommandContextCancellation(t *testing.T) {
	deps, stdout, stderr, _ := setupUpdateTest(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "a.go", `package a
func Cancelled() {}
`)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := executeCmdWithContext(ctx, deps, stdout, stderr, "update", dir)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}
