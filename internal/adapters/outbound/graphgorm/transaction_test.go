package graphgorm

import (
	"context"
	"errors"
	"testing"

	"github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"gorm.io/gorm"
)

type transactionSearchWriter struct {
	db *gorm.DB
}

func (w transactionSearchWriter) RebuildAll(ctx context.Context) error {
	return w.db.WithContext(ctx).Create(&graph.SearchDocument{NodeID: 9001}).Error
}

func (w transactionSearchWriter) RebuildNodes(context.Context, []uint) error { return nil }

func TestUnitOfWork_UsesOneTransactionForGraphAndSearch(t *testing.T) {
	store := setupTestDB(t)
	var writerDB *gorm.DB
	uow := NewUnitOfWork(store.db, func(tx *gorm.DB) ingest.SearchWriter {
		writerDB = tx
		return transactionSearchWriter{db: tx}
	})

	err := uow.WithinTransaction(context.Background(), func(tx ingest.Transaction) error {
		graphStore, ok := tx.Graph().(*Store)
		if !ok {
			t.Fatalf("Graph() type = %T, want *graphgorm.Store", tx.Graph())
		}
		if graphStore.db != writerDB {
			t.Fatal("graph store and search writer do not share the same transaction handle")
		}
		if err := tx.Graph().UpsertNodes(context.Background(), []graph.Node{{
			QualifiedName: "pkg.Func", Kind: graph.NodeKindFunction, Name: "Func", FilePath: "a.go",
		}}); err != nil {
			return err
		}
		return tx.Search().RebuildAll(context.Background())
	})
	if err != nil {
		t.Fatalf("WithinTransaction: %v", err)
	}

	var nodeCount, searchCount int64
	if err := store.db.Model(&graph.Node{}).Count(&nodeCount).Error; err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if err := store.db.Model(&graph.SearchDocument{}).Count(&searchCount).Error; err != nil {
		t.Fatalf("count search documents: %v", err)
	}
	if nodeCount != 1 || searchCount != 1 {
		t.Fatalf("committed counts = nodes:%d search:%d, want 1/1", nodeCount, searchCount)
	}
}

func TestUnitOfWork_RollsBackGraphAndSearchOnCallbackError(t *testing.T) {
	store := setupTestDB(t)
	uow := NewUnitOfWork(store.db, func(tx *gorm.DB) ingest.SearchWriter {
		return transactionSearchWriter{db: tx}
	})
	wantErr := errors.New("stop ingest")

	err := uow.WithinTransaction(context.Background(), func(tx ingest.Transaction) error {
		if err := tx.Graph().UpsertNodes(context.Background(), []graph.Node{{
			QualifiedName: "pkg.Func", Kind: graph.NodeKindFunction, Name: "Func", FilePath: "a.go",
		}}); err != nil {
			return err
		}
		if err := tx.Search().RebuildAll(context.Background()); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("WithinTransaction error = %v, want %v", err, wantErr)
	}

	var nodeCount, searchCount int64
	if err := store.db.Model(&graph.Node{}).Count(&nodeCount).Error; err != nil {
		t.Fatalf("count nodes: %v", err)
	}
	if err := store.db.Model(&graph.SearchDocument{}).Count(&searchCount).Error; err != nil {
		t.Fatalf("count search documents: %v", err)
	}
	if nodeCount != 0 || searchCount != 0 {
		t.Fatalf("rolled-back counts = nodes:%d search:%d, want 0/0", nodeCount, searchCount)
	}
}

func TestUnitOfWork_RejectsMissingSearchWriterFactory(t *testing.T) {
	store := setupTestDB(t)
	uow := NewUnitOfWork(store.db, nil)
	called := false

	err := uow.WithinTransaction(context.Background(), func(ingest.Transaction) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("WithinTransaction succeeded without a search writer factory")
	}
	if called {
		t.Fatal("WithinTransaction invoked callback without a complete transaction")
	}
}
