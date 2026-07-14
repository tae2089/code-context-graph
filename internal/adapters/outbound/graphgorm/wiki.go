// @index GORM read adapter for eager and lazy built-in Wiki tree construction.
package graphgorm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/app/wiki"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/domain/reference"
)

var _ wiki.Repository = (*Store)(nil)

// Namespaces returns distinct stored namespaces in stable order.
// @intent implement Wiki namespace discovery without exposing persistence to HTTP.
func (s *Store) Namespaces(ctx context.Context) ([]string, error) {
	var rows []string
	err := s.db.WithContext(ctx).Model(&graph.Node{}).Distinct("namespace").Order("namespace ASC").Pluck("namespace", &rows).Error
	return rows, err
}

// @intent load the stable graph snapshot from which the eager Wiki hierarchy is derived.
func (s *Store) NavigationNodes(ctx context.Context, kinds []graph.NodeKind) ([]graph.Node, error) {
	var nodes []graph.Node
	err := s.db.WithContext(ctx).Where("namespace = ? AND kind IN ?", requestctx.FromContext(ctx), wikiKinds(kinds)).Order("file_path ASC, start_line ASC, qualified_name ASC").Find(&nodes).Error
	return nodes, err
}

// @intent load stable path-bearing candidates below one lazy Wiki folder or package.
func (s *Store) PathNodes(ctx context.Context, folderPath string, kinds []graph.NodeKind) ([]graph.Node, error) {
	var nodes []graph.Node
	q := s.db.WithContext(ctx).Where("namespace = ? AND kind IN ?", requestctx.FromContext(ctx), wikiKinds(kinds)).Order("file_path ASC, start_line ASC, qualified_name ASC")
	folderPath = strings.Trim(path.Clean(folderPath), "/")
	if folderPath != "." && folderPath != "" {
		q = q.Where("file_path LIKE ?", folderPath+"/%")
	}
	return nodes, q.Find(&nodes).Error
}

// @intent resolve one stored package or file used as a lazy Wiki root.
func (s *Store) StoredNode(ctx context.Context, kind graph.NodeKind, filePath string) (*graph.Node, error) {
	var node graph.Node
	if err := s.db.WithContext(ctx).Where("namespace = ? AND kind = ? AND file_path = ?", requestctx.FromContext(ctx), kind, filePath).First(&node).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: wiki node", os.ErrNotExist)
		}
		return nil, err
	}
	return &node, nil
}

// @intent resolve the first deterministic symbol match used by direct lazy navigation.
func (s *Store) SymbolNode(ctx context.Context, qualifiedName string, kinds []graph.NodeKind) (*graph.Node, error) {
	var node graph.Node
	if err := s.db.WithContext(ctx).Where("namespace = ? AND qualified_name = ? AND kind IN ?", requestctx.FromContext(ctx), qualifiedName, wikiKinds(kinds)).Order("file_path ASC, start_line ASC, id ASC").First(&node).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: wiki symbol", os.ErrNotExist)
		}
		return nil, err
	}
	return &node, nil
}

// @intent load stable symbol children for one lazy Wiki file node.
func (s *Store) FileSymbols(ctx context.Context, filePath string, kinds []graph.NodeKind) ([]graph.Node, error) {
	var nodes []graph.Node
	err := s.db.WithContext(ctx).Where("namespace = ? AND file_path = ? AND kind IN ?", requestctx.FromContext(ctx), filePath, wikiKinds(kinds)).Order("start_line ASC, qualified_name ASC").Find(&nodes).Error
	return nodes, err
}

// @intent batch-load Wiki annotations with deterministic tag ordering.
func (s *Store) Annotations(ctx context.Context, ids []uint) (map[uint]*graph.Annotation, error) {
	result := map[uint]*graph.Annotation{}
	if len(ids) == 0 {
		return result, nil
	}
	var rows []graph.Annotation
	err := s.db.WithContext(ctx).Joins("JOIN nodes ON nodes.id = annotations.node_id").Where("annotations.node_id IN ? AND nodes.namespace = ?", ids, requestctx.FromContext(ctx)).Preload("Tags", func(db *gorm.DB) *gorm.DB { return db.Order("ordinal ASC, id ASC") }).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for i := range rows {
		result[rows[i].NodeID] = &rows[i]
	}
	return result, nil
}

// @intent answer whether a lazy file node has expandable symbol children.
func (s *Store) HasSymbol(ctx context.Context, filePath string, kinds []graph.NodeKind) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&graph.Node{}).Where("namespace = ? AND file_path = ? AND kind IN ?", requestctx.FromContext(ctx), filePath, wikiKinds(kinds)).Limit(1).Count(&count).Error
	return count > 0, err
}

// GraphView loads a bounded stable node set and only edges contained within it.
// @intent implement the Wiki force-graph read port with deterministic ordering and limits.
func (s *Store) GraphView(ctx context.Context, limit int, edgeKinds []graph.EdgeKind) (wiki.GraphView, error) {
	ns := requestctx.FromContext(ctx)
	var view wiki.GraphView
	if err := s.db.WithContext(ctx).Model(&graph.Node{}).Where("namespace = ?", ns).Count(&view.TotalNodes).Error; err != nil {
		return view, &wiki.GraphViewError{Stage: wiki.GraphViewStageCountNodes, Err: err}
	}
	if err := s.db.WithContext(ctx).Where("namespace = ?", ns).Order("kind ASC, file_path ASC, start_line ASC, qualified_name ASC").Limit(limit).Find(&view.Nodes).Error; err != nil {
		return view, &wiki.GraphViewError{Stage: wiki.GraphViewStageListNodes, Err: err}
	}
	ids := make([]uint, len(view.Nodes))
	for i := range view.Nodes {
		ids[i] = view.Nodes[i].ID
	}
	if len(ids) == 0 {
		return view, nil
	}
	q := s.db.WithContext(ctx).Where("namespace = ? AND from_node_id IN ? AND to_node_id IN ?", ns, ids, ids)
	if len(edgeKinds) > 0 {
		q = q.Where("kind IN ?", edgeKinds)
	}
	if err := q.Order("kind ASC, file_path ASC, line ASC, id ASC").Limit(limit * 4).Find(&view.Edges).Error; err != nil {
		return view, &wiki.GraphViewError{Stage: wiki.GraphViewStageListEdges, Err: err}
	}
	return view, nil
}

// ResolveReference finds the first deterministic graph node matching a parsed CCG reference.
// @intent resolve Wiki reference navigation while keeping GORM filtering and preload behavior in the outbound adapter.
func (s *Store) ResolveReference(ctx context.Context, ref *reference.Ref) (*graph.Node, error) {
	if ref == nil || (ref.Path == "" && ref.Symbol == "") {
		return nil, fmt.Errorf("%w: graph reference", os.ErrNotExist)
	}
	var nodes []graph.Node
	q := s.db.WithContext(ctx).Where("namespace = ?", ref.Namespace).Preload("Annotation.Tags").Order("kind ASC, file_path ASC, start_line ASC, qualified_name ASC")
	if ref.Path != "" && ref.Symbol != "" {
		q = q.Where("file_path = ?", ref.Path)
	}
	if err := q.Find(&nodes).Error; err != nil {
		return nil, err
	}
	for i := range nodes {
		node := &nodes[i]
		if ref.Symbol != "" {
			if node.Name != ref.Symbol && node.QualifiedName != ref.Symbol && !strings.HasSuffix(node.QualifiedName, "."+ref.Symbol) && !strings.HasSuffix(node.QualifiedName, "::"+ref.Symbol) {
				continue
			}
		} else {
			nodePath := strings.Trim(filepath.ToSlash(node.FilePath), "/")
			refPath := strings.Trim(filepath.ToSlash(ref.Path), "/")
			if nodePath != refPath && !strings.HasPrefix(nodePath, strings.TrimSuffix(refPath, "/")+"/") {
				continue
			}
		}
		return node, nil
	}
	return nil, fmt.Errorf("%w: graph reference", os.ErrNotExist)
}

// @intent convert domain node kinds to stable GORM IN-clause values at the adapter boundary.
func wikiKinds(kinds []graph.NodeKind) []string {
	values := make([]string, len(kinds))
	for i := range kinds {
		values[i] = string(kinds[i])
	}
	return values
}
