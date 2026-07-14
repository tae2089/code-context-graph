// @index Search-document refresh and FTS content generation.
package searchsql

import (
	"context"
	"log/slog"
	"strconv"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/graphgorm"
	"github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/app/search/document"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

const scopedINQueryChunkSize = 400

// Writer updates derived search documents and the configured search backend through one DB handle.
// @intent provide a transaction-scoped SearchWriter implementation for ingest unit-of-work adapters.
type Writer struct {
	db      *gorm.DB
	backend Backend
	logger  *slog.Logger
}

var _ document.Maintenance = (*Writer)(nil)

// NewSearchWriter binds derived search updates to the supplied database handle.
// @intent construct a search writer that can share an ingest transaction with graph persistence.
func NewSearchWriter(db *gorm.DB, backend Backend, logger *slog.Logger) *Writer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Writer{db: db, backend: backend, logger: logger}
}

// NewIngestUnitOfWork composes transaction-scoped graph and search adapters for ingest workflows.
// @intent keep raw GORM transaction wiring at the outbound composition boundary.
func NewIngestUnitOfWork(db *gorm.DB, backend Backend, logger *slog.Logger) ingest.UnitOfWork {
	return graphgorm.NewUnitOfWork(db, func(tx *gorm.DB) ingest.SearchWriter {
		return NewSearchWriter(tx, backend, logger)
	})
}

// RebuildAll refreshes every namespace-scoped search document and rebuilds the backend index.
// @intent implement the full derived-search refresh required by a graph build.
// @sideEffect rewrites search documents and the backend index through the bound DB handle.
func (w *Writer) RebuildAll(ctx context.Context) error {
	if w == nil || w.backend == nil || w.db == nil {
		return nil
	}
	docCount, err := RefreshSearchDocuments(ctx, w.db)
	if err != nil {
		return err
	}
	if err := w.backend.Rebuild(ctx, w.db); err != nil {
		return trace.Wrap(err, "rebuild search index")
	}
	w.logger.Info("search index rebuilt", "documents", docCount)
	return nil
}

// RefreshDocuments refreshes derived search documents and returns their count.
// @intent implement the first application maintenance stage without exposing the database handle.
func (w *Writer) RefreshDocuments(ctx context.Context) (int, error) {
	if w == nil || w.backend == nil || w.db == nil {
		return 0, nil
	}
	return RefreshSearchDocuments(ctx, w.db)
}

// RebuildIndex rebuilds the configured full-text backend after documents refresh.
// @intent implement the second application maintenance stage without exposing backend or database handles.
func (w *Writer) RebuildIndex(ctx context.Context) error {
	if w == nil || w.backend == nil || w.db == nil {
		return nil
	}
	return w.backend.Rebuild(ctx, w.db)
}

// RebuildNodes refreshes only the supplied node IDs and updates the matching backend scope.
// @intent implement the incremental derived-search refresh required by graph updates.
// @sideEffect rewrites scoped search documents and the backend index through the bound DB handle.
func (w *Writer) RebuildNodes(ctx context.Context, nodeIDs []uint) error {
	if w == nil || w.backend == nil || w.db == nil || len(nodeIDs) == 0 {
		return nil
	}
	docCount, err := RefreshSearchDocumentsFor(ctx, w.db, nodeIDs)
	if err != nil {
		return err
	}
	if err := w.backend.RebuildNodes(ctx, w.db, nodeIDs); err != nil {
		return trace.Wrap(err, "rebuild scoped search index")
	}
	w.logger.Info("search index partially rebuilt", "documents", docCount, "nodes", len(nodeIDs))
	return nil
}

// RefreshSearchDocuments rebuilds namespace-scoped search_documents from current graph nodes.
// @intent keep derived search documents consistent with graph state before FTS rebuilds
func RefreshSearchDocuments(ctx context.Context, db *gorm.DB) (int, error) {
	if db == nil {
		return 0, trace.New("search document refresh requires db handle")
	}
	return refreshSearchDocuments(ctx, db, nil, false)
}

// RefreshSearchDocumentsFor rebuilds search_documents for the specified node IDs only.
// @intent incremental update 경로에서 영향받은 문서만 갱신한다.
func RefreshSearchDocumentsFor(ctx context.Context, db *gorm.DB, nodeIDs []uint) (int, error) {
	if len(nodeIDs) == 0 {
		return 0, nil
	}
	if db == nil {
		return 0, trace.New("search document refresh requires db handle")
	}
	return refreshSearchDocuments(ctx, db, nodeIDs, true)
}

// refreshSearchDocuments rebuilds the namespace-scoped search_documents table either fully or for a node-id scope.
// @intent regenerate FTS content from the latest nodes and annotations in batches to bound memory.
// @sideEffect deletes and re-inserts rows in search_documents within a DB transaction.
// @mutates search_documents
func refreshSearchDocuments(ctx context.Context, db *gorm.DB, nodeIDs []uint, scoped bool) (int, error) {
	ns := requestctx.FromContext(ctx)
	count := 0
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		docsQ := tx.WithContext(ctx).Where("namespace = ?", ns)
		nodesQ := tx.WithContext(ctx).
			Where("kind IN ?", []string{"function", "class", "type", "test", "file"}).
			Where("namespace = ?", ns)
		if scoped {
			for start := 0; start < len(nodeIDs); start += scopedINQueryChunkSize {
				chunk := scopedNodeIDsForChunk(nodeIDs, start)
				if err := tx.WithContext(ctx).Where("namespace = ?", ns).Where("node_id IN ?", chunk).Delete(&graph.SearchDocument{}).Error; err != nil {
					return trace.Wrap(err, "clear search documents")
				}
			}
		} else {
			if err := docsQ.Delete(&graph.SearchDocument{}).Error; err != nil {
				return trace.Wrap(err, "clear search documents")
			}
		}

		loadNodes := func(query *gorm.DB) error {
			var batchNodes []graph.Node
			result := query.FindInBatches(&batchNodes, 500, func(batchTx *gorm.DB, batch int) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				_ = batchTx
				nodeIDs := make([]uint, len(batchNodes))
				for i, n := range batchNodes {
					nodeIDs[i] = n.ID
				}
				annByNode := map[uint]*graph.Annotation{}
				if len(nodeIDs) > 0 {
					var annotations []graph.Annotation
					annQ := tx.Session(&gorm.Session{NewDB: true}).WithContext(ctx).Model(&graph.Annotation{})
					if err := annQ.Where("node_id IN ?", nodeIDs).Find(&annotations).Error; err != nil {
						return trace.Wrap(err, "load annotations batch "+strconv.Itoa(batch))
					}
					if len(annotations) > 0 {
						annotationIDs := make([]uint, len(annotations))
						for i := range annotations {
							annotationIDs[i] = annotations[i].ID
						}
						var tags []graph.DocTag
						tagsQ := tx.Session(&gorm.Session{NewDB: true}).WithContext(ctx).Model(&graph.DocTag{})
						if err := tagsQ.Where("annotation_id IN ?", annotationIDs).Order("annotation_id, ordinal").Find(&tags).Error; err != nil {
							return trace.Wrap(err, "load annotation tags batch "+strconv.Itoa(batch))
						}
						tagsByAnnotation := make(map[uint][]graph.DocTag, len(annotations))
						for _, tag := range tags {
							tagsByAnnotation[tag.AnnotationID] = append(tagsByAnnotation[tag.AnnotationID], tag)
						}
						for i := range annotations {
							annotations[i].Tags = tagsByAnnotation[annotations[i].ID]
						}
					}
					for i := range annotations {
						annByNode[annotations[i].NodeID] = &annotations[i]
					}
				}
				docs := make([]graph.SearchDocument, 0, len(batchNodes))
				for _, n := range batchNodes {
					docs = append(docs, graph.SearchDocument{
						Namespace: n.Namespace,
						NodeID:    n.ID,
						Content:   document.BuildContent(n, annByNode),
						Language:  n.Language,
					})
				}
				if len(docs) > 0 {
					if err := tx.WithContext(ctx).CreateInBatches(docs, 100).Error; err != nil {
						return trace.Wrap(err, "batch insert search documents")
					}
				}
				count += len(docs)
				return nil
			})
			if result.Error != nil {
				return trace.Wrap(result.Error, "load index nodes")
			}
			return nil
		}

		if scoped {
			for start := 0; start < len(nodeIDs); start += scopedINQueryChunkSize {
				chunk := scopedNodeIDsForChunk(nodeIDs, start)
				chunkNodesQ := tx.WithContext(ctx).
					Where("kind IN ?", []string{"function", "class", "type", "test", "file"}).
					Where("namespace = ?", ns).
					Where("id IN ?", chunk)
				if err := loadNodes(chunkNodesQ); err != nil {
					return err
				}
			}
			return nil
		}
		return loadNodes(nodesQ)
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// scopedNodeIDsForChunk slices a node ID list using the configured IN-query chunk size.
// @intent keep search rebuild SQL within the SQLite/Postgres parameter limit.
func scopedNodeIDsForChunk(nodeIDs []uint, start int) []uint {
	end := min(start+scopedINQueryChunkSize, len(nodeIDs))
	return nodeIDs[start:end]
}
