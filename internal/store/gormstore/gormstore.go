// @index GORM-based graph repository that manages CRUD operations and transactions for nodes, edges, and annotations.
package gormstore

import (
	"context"
	"errors"
	"path"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/store"
)

// Store is the GORM-backed GraphStore implementation.
// @intent implement the graph repository contract through a GORM DB handle.
type Store struct {
	db *gorm.DB
}

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
		&model.Node{},
		&model.Edge{},
		&model.Annotation{},
		&model.DocTag{},
		&model.Community{},
		&model.CommunityMembership{},
		&model.Flow{},
		&model.FlowMembership{},
		&model.PostprocessPolicyState{},
		&model.PostprocessRunLog{},
	); err != nil {
		return err
	}
	if s.db.Migrator().HasIndex(&model.Edge{}, "idx_edges_fingerprint") {
		if err := s.db.Migrator().DropIndex(&model.Edge{}, "idx_edges_fingerprint"); err != nil {
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
func (s *Store) UpsertNodes(ctx context.Context, nodes []model.Node) error {
	if len(nodes) == 0 {
		return nil
	}
	ns := ctxns.FromContext(ctx)
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
func (s *Store) GetNode(ctx context.Context, qualifiedName string) (*model.Node, error) {
	var node model.Node
	ns := ctxns.FromContext(ctx)
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
func (s *Store) GetNodeByID(ctx context.Context, id uint) (*model.Node, error) {
	var node model.Node
	ns := ctxns.FromContext(ctx)
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
func (s *Store) GetNodesByIDs(ctx context.Context, ids []uint) ([]model.Node, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var nodes []model.Node
	ns := ctxns.FromContext(ctx)
	if err := s.db.WithContext(ctx).Where("namespace = ? AND id IN ?", ns, ids).Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

// GetNodesByQualifiedNames loads a set of names into a node map.
// @intent build a fast lookup map for qualified-name-based reference resolution.
// @return returns a map keyed by QualifiedName, with slices containing all nodes for each name.
func (s *Store) GetNodesByQualifiedNames(ctx context.Context, names []string) (map[string][]model.Node, error) {
	if len(names) == 0 {
		return map[string][]model.Node{}, nil
	}
	ns := ctxns.FromContext(ctx)
	var nodes []model.Node
	if err := s.db.WithContext(ctx).Where("namespace = ? AND qualified_name IN ?", ns, names).Find(&nodes).Error; err != nil {
		return nil, err
	}
	result := make(map[string][]model.Node, len(nodes))
	for _, node := range nodes {
		result[node.QualifiedName] = append(result[node.QualifiedName], node)
	}
	return result, nil
}

// GetNodesByFile retrieves nodes belonging to a file path.
// @intent load declarations parsed from a specific source file.
func (s *Store) GetNodesByFile(ctx context.Context, filePath string) ([]model.Node, error) {
	ns := ctxns.FromContext(ctx)
	var nodes []model.Node
	if err := s.db.WithContext(ctx).Where("namespace = ? AND file_path = ?", ns, filePath).Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

// GetNodesByFiles retrieves nodes from multiple files grouped by file path.
// @intent return declarations for a file set grouped by path.
// @return returns a map whose keys are file paths and values are the nodes in each file.
func (s *Store) GetNodesByFiles(ctx context.Context, filePaths []string) (map[string][]model.Node, error) {
	if len(filePaths) == 0 {
		return map[string][]model.Node{}, nil
	}
	ns := ctxns.FromContext(ctx)
	var nodes []model.Node
	if err := s.db.WithContext(ctx).Where("namespace = ? AND file_path IN ?", ns, filePaths).Find(&nodes).Error; err != nil {
		return nil, err
	}
	result := make(map[string][]model.Node, len(filePaths))
	for _, n := range nodes {
		result[n.FilePath] = append(result[n.FilePath], n)
	}
	return result, nil
}

// GetFileNodesByPathSuffix finds file nodes whose directory matches an import-path suffix.
// @intent let import edge resolution bind repo-local import paths back to stored file nodes.
func (s *Store) GetFileNodesByPathSuffix(ctx context.Context, suffix string) ([]model.Node, error) {
	suffix = strings.Trim(path.Clean(strings.TrimSpace(suffix)), "/")
	if suffix == "" || suffix == "." {
		return nil, nil
	}
	ns := ctxns.FromContext(ctx)
	var nodes []model.Node
	if err := s.db.WithContext(ctx).
		Where("namespace = ? AND kind = ?", ns, model.NodeKindFile).
		Find(&nodes).Error; err != nil {
		return nil, err
	}
	var out []model.Node
	var exact []model.Node
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
		if depth := commonPathSuffixDepth(suffix, dir); depth > 0 {
			if depth > bestDepth {
				bestDepth = depth
				out = []model.Node{node}
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

// commonPathSuffixDepth calculates the depth of common directory suffix between two paths.
// @intent identify the best matching directory for an import path based on trailing segments.
func commonPathSuffixDepth(a, b string) int {
	a = strings.Trim(a, "/")
	b = strings.Trim(b, "/")
	if a == "" || b == "" {
		return 0
	}
	aParts := strings.Split(a, "/")
	bParts := strings.Split(b, "/")
	depth := 0
	for i, j := len(aParts)-1, len(bParts)-1; i >= 0 && j >= 0; i, j = i-1, j-1 {
		if aParts[i] != bParts[j] {
			break
		}
		depth++
	}
	return depth
}

// DeleteNodesByFile removes nodes in a file and their related data.
// @intent clean out prior nodes, edges, and annotations before reparsing a file.
// @sideEffect deletes related records from the nodes, edges, annotations, and doc_tags tables.
// @domainRule connected edges and annotations must also be removed when deleting a file.
func (s *Store) DeleteNodesByFile(ctx context.Context, filePath string) error {
	ns := ctxns.FromContext(ctx)
	var nodeIDs []uint
	if err := s.db.WithContext(ctx).
		Model(&model.Node{}).
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
			Delete(&model.Edge{}).Error; err != nil {
			return trace.Wrap(err, "delete file-owned edges")
		}

		if err := tx.
			Where("from_node_id IN ? OR to_node_id IN ?", nodeIDs, nodeIDs).
			Delete(&model.Edge{}).Error; err != nil {
			return trace.Wrap(err, "cascade delete edges")
		}

		if err := tx.
			Where("annotation_id IN (?)",
				tx.Model(&model.Annotation{}).Select("id").Where("node_id IN ?", nodeIDs),
			).Delete(&model.DocTag{}).Error; err != nil {
			return trace.Wrap(err, "cascade delete doc_tags")
		}

		if err := tx.
			Where("node_id IN ?", nodeIDs).
			Delete(&model.Annotation{}).Error; err != nil {
			return trace.Wrap(err, "cascade delete annotations")
		}

		if err := tx.
			Where("node_id IN ?", nodeIDs).
			Delete(&model.CommunityMembership{}).Error; err != nil {
			return trace.Wrap(err, "cascade delete community memberships")
		}

		if tx.Migrator().HasTable(&model.FlowMembership{}) {
			if err := tx.
				Where("node_id IN ?", nodeIDs).
				Delete(&model.FlowMembership{}).Error; err != nil {
				return trace.Wrap(err, "cascade delete flow memberships")
			}
		}

		if tx.Migrator().HasTable(&model.SearchDocument{}) {
			if err := tx.
				Where("node_id IN ?", nodeIDs).
				Delete(&model.SearchDocument{}).Error; err != nil {
				return trace.Wrap(err, "cascade delete search_documents")
			}
		}

		return tx.Where("id IN ?", nodeIDs).Delete(&model.Node{}).Error
	})
}

// DeleteGraph removes the entire graph state for the current namespace.
// @intent replace namespace-scoped state before a full rebuild or include_paths rebuild.
// @sideEffect deletes all nodes, edges, annotations, and doc_tags in the namespace.
func (s *Store) DeleteGraph(ctx context.Context) error {
	ns := ctxns.FromContext(ctx)
	var nodeIDs []uint
	var filePaths []string
	if err := s.db.WithContext(ctx).
		Model(&model.Node{}).
		Where("namespace = ?", ns).
		Pluck("id", &nodeIDs).Error; err != nil {
		return trace.Wrap(err, "pluck namespace node ids")
	}
	if err := s.db.WithContext(ctx).
		Model(&model.Node{}).
		Where("namespace = ?", ns).
		Distinct().
		Pluck("file_path", &filePaths).Error; err != nil {
		return trace.Wrap(err, "pluck namespace file paths")
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(filePaths) > 0 {
			if err := tx.
				Where("namespace = ? AND file_path IN ?", ns, filePaths).
				Delete(&model.Edge{}).Error; err != nil {
				return trace.Wrap(err, "delete namespace file-owned edges")
			}
		}

		if len(nodeIDs) > 0 {
			if err := tx.
				Where("annotation_id IN (?)",
					tx.Model(&model.Annotation{}).Select("id").Where("node_id IN ?", nodeIDs),
				).Delete(&model.DocTag{}).Error; err != nil {
				return trace.Wrap(err, "delete namespace doc_tags")
			}

			if err := tx.
				Where("node_id IN ?", nodeIDs).
				Delete(&model.Annotation{}).Error; err != nil {
				return trace.Wrap(err, "delete namespace annotations")
			}

			if err := tx.
				Where("node_id IN ?", nodeIDs).
				Delete(&model.CommunityMembership{}).Error; err != nil {
				return trace.Wrap(err, "delete namespace community memberships")
			}

			if tx.Migrator().HasTable(&model.FlowMembership{}) {
				flowIDs := tx.Model(&model.Flow{}).Select("id").Where("namespace = ?", ns)
				if err := tx.
					Where("node_id IN ? OR flow_id IN (?)", nodeIDs, flowIDs).
					Delete(&model.FlowMembership{}).Error; err != nil {
					return trace.Wrap(err, "delete namespace flow memberships")
				}
			}

			if tx.Migrator().HasTable(&model.SearchDocument{}) {
				if err := tx.
					Where("node_id IN ?", nodeIDs).
					Delete(&model.SearchDocument{}).Error; err != nil {
					return trace.Wrap(err, "delete namespace search_documents")
				}
			}

			if err := tx.
				Where("from_node_id IN ? OR to_node_id IN ?", nodeIDs, nodeIDs).
				Delete(&model.Edge{}).Error; err != nil {
				return trace.Wrap(err, "delete namespace connected edges")
			}

			if err := tx.
				Where("id IN ?", nodeIDs).
				Delete(&model.Node{}).Error; err != nil {
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
func (s *Store) UpsertEdges(ctx context.Context, edges []model.Edge) error {
	if len(edges) == 0 {
		return nil
	}
	ns := ctxns.FromContext(ctx)
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
func (s *Store) GetEdgesFrom(ctx context.Context, nodeID uint) ([]model.Edge, error) {
	var edges []model.Edge
	ns := ctxns.FromContext(ctx)
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
func (s *Store) GetEdgesFromNodes(ctx context.Context, nodeIDs []uint) ([]model.Edge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	var edges []model.Edge
	ns := ctxns.FromContext(ctx)
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
func (s *Store) GetEdgesTo(ctx context.Context, nodeID uint) ([]model.Edge, error) {
	var edges []model.Edge
	ns := ctxns.FromContext(ctx)
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
func (s *Store) GetEdgesToNodes(ctx context.Context, nodeIDs []uint) ([]model.Edge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	var edges []model.Edge
	ns := ctxns.FromContext(ctx)
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
	ns := ctxns.FromContext(ctx)
	return s.db.WithContext(ctx).Where("namespace = ? AND file_path = ?", ns, filePath).Delete(&model.Edge{}).Error
}

// UpsertAnnotation stores a node's annotation and tags.
// @intent replace structured comments for each node with the latest state.
// @sideEffect performs inserts, updates, and deletes on the annotations and doc_tags tables.
// @mutates may overwrite ann.ID with the existing record ID.
// @domainRule only one annotation must be kept per node_id.
func (s *Store) UpsertAnnotation(ctx context.Context, ann *model.Annotation) error {
	var existing model.Annotation
	ns := ctxns.FromContext(ctx)
	result := s.db.WithContext(ctx).
		Joins("JOIN nodes ON nodes.id = annotations.node_id").
		Where("annotations.node_id = ? AND nodes.namespace = ?", ann.NodeID, ns).
		First(&existing)

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return tx.Create(ann).Error
		})
	}
	if result.Error != nil {
		return result.Error
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("annotation_id = ?", existing.ID).Delete(&model.DocTag{}).Error; err != nil {
			return trace.Wrap(err, "delete doc tags")
		}
		ann.ID = existing.ID
		return tx.Save(ann).Error
	})
}

// GetAnnotation retrieves the annotation attached to a node ID.
// @intent load a node's structured comment and tags together for search and display.
// @return returns nil when no annotation exists.
func (s *Store) GetAnnotation(ctx context.Context, nodeID uint) (*model.Annotation, error) {
	var ann model.Annotation
	ns := ctxns.FromContext(ctx)
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
func (s *Store) WithTx(ctx context.Context, fn func(store store.GraphStore) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txStore := New(tx)
		return fn(txStore)
	})
}

// WithTxDB passes the transaction DB handle together with the repository.
// @intent let graph persistence and DB-backed derived-state updates share a single transaction.
// @sideEffect starts a database transaction and commits or rolls it back.
func (s *Store) WithTxDB(ctx context.Context, fn func(store.GraphStore, *gorm.DB) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		txStore := New(tx)
		return fn(txStore, tx)
	})
}

var _ store.GraphStore = (*Store)(nil)
