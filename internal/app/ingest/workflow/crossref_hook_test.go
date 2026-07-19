package workflow

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/graphgorm"
	"github.com/tae2089/code-context-graph/internal/adapters/outbound/treesitter"
	"github.com/tae2089/code-context-graph/internal/app/crossref"
	"github.com/tae2089/code-context-graph/internal/app/ingest/incremental"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/trace"
)

type recordingCrossRefSyncer struct {
	namespaces []string
	err        error
}

func (r *recordingCrossRefSyncer) SyncNamespace(ctx context.Context) error {
	if r.err != nil {
		return r.err
	}
	r.namespaces = append(r.namespaces, requestctx.FromContext(ctx))
	return nil
}

func newCrossRefHookService(t *testing.T) (*Service, *graphgorm.Store, string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	svc := &Service{Store: st, UnitOfWork: newTestUnitOfWork(db, nil), Walkers: map[string]Parser{".go": treesitter.NewWalker(treesitter.GoSpec)}, Logger: slog.Default()}

	tmpDir := t.TempDir()
	writeFile := func(rel, content string) {
		full := filepath.Join(tmpDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	writeFile("go.mod", "module github.com/example/project\n\ngo 1.25.0\n")
	writeFile("mainpkg/main.go", "package mainpkg\n\nfunc Run() {}\n")
	return svc, st, tmpDir
}

func TestBuild_MaterializesCrossRefsFromSourceComments(t *testing.T) {
	svc, st, tmpDir := newCrossRefHookService(t)
	svc.CrossRefs = crossref.New(st)

	target := "package mainpkg\n\n// ValidateToken validates a token.\nfunc ValidateToken() {}\n"
	if err := os.MkdirAll(filepath.Join(tmpDir, "auth"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "auth/token.go"), []byte(target), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	authCtx := requestctx.WithNamespace(context.Background(), "auth-svc")
	if _, err := svc.Build(authCtx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("build auth-svc: %v", err)
	}

	caller := "package mainpkg\n\n// Login logs a user in.\n// @intent authenticate the session.\n// @see ccg://auth-svc/auth/token.go#ValidateToken\nfunc Login() {}\n"
	callerDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(callerDir, "go.mod"), []byte("module github.com/example/web\n\ngo 1.25.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(callerDir, "mainpkg"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(callerDir, "mainpkg/login.go"), []byte(caller), 0o644); err != nil {
		t.Fatalf("write caller: %v", err)
	}
	webCtx := requestctx.WithNamespace(context.Background(), "web")
	if _, err := svc.Build(webCtx, BuildOptions{Dir: callerDir}); err != nil {
		t.Fatalf("build web: %v", err)
	}

	rows, err := st.ListOutboundCrossRefs(context.Background(), "web")
	if err != nil {
		t.Fatalf("list outbound: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no cross refs materialized from @see comment")
	}
	// The comment may bind to both the function and its file node; every
	// materialized row must resolve into auth-svc.
	for _, row := range rows {
		if row.ToNamespace != "auth-svc" || row.ToSymbol != "ValidateToken" || row.Status != graph.CrossRefStatusResolved || row.ResolvedNodeID == nil {
			t.Fatalf("materialized row = %+v, want resolved ref into auth-svc", row)
		}
	}
}

func TestBuild_TriggersCrossRefSync(t *testing.T) {
	svc, _, tmpDir := newCrossRefHookService(t)
	rec := &recordingCrossRefSyncer{}
	svc.CrossRefs = rec

	ctx := requestctx.WithNamespace(context.Background(), "web")
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(rec.namespaces) != 1 || rec.namespaces[0] != "web" {
		t.Fatalf("cross-ref sync calls = %v, want one call for namespace web", rec.namespaces)
	}
}

func TestBuild_PropagatesCrossRefSyncError(t *testing.T) {
	svc, _, tmpDir := newCrossRefHookService(t)
	svc.CrossRefs = &recordingCrossRefSyncer{err: trace.New("sync boom")}

	if _, err := svc.Build(context.Background(), BuildOptions{Dir: tmpDir}); err == nil {
		t.Fatal("Build should surface cross-ref sync failure")
	}
}

func TestBuild_WithoutSyncerSkipsCrossRefSync(t *testing.T) {
	svc, _, tmpDir := newCrossRefHookService(t)
	if _, err := svc.Build(context.Background(), BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build without syncer: %v", err)
	}
}

func TestUpdate_TriggersCrossRefSyncOnce(t *testing.T) {
	svc, st, tmpDir := newCrossRefHookService(t)
	ctx := requestctx.WithNamespace(context.Background(), "web")
	if _, err := svc.Build(ctx, BuildOptions{Dir: tmpDir}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	rec := &recordingCrossRefSyncer{}
	svc.CrossRefs = rec
	if err := os.WriteFile(filepath.Join(tmpDir, "mainpkg/main.go"), []byte("package mainpkg\n\nfunc Run() {}\n\nfunc Stop() {}\n"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}
	syncer := incremental.NewWithRegistry(st, map[string]incremental.Parser{".go": treesitter.NewWalker(treesitter.GoSpec)}, incremental.WithLogger(slog.Default()))
	if _, err := svc.Update(ctx, UpdateOptions{BuildOptions: BuildOptions{Dir: tmpDir}, Syncer: syncer}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(rec.namespaces) != 1 || rec.namespaces[0] != "web" {
		t.Fatalf("cross-ref sync calls = %v, want exactly one call for namespace web", rec.namespaces)
	}
}
