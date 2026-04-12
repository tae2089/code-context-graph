package cli

import (
	"bytes"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/imtaebin/code-context-graph/internal/parse/treesitter"
	"github.com/imtaebin/code-context-graph/internal/store/gormstore"
)

func setupStatusTest(t *testing.T) (*Deps, *bytes.Buffer, *bytes.Buffer, *gorm.DB) {
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

	deps.DB = db
	deps.Store = st
	deps.Walkers = map[string]*treesitter.Walker{
		".go": treesitter.NewWalker(treesitter.GoSpec),
	}

	return deps, stdout, stderr, db
}

func TestStatusCommand_ShowsStats(t *testing.T) {
	deps, stdout, stderr, _ := setupStatusTest(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "s.go", `package s
func S() {}
`)

	err := executeCmd(deps, stdout, stderr, "build", dir)
	if err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()

	err = executeCmd(deps, stdout, stderr, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Nodes:") {
		t.Fatalf("expected 'Nodes:' in output, got: %s", out)
	}
	if !strings.Contains(out, "Edges:") {
		t.Fatalf("expected 'Edges:' in output, got: %s", out)
	}
	if !strings.Contains(out, "Files:") {
		t.Fatalf("expected 'Files:' in output, got: %s", out)
	}
}

func TestStatusCommand_ShowsKindBreakdown(t *testing.T) {
	deps, stdout, stderr, _ := setupStatusTest(t)

	dir := t.TempDir()
	writeGoFile(t, dir, "k.go", `package k

type Foo struct{}
func Bar() {}
`)

	err := executeCmd(deps, stdout, stderr, "build", dir)
	if err != nil {
		t.Fatal(err)
	}

	stdout.Reset()
	stderr.Reset()

	err = executeCmd(deps, stdout, stderr, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "Node kinds:") {
		t.Fatalf("expected 'Node kinds:' in output, got: %s", out)
	}
	if !strings.Contains(out, "function:") {
		t.Fatalf("expected 'function:' in kind breakdown, got: %s", out)
	}
}

func TestStatusCommand_EmptyDB(t *testing.T) {
	deps, stdout, stderr, _ := setupStatusTest(t)

	stdout.Reset()
	stderr.Reset()

	err := executeCmd(deps, stdout, stderr, "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "No data") {
		t.Fatalf("expected 'No data' for empty DB, got: %s", out)
	}
}
