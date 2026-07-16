package graphgorm

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

type annotationSQLCounter struct {
	statements atomic.Int64
}

func (c *annotationSQLCounter) LogMode(logger.LogLevel) logger.Interface { return c }
func (c *annotationSQLCounter) Info(context.Context, string, ...any)     {}
func (c *annotationSQLCounter) Warn(context.Context, string, ...any)     {}
func (c *annotationSQLCounter) Error(context.Context, string, ...any)    {}
func (c *annotationSQLCounter) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	c.statements.Add(1)
	_, _ = fc()
}

func (c *annotationSQLCounter) reset() {
	c.statements.Store(0)
}

func setupAnnotationSQLMeasurement(t *testing.T, count int) (*Store, context.Context, []*graph.Annotation, *annotationSQLCounter) {
	t.Helper()
	counter := &annotationSQLCounter{}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: counter})
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	store := New(db)
	if err := store.AutoMigrate(); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	ctx := requestctx.WithNamespace(context.Background(), "annotation-sql-measurement")
	nodes := make([]graph.Node, count)
	for i := range nodes {
		nodes[i] = graph.Node{
			QualifiedName: fmt.Sprintf("sample.F%d", i),
			Kind:          graph.NodeKindFunction,
			Name:          fmt.Sprintf("F%d", i),
			FilePath:      "sample.go",
			StartLine:     i + 1,
			EndLine:       i + 1,
			Language:      "go",
		}
	}
	if err := store.UpsertNodes(ctx, nodes); err != nil {
		t.Fatalf("upsert nodes: %v", err)
	}
	stored, err := store.GetNodesByFile(ctx, "sample.go")
	if err != nil {
		t.Fatalf("load nodes: %v", err)
	}
	annotations := make([]*graph.Annotation, len(stored))
	for i := range stored {
		annotations[i] = &graph.Annotation{
			NodeID:  stored[i].ID,
			Summary: fmt.Sprintf("summary %d", i),
			Tags: []graph.DocTag{{
				Kind:    graph.TagIntent,
				Value:   fmt.Sprintf("intent %d", i),
				Ordinal: 0,
			}},
		}
	}
	counter.reset()
	return store, ctx, annotations, counter
}

func TestUpsertAnnotations_BatchesSQLAndPreservesTags(t *testing.T) {
	store, ctx, annotations, counter := setupAnnotationSQLMeasurement(t, 20)
	started := time.Now()
	if err := store.UpsertAnnotations(ctx, annotations); err != nil {
		t.Fatalf("upsert annotations: %v", err)
	}
	statements := counter.statements.Load()
	t.Logf("bulk annotation candidate: annotations=%d statements=%d elapsed=%s", len(annotations), statements, time.Since(started))
	if statements > 5 {
		t.Fatalf("bulk annotation statements = %d, want <= 5", statements)
	}

	for i, annotation := range annotations {
		got, err := store.GetAnnotation(ctx, annotation.NodeID)
		if err != nil {
			t.Fatalf("get annotation %d: %v", i, err)
		}
		if got == nil || got.Summary != annotation.Summary {
			t.Fatalf("annotation %d = %+v, want summary %q", i, got, annotation.Summary)
		}
		if len(got.Tags) != 1 || got.Tags[0].Value != annotation.Tags[0].Value {
			t.Fatalf("annotation %d tags = %+v, want %+v", i, got.Tags, annotation.Tags)
		}
	}

	for i, annotation := range annotations {
		annotation.Summary = fmt.Sprintf("updated summary %d", i)
		annotation.Tags = []graph.DocTag{{Kind: graph.TagDomainRule, Value: fmt.Sprintf("rule %d", i), Ordinal: 0}}
	}
	counter.reset()
	if err := store.UpsertAnnotations(ctx, annotations); err != nil {
		t.Fatalf("update annotations: %v", err)
	}
	if statements := counter.statements.Load(); statements > 5 {
		t.Fatalf("bulk annotation update statements = %d, want <= 5", statements)
	}
	for i, annotation := range annotations {
		got, err := store.GetAnnotation(ctx, annotation.NodeID)
		if err != nil {
			t.Fatalf("get updated annotation %d: %v", i, err)
		}
		if got == nil || got.Summary != annotation.Summary || len(got.Tags) != 1 || got.Tags[0].Value != annotation.Tags[0].Value {
			t.Fatalf("updated annotation %d = %+v, want %+v", i, got, annotation)
		}
	}
}

func TestUpsertAnnotations_RejectsForeignNodeBeforeWrites(t *testing.T) {
	store, ctx, annotations, _ := setupAnnotationSQLMeasurement(t, 1)
	foreignCtx := requestctx.WithNamespace(context.Background(), "foreign")
	foreignNodes := []graph.Node{{
		QualifiedName: "foreign.F",
		Kind:          graph.NodeKindFunction,
		Name:          "F",
		FilePath:      "foreign.go",
		StartLine:     1,
		EndLine:       1,
		Language:      "go",
	}}
	if err := store.UpsertNodes(foreignCtx, foreignNodes); err != nil {
		t.Fatalf("upsert foreign node: %v", err)
	}
	storedForeign, err := store.GetNodesByFile(foreignCtx, "foreign.go")
	if err != nil || len(storedForeign) != 1 {
		t.Fatalf("load foreign node: nodes=%+v err=%v", storedForeign, err)
	}
	foreignAnnotation := &graph.Annotation{NodeID: storedForeign[0].ID, Summary: "foreign"}

	err = store.UpsertAnnotations(ctx, []*graph.Annotation{annotations[0], foreignAnnotation})
	if err == nil {
		t.Fatal("expected foreign-node batch to be rejected")
	}
	got, getErr := store.GetAnnotation(ctx, annotations[0].NodeID)
	if getErr != nil {
		t.Fatalf("get owned annotation after rejection: %v", getErr)
	}
	if got != nil {
		t.Fatalf("owned annotation was written before batch rejection: %+v", got)
	}
}
