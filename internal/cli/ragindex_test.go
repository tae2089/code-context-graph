package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/store/gormstore"
)

func setupRagIndexTestDeps(t *testing.T) *Deps {
	t.Helper()
	deps, _, _ := newTestDeps()

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
	return deps
}

func TestRagIndexCmd_OutputsBuiltMessage(t *testing.T) {
	deps := setupRagIndexTestDeps(t)
	tmpDir := t.TempDir()
	indexDir := filepath.Join(tmpDir, ".ccg")

	outBuffer := &bytes.Buffer{}
	errBuffer := &bytes.Buffer{}

	err := executeCmd(deps, outBuffer, errBuffer,
		"rag-index",
		"--index-dir", indexDir,
	)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	output := outBuffer.String()
	if output == "" {
		t.Error("expected output message, got empty string")
	}
	if !strings.Contains(output, "Built doc-index:") {
		t.Errorf("expected 'Built doc-index:' in output, got: %q", output)
	}

	// doc-index.json이 생성되어야 한다
	if _, err := os.Stat(filepath.Join(indexDir, "doc-index.json")); err != nil {
		t.Errorf("doc-index.json not created: %v", err)
	}
}

func TestRagIndexCmd_NoDB(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	// deps.DB == nil

	outBuffer := stdout
	errBuffer := stderr
	err := executeCmd(deps, outBuffer, errBuffer, "rag-index")
	if err == nil {
		t.Fatal("expected error when DB is nil, got nil")
	}
}

func TestRagIndexCmd_OutFlag(t *testing.T) {
	deps := setupRagIndexTestDeps(t)
	tmpDir := t.TempDir()
	indexDir := filepath.Join(tmpDir, ".ccg")
	docsDir := filepath.Join(tmpDir, "mydocs")

	outBuffer := &bytes.Buffer{}
	errBuffer := &bytes.Buffer{}

	err := executeCmd(deps, outBuffer, errBuffer,
		"rag-index",
		"--out", docsDir,
		"--index-dir", indexDir,
	)
	if err != nil {
		t.Fatalf("Execute() with --out flag error: %v", err)
	}

	// doc-index.json must be created
	if _, err := os.Stat(filepath.Join(indexDir, "doc-index.json")); err != nil {
		t.Errorf("doc-index.json not created: %v", err)
	}
}
