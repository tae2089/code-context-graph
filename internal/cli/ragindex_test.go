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

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/ragindex"
	"github.com/tae2089/code-context-graph/internal/store/gormstore"
)

func setupRagIndexTestDeps(t *testing.T) *Deps {
	t.Helper()
	deps, _, _ := newTestDeps()

	dsn := "file:" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
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

func TestRagIndexCmd_NamespaceFiltersDocIndex(t *testing.T) {
	deps := setupRagIndexTestDeps(t)
	tmpDir := t.TempDir()
	indexDir := filepath.Join(tmpDir, ".ccg")

	backendComm := model.Community{Key: "backend", Label: "Backend", Strategy: "auto"}
	frontendComm := model.Community{Key: "frontend", Label: "Frontend", Strategy: "auto"}
	if err := deps.DB.Create(&backendComm).Error; err != nil {
		t.Fatalf("create backend community: %v", err)
	}
	if err := deps.DB.Create(&frontendComm).Error; err != nil {
		t.Fatalf("create frontend community: %v", err)
	}

	backendNode := model.Node{Namespace: "backend", QualifiedName: "backend/handler.go/Login", Kind: model.NodeKindFunction, Name: "Login", FilePath: "handler.go", StartLine: 1, EndLine: 10, Language: "go"}
	frontendNode := model.Node{Namespace: "frontend", QualifiedName: "frontend/page.go/Render", Kind: model.NodeKindFunction, Name: "Render", FilePath: "page.go", StartLine: 1, EndLine: 10, Language: "go"}
	if err := deps.DB.Create(&backendNode).Error; err != nil {
		t.Fatalf("create backend node: %v", err)
	}
	if err := deps.DB.Create(&frontendNode).Error; err != nil {
		t.Fatalf("create frontend node: %v", err)
	}
	if err := deps.DB.Create(&model.CommunityMembership{CommunityID: backendComm.ID, NodeID: backendNode.ID}).Error; err != nil {
		t.Fatalf("create backend membership: %v", err)
	}
	if err := deps.DB.Create(&model.CommunityMembership{CommunityID: frontendComm.ID, NodeID: frontendNode.ID}).Error; err != nil {
		t.Fatalf("create frontend membership: %v", err)
	}

	outBuffer := &bytes.Buffer{}
	errBuffer := &bytes.Buffer{}
	err := executeCmd(deps, outBuffer, errBuffer,
		"rag-index",
		"--namespace", "backend",
		"--index-dir", indexDir,
	)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	idx, err := ragindex.LoadIndex(filepath.Join(indexDir, "doc-index.json"))
	if err != nil {
		t.Fatalf("loadIndex: %v", err)
	}
	if got := len(idx.Root.Children); got != 1 {
		t.Fatalf("root children = %d, want 1", got)
	}
	if idx.Root.Children[0].Label != "Backend" {
		t.Fatalf("root child label = %q, want %q", idx.Root.Children[0].Label, "Backend")
	}
	if strings.Contains(outBuffer.String(), "frontend") {
		t.Fatalf("unexpected frontend output: %q", outBuffer.String())
	}
	if strings.Contains(errBuffer.String(), "frontend") {
		t.Fatalf("unexpected frontend stderr: %q", errBuffer.String())
	}
}
