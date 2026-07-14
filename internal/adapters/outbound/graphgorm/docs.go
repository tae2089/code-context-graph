// @index GORM read adapter for documentation generation and lint facts.
package graphgorm

import (
	"context"
	"strings"

	docsapp "github.com/tae2089/code-context-graph/internal/app/docs"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/domain/reference"
)

var _ docsapp.Repository = (*Store)(nil)

// @intent load documentable nodes and their annotations from one namespace.
func (s *Store) Snapshot(ctx context.Context, namespace string, kinds []graph.NodeKind) (docsapp.Snapshot, error) {
	var nodes []graph.Node
	q := s.db.WithContext(ctx).Where("kind IN ?", kinds)
	if namespace != "" {
		q = q.Where("namespace = ?", namespace)
	}
	if err := q.Find(&nodes).Error; err != nil {
		return docsapp.Snapshot{}, err
	}
	ids := make([]uint, len(nodes))
	for i := range nodes {
		ids[i] = nodes[i].ID
	}
	annotations := map[uint]*graph.Annotation{}
	if len(ids) > 0 {
		var rows []graph.Annotation
		if err := s.db.WithContext(ctx).Where("node_id IN ?", ids).Preload("Tags").Find(&rows).Error; err != nil {
			return docsapp.Snapshot{}, err
		}
		for i := range rows {
			annotations[rows[i].NodeID] = &rows[i]
		}
	}
	return docsapp.Snapshot{Nodes: nodes, Annotations: annotations}, nil
}

// @intent load call/import relationships rendered beneath symbol documentation.
func (s *Store) OutgoingDocEdges(ctx context.Context, namespace string, ids []uint) (map[uint][]graph.Edge, error) {
	result := map[uint][]graph.Edge{}
	if len(ids) == 0 {
		return result, nil
	}
	var edges []graph.Edge
	q := s.db.WithContext(ctx).Preload("ToNode").Where("from_node_id IN ? AND kind IN ?", ids, []graph.EdgeKind{graph.EdgeKindCalls, graph.EdgeKindImportsFrom})
	if namespace != "" {
		q = q.Where("namespace = ?", namespace)
	}
	err := q.Find(&edges).Error
	if err != nil {
		return nil, err
	}
	for _, edge := range edges {
		result[edge.FromNodeID] = append(result[edge.FromNodeID], edge)
	}
	return result, nil
}

// @intent validate local @see targets within the active docs namespace.
func (s *Store) QualifiedNameExists(ctx context.Context, namespace, name string) (bool, error) {
	var count int64
	q := s.db.WithContext(ctx).Model(&graph.Node{}).Where("qualified_name = ?", name)
	if namespace != "" {
		q = q.Where("namespace = ?", namespace)
	}
	err := q.Count(&count).Error
	return count > 0, err
}

// @intent validate parsed cross-namespace ccg references against graph path and symbol semantics.
func (s *Store) CCGRefExists(ctx context.Context, ref reference.Ref) (bool, error) {
	q := s.db.WithContext(ctx).Model(&graph.Node{}).Where("namespace = ?", ref.Namespace)
	if ref.Path != "" {
		if ref.Symbol != "" {
			q = q.Where("file_path = ? AND (name = ? OR qualified_name = ? OR qualified_name LIKE ? OR qualified_name LIKE ?)", ref.Path, ref.Symbol, ref.Symbol, "%."+ref.Symbol, "%::"+ref.Symbol)
		} else {
			q = q.Where("file_path = ? OR file_path LIKE ?", ref.Path, strings.TrimSuffix(ref.Path, "/")+"/%")
		}
	} else if ref.Symbol != "" {
		q = q.Where("name = ? OR qualified_name = ? OR qualified_name LIKE ? OR qualified_name LIKE ?", ref.Symbol, ref.Symbol, "%."+ref.Symbol, "%::"+ref.Symbol)
	}
	var count int64
	err := q.Count(&count).Error
	return count > 0, err
}
