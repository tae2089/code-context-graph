package cli

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

type evalSpySearchBackend struct {
	queryFn func(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error)
}

func (s *evalSpySearchBackend) Migrate(db *gorm.DB) error {
	return nil
}

func (s *evalSpySearchBackend) Rebuild(ctx context.Context, db *gorm.DB) error {
	return nil
}

func (s *evalSpySearchBackend) PurgeNamespace(ctx context.Context, db *gorm.DB) error {
	return nil
}

func (s *evalSpySearchBackend) Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error) {
	if s.queryFn != nil {
		return s.queryFn(ctx, db, query, limit)
	}
	return nil, nil
}

func TestMakeSearchFn_UsesProvidedNamespaceContext(t *testing.T) {
	deps, _, _ := newTestDeps()
	deps.DB = &gorm.DB{}
	deps.SearchBackend = &evalSpySearchBackend{queryFn: func(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error) {
		if got := ctxns.FromContext(ctx); got != "alpha" {
			t.Fatalf("query context namespace = %q, want %q", got, "alpha")
		}
		return []model.Node{{Kind: model.NodeKindFunction, Name: "Foo", FilePath: "foo.go"}}, nil
	}}

	searchFn := makeSearchFn(ctxns.WithNamespace(context.Background(), "alpha"), deps)
	if _, err := searchFn("foo", 5); err != nil {
		t.Fatalf("searchFn returned error: %v", err)
	}
}
