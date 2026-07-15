// @index GORM-based graph repository that manages CRUD operations and transactions for nodes, edges, and annotations.
package graphgorm

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/app/ingest"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/domain/reference"
)

// Store is the GORM-backed GraphStore implementation.
// @intent implement the graph repository contract through a GORM DB handle.
type Store struct {
	db *gorm.DB
}

var _ ingest.GraphStore = (*Store)(nil)

// New creates a repository wrapping a GORM DB.
// @intent initialize the GraphStore implementation with the injected DB handle.
func New(db *gorm.DB) *Store {
	return &Store{db: db}
}

// AutoMigrate creates or updates the graph repository schema.
// @intent prepare the GORM model tables required for graph persistence.
// @sideEffect may modify the database schema.
func (s *Store) AutoMigrate() error {
	if err := s.db.AutoMigrate(
		&graph.Node{},
		&graph.Edge{},
		&graph.Annotation{},
		&graph.DocTag{},
		&graph.Community{},
		&graph.CommunityMembership{},
		&graph.Flow{},
		&graph.FlowMembership{},
	); err != nil {
		return err
	}
	if s.db.Migrator().HasIndex(&graph.Edge{}, "idx_edges_fingerprint") {
		if err := s.db.Migrator().DropIndex(&graph.Edge{}, "idx_edges_fingerprint"); err != nil {
			return trace.Wrap(err, "drop legacy edge fingerprint index")
		}
	}
	return nil
}

// UpsertNodes stores a batch of nodes keyed by qualified_name.
// @intent apply parsed result nodes in bulk without creating duplicates.
// @sideEffect performs batch inserts and updates on the nodes table.
// @requires nodes must have a non-empty QualifiedName.
// @ensures existing nodes with the same qualified_name are updated with the latest metadata.
func (s *Store) UpsertNodes(ctx context.Context, nodes []graph.Node) error {
	if len(nodes) == 0 {
		return nil
	}
	ns := requestctx.FromContext(ctx)
	for i := range nodes {
		nodes[i].Namespace = ns
	}
	err := s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "namespace"}, {Name: "qualified_name"}, {Name: "file_path"}, {Name: "start_line"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"kind", "name", "end_line", "hash", "language",
			}),
		}).
		CreateInBatches(nodes, 100).Error
	if err != nil {
		return trace.Wrap(err, "batch upsert nodes")
	}
	return nil
}

// GetNode retrieves a single node by qualified name.
// @intent find one node by the declaration's qualified name.
// @return returns nil when no node exists.
func (s *Store) GetNode(ctx context.Context, qualifiedName string) (*graph.Node, error) {
	var node graph.Node
	ns := requestctx.FromContext(ctx)
	result := s.db.WithContext(ctx).Where("namespace = ? AND qualified_name = ?", ns, qualifiedName).First(&node)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, trace.Wrap(result.Error, "get node by qualified name")
	}
	return &node, nil
}

// GetNodeByID retrieves a single node by primary key.
// @intent find one node by its internal identifier.
// @return returns nil when no node exists.
func (s *Store) GetNodeByID(ctx context.Context, id uint) (*graph.Node, error) {
	var node graph.Node
	ns := requestctx.FromContext(ctx)
	result := s.db.WithContext(ctx).Where("namespace = ? AND id = ?", ns, id).First(&node)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, trace.Wrap(result.Error, "get node by id")
	}
	return &node, nil
}

// GetNodesByIDs retrieves nodes matching the provided ID list.
// @intent load multiple nodes at once by internal identifier.
// @return returns a nil slice when ids is empty.
func (s *Store) GetNodesByIDs(ctx context.Context, ids []uint) ([]graph.Node, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var nodes []graph.Node
	ns := requestctx.FromContext(ctx)
	if err := s.db.WithContext(ctx).Where("namespace = ? AND id IN ?", ns, ids).Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

// GetNodesByQualifiedNames loads a set of names into a node map.
// @intent build a fast lookup map for qualified-name-based reference resolution.
// @return returns a map keyed by QualifiedName, with slices containing all nodes for each name.
func (s *Store) GetNodesByQualifiedNames(ctx context.Context, names []string) (map[string][]graph.Node, error) {
	if len(names) == 0 {
		return map[string][]graph.Node{}, nil
	}
	ns := requestctx.FromContext(ctx)
	var nodes []graph.Node
	if err := s.db.WithContext(ctx).Where("namespace = ? AND qualified_name IN ?", ns, names).Find(&nodes).Error; err != nil {
		return nil, err
	}
	result := make(map[string][]graph.Node, len(nodes))
	for _, node := range nodes {
		result[node.QualifiedName] = append(result[node.QualifiedName], node)
	}
	return result, nil
}

// GetNodesByFile retrieves nodes belonging to a file path.
// @intent load declarations parsed from a specific source file.
func (s *Store) GetNodesByFile(ctx context.Context, filePath string) ([]graph.Node, error) {
	ns := requestctx.FromContext(ctx)
	var nodes []graph.Node
	if err := s.db.WithContext(ctx).Where("namespace = ? AND file_path = ?", ns, filePath).Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

// GetNodesByFiles retrieves nodes from multiple files grouped by file path.
// @intent return declarations for a file set grouped by path.
// @return returns a map whose keys are file paths and values are the nodes in each file.
func (s *Store) GetNodesByFiles(ctx context.Context, filePaths []string) (map[string][]graph.Node, error) {
	if len(filePaths) == 0 {
		return map[string][]graph.Node{}, nil
	}
	ns := requestctx.FromContext(ctx)
	var nodes []graph.Node
	if err := s.db.WithContext(ctx).Where("namespace = ? AND file_path IN ?", ns, filePaths).Find(&nodes).Error; err != nil {
		return nil, err
	}
	result := make(map[string][]graph.Node, len(filePaths))
	for _, n := range nodes {
		result[n.FilePath] = append(result[n.FilePath], n)
	}
	return result, nil
}

// ListFileNodes returns the minimal persisted state used to compare source files during an update.
// @intent expose namespace-scoped file identity and hash state without leaking the database handle.
func (s *Store) ListFileNodes(ctx context.Context) ([]graph.Node, error) {
	ns := requestctx.FromContext(ctx)
	var nodes []graph.Node
	if err := s.db.WithContext(ctx).
		Model(&graph.Node{}).
		Select("id", "file_path", "hash").
		Where("namespace = ? AND kind <> ?", ns, graph.NodeKindPackage).
		Find(&nodes).Error; err != nil {
		return nil, trace.Wrap(err, "list file node state")
	}
	return nodes, nil
}

// ListImportFileNodes returns actual file nodes for build-scoped import-path resolution.
// @intent let full builds create an in-memory import suffix index without reloading all file nodes per import.
func (s *Store) ListImportFileNodes(ctx context.Context) ([]graph.Node, error) {
	ns := requestctx.FromContext(ctx)
	var nodes []graph.Node
	if err := s.db.WithContext(ctx).
		Where("namespace = ? AND kind = ?", ns, graph.NodeKindFile).
		Find(&nodes).Error; err != nil {
		return nil, trace.Wrap(err, "list import file nodes")
	}
	return nodes, nil
}

// GetFileNodesByPathSuffix finds file nodes whose directory matches an import-path suffix.
// @intent let import edge resolution bind repo-local import paths back to stored file nodes.
func (s *Store) GetFileNodesByPathSuffix(ctx context.Context, suffix string) ([]graph.Node, error) {
	suffix = strings.Trim(path.Clean(strings.TrimSpace(suffix)), "/")
	if suffix == "" || suffix == "." {
		return nil, nil
	}
	ns := requestctx.FromContext(ctx)
	var nodes []graph.Node
	if err := s.db.WithContext(ctx).
		Where("namespace = ? AND kind = ?", ns, graph.NodeKindFile).
		Find(&nodes).Error; err != nil {
		return nil, err
	}
	var out []graph.Node
	var exact []graph.Node
	bestDepth := -1
	for _, node := range nodes {
		dir := strings.Trim(path.Dir(node.FilePath), "/")
		if dir == "." || dir == "" {
			continue
		}
		if suffix == dir {
			exact = append(exact, node)
			continue
		}
		if depth := reference.CommonSuffixDepth(suffix, dir); depth > 0 {
			if depth > bestDepth {
				bestDepth = depth
				out = []graph.Node{node}
				continue
			}
			if depth == bestDepth {
				out = append(out, node)
			}
		}
	}
	if len(exact) > 0 {
		return exact, nil
	}
	return out, nil
}

// DeleteNodesByFile removes nodes in a file and their related data.
// @intent clean out prior nodes, edges, and annotations before reparsing a file.
// @sideEffect deletes related records from the nodes, edges, annotations, and doc_tags tables.
// @domainRule connected edges and annotations must also be removed when deleting a file.
func (s *Store) DeleteNodesByFile(ctx context.Context, filePath string) error {
	ns := requestctx.FromContext(ctx)
	var nodeIDs []uint
	if err := s.db.WithContext(ctx).
		Model(&graph.Node{}).
		Where("namespace = ? AND file_path = ?", ns, filePath).
		Pluck("id", &nodeIDs).Error; err != nil {
		return trace.Wrap(err, "pluck node ids")
	}
	if len(nodeIDs) == 0 {
		return nil
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.
			Where("namespace = ? AND file_path = ?", ns, filePath).
			Delete(&graph.Edge{}).Error; err != nil {
			return trace.Wrap(err, "delete file-owned edges")
		}

		if err := tx.
			Where("from_node_id IN ? OR to_node_id IN ?", nodeIDs, nodeIDs).
			Delete(&graph.Edge{}).Error; err != nil {
			return trace.Wrap(err, "cascade delete edges")
		}

		if err := tx.
			Where("annotation_id IN (?)",
				tx.Model(&graph.Annotation{}).Select("id").Where("node_id IN ?", nodeIDs),
			).Delete(&graph.DocTag{}).Error; err != nil {
			return trace.Wrap(err, "cascade delete doc_tags")
		}

		if err := tx.
			Where("node_id IN ?", nodeIDs).
			Delete(&graph.Annotation{}).Error; err != nil {
			return trace.Wrap(err, "cascade delete annotations")
		}

		if err := tx.
			Where("node_id IN ?", nodeIDs).
			Delete(&graph.CommunityMembership{}).Error; err != nil {
			return trace.Wrap(err, "cascade delete community memberships")
		}

		if tx.Migrator().HasTable(&graph.FlowMembership{}) {
			if err := tx.
				Where("node_id IN ?", nodeIDs).
				Delete(&graph.FlowMembership{}).Error; err != nil {
				return trace.Wrap(err, "cascade delete flow memberships")
			}
		}

		if tx.Migrator().HasTable(&graph.SearchDocument{}) {
			if err := tx.
				Where("node_id IN ?", nodeIDs).
				Delete(&graph.SearchDocument{}).Error; err != nil {
				return trace.Wrap(err, "cascade delete search_documents")
			}
		}

		return tx.Where("id IN ?", nodeIDs).Delete(&graph.Node{}).Error
	})
}

// DeleteGraph removes the entire graph state for the current namespace.
// @intent replace namespace-scoped state before a full rebuild or include_paths rebuild.
// @sideEffect deletes all nodes, edges, annotations, and doc_tags in the namespace.
func (s *Store) DeleteGraph(ctx context.Context) error {
	ns := requestctx.FromContext(ctx)
	var nodeIDs []uint
	var filePaths []string
	if err := s.db.WithContext(ctx).
		Model(&graph.Node{}).
		Where("namespace = ?", ns).
		Pluck("id", &nodeIDs).Error; err != nil {
		return trace.Wrap(err, "pluck namespace node ids")
	}
	if err := s.db.WithContext(ctx).
		Model(&graph.Node{}).
		Where("namespace = ?", ns).
		Distinct().
		Pluck("file_path", &filePaths).Error; err != nil {
		return trace.Wrap(err, "pluck namespace file paths")
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(filePaths) > 0 {
			if err := tx.
				Where("namespace = ? AND file_path IN ?", ns, filePaths).
				Delete(&graph.Edge{}).Error; err != nil {
				return trace.Wrap(err, "delete namespace file-owned edges")
			}
		}

		if len(nodeIDs) > 0 {
			if err := tx.
				Where("annotation_id IN (?)",
					tx.Model(&graph.Annotation{}).Select("id").Where("node_id IN ?", nodeIDs),
				).Delete(&graph.DocTag{}).Error; err != nil {
				return trace.Wrap(err, "delete namespace doc_tags")
			}

			if err := tx.
				Where("node_id IN ?", nodeIDs).
				Delete(&graph.Annotation{}).Error; err != nil {
				return trace.Wrap(err, "delete namespace annotations")
			}

			if err := tx.
				Where("node_id IN ?", nodeIDs).
				Delete(&graph.CommunityMembership{}).Error; err != nil {
				return trace.Wrap(err, "delete namespace community memberships")
			}

			if tx.Migrator().HasTable(&graph.FlowMembership{}) {
				flowIDs := tx.Model(&graph.Flow{}).Select("id").Where("namespace = ?", ns)
				if err := tx.
					Where("node_id IN ? OR flow_id IN (?)", nodeIDs, flowIDs).
					Delete(&graph.FlowMembership{}).Error; err != nil {
					return trace.Wrap(err, "delete namespace flow memberships")
				}
			}

			if tx.Migrator().HasTable(&graph.SearchDocument{}) {
				if err := tx.
					Where("node_id IN ?", nodeIDs).
					Delete(&graph.SearchDocument{}).Error; err != nil {
					return trace.Wrap(err, "delete namespace search_documents")
				}
			}

			if err := tx.
				Where("from_node_id IN ? OR to_node_id IN ?", nodeIDs, nodeIDs).
				Delete(&graph.Edge{}).Error; err != nil {
				return trace.Wrap(err, "delete namespace connected edges")
			}

			if err := tx.
				Where("id IN ?", nodeIDs).
				Delete(&graph.Node{}).Error; err != nil {
				return trace.Wrap(err, "delete namespace nodes")
			}
		}

		return nil
	})
}

// UpsertEdges stores a batch of edges keyed by fingerprint.
// @intent apply graph relationships in bulk without duplicates.
// @sideEffect performs batch inserts on the edges table.
// @domainRule edges with the same fingerprint must be stored only once.
func (s *Store) UpsertEdges(ctx context.Context, edges []graph.Edge) error {
	if len(edges) == 0 {
		return nil
	}
	ns := requestctx.FromContext(ctx)
	for i := range edges {
		edges[i].Namespace = ns
	}
	if err := s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "namespace"}, {Name: "fingerprint"}},
			DoNothing: true,
		}).
		CreateInBatches(edges, 500).Error; err != nil {
		return trace.Wrap(err, "upsert edge batch")
	}
	return nil
}

// GetEdgesFrom retrieves outgoing edges from a node.
// @intent load outbound relationships for a specific declaration.
func (s *Store) GetEdgesFrom(ctx context.Context, nodeID uint) ([]graph.Edge, error) {
	var edges []graph.Edge
	ns := requestctx.FromContext(ctx)
	if err := s.db.WithContext(ctx).
		Where("namespace = ? AND from_node_id = ?", ns, nodeID).
		Order("file_path ASC").
		Order("line ASC").
		Order("fingerprint ASC").
		Order("id ASC").
		Find(&edges).Error; err != nil {
		return nil, err
	}
	return edges, nil
}

// GetEdgesFromNodes retrieves outgoing edges from multiple nodes.
// @intent load outbound relationships for multiple declarations in one call.
// @return returns a nil slice when nodeIDs is empty.
func (s *Store) GetEdgesFromNodes(ctx context.Context, nodeIDs []uint) ([]graph.Edge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	var edges []graph.Edge
	ns := requestctx.FromContext(ctx)
	if err := s.db.WithContext(ctx).
		Where("namespace = ? AND from_node_id IN ?", ns, nodeIDs).
		Order("file_path ASC").
		Order("line ASC").
		Order("fingerprint ASC").
		Order("id ASC").
		Find(&edges).Error; err != nil {
		return nil, err
	}
	return edges, nil
}

// GetEdgesTo retrieves incoming edges to a node.
// @intent load inbound relationships for a specific declaration.
func (s *Store) GetEdgesTo(ctx context.Context, nodeID uint) ([]graph.Edge, error) {
	var edges []graph.Edge
	ns := requestctx.FromContext(ctx)
	if err := s.db.WithContext(ctx).
		Where("namespace = ? AND to_node_id = ?", ns, nodeID).
		Order("file_path ASC").
		Order("line ASC").
		Order("fingerprint ASC").
		Order("id ASC").
		Find(&edges).Error; err != nil {
		return nil, err
	}
	return edges, nil
}

// GetEdgesToNodes retrieves incoming edges to multiple nodes.
// @intent load inbound relationships for multiple declarations in one call.
// @return returns a nil slice when nodeIDs is empty.
func (s *Store) GetEdgesToNodes(ctx context.Context, nodeIDs []uint) ([]graph.Edge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	var edges []graph.Edge
	ns := requestctx.FromContext(ctx)
	if err := s.db.WithContext(ctx).
		Where("namespace = ? AND to_node_id IN ?", ns, nodeIDs).
		Order("file_path ASC").
		Order("line ASC").
		Order("fingerprint ASC").
		Order("id ASC").
		Find(&edges).Error; err != nil {
		return nil, err
	}
	return edges, nil
}

// DeleteEdgesByFile removes edges generated from a file.
// @intent selectively clean existing relationships during file-scoped updates.
// @sideEffect deletes matching file_path records from the edges table.
func (s *Store) DeleteEdgesByFile(ctx context.Context, filePath string) error {
	ns := requestctx.FromContext(ctx)
	return s.db.WithContext(ctx).Where("namespace = ? AND file_path = ?", ns, filePath).Delete(&graph.Edge{}).Error
}

// DeletePackageSemanticEdges removes synthesized package-level implementation edges for anchor files.
// @intent replace stale package semantic relationships without exposing persistence queries to the application layer.
// @sideEffect deletes namespace-scoped edge rows matching the supplied anchors.
// @domainRule only synthesized implements edges, identified by line zero, are eligible for deletion.
func (s *Store) DeletePackageSemanticEdges(ctx context.Context, anchors []string) error {
	if len(anchors) == 0 {
		return nil
	}
	ns := requestctx.FromContext(ctx)
	if err := s.db.WithContext(ctx).
		Where("namespace = ? AND kind = ? AND line = ? AND file_path IN ?", ns, graph.EdgeKindImplements, 0, anchors).
		Delete(&graph.Edge{}).Error; err != nil {
		return trace.Wrap(err, "delete package semantic edges")
	}
	return nil
}

// UpsertAnnotation stores a node's annotation and tags.
// @intent replace structured comments for each node with the latest state.
// @sideEffect performs inserts, updates, and deletes on the annotations and doc_tags tables.
// @mutates may overwrite ann.ID with the existing record ID.
// @domainRule only one annotation must be kept per node_id.
func (s *Store) UpsertAnnotation(ctx context.Context, ann *graph.Annotation) error {
	var existing graph.Annotation
	ns := requestctx.FromContext(ctx)
	result := s.db.WithContext(ctx).
		Joins("JOIN nodes ON nodes.id = annotations.node_id").
		Where("annotations.node_id = ? AND nodes.namespace = ?", ann.NodeID, ns).
		First(&existing)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// The read path is namespace-scoped; the create path must be too, or a caller
			// could attach an annotation to another namespace's node (or a missing node).
			var owned int64
			if err := tx.Model(&graph.Node{}).
				Where("id = ? AND namespace = ?", ann.NodeID, ns).
				Count(&owned).Error; err != nil {
				return trace.Wrap(err, "verify annotation node namespace")
			}
			if owned == 0 {
				return fmt.Errorf("annotation node %d not found in namespace %q", ann.NodeID, ns)
			}
			return tx.Create(ann).Error
		})
	}
	if result.Error != nil {
		return result.Error
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("annotation_id = ?", existing.ID).Delete(&graph.DocTag{}).Error; err != nil {
			return trace.Wrap(err, "delete doc tags")
		}
		ann.ID = existing.ID
		return tx.Save(ann).Error
	})
}

// GetAnnotation retrieves the annotation attached to a node ID.
// @intent load a node's structured comment and tags together for search and display.
// @return returns nil when no annotation exists.
func (s *Store) GetAnnotation(ctx context.Context, nodeID uint) (*graph.Annotation, error) {
	var ann graph.Annotation
	ns := requestctx.FromContext(ctx)
	result := s.db.WithContext(ctx).
		Preload("Tags").
		Joins("JOIN nodes ON nodes.id = annotations.node_id").
		Where("annotations.node_id = ? AND nodes.namespace = ?", nodeID, ns).
		First(&ann)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, trace.Wrap(result.Error, "get annotation")
	}
	return &ann, nil
}

// WithTx executes the given function inside the same transaction.
// @intent allow multiple repository operations to run atomically as one unit.
// @sideEffect starts a database transaction and commits or rolls it back.
// @ensures commits the transaction when fn returns a nil error.
func (s *Store) WithTx(ctx context.Context, fn func(store ingest.GraphStore) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txStore := New(tx)
		return fn(txStore)
	})
}

// WithTxDB passes the transaction DB handle together with the repository.
// @intent let graph persistence and DB-backed derived-state updates share a single transaction.
// @sideEffect starts a database transaction and commits or rolls it back.
func (s *Store) WithTxDB(ctx context.Context, fn func(ingest.GraphStore, *gorm.DB) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txStore := New(tx)
		return fn(txStore, tx)
	})
}
