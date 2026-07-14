package docs

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/domain/reference"
)

type testRepository struct{ db *gorm.DB }

func migrateDocsTestDB(db *gorm.DB) error {
	return db.AutoMigrate(&graph.Node{}, &graph.Edge{}, &graph.Annotation{}, &graph.DocTag{}, &graph.Community{}, &graph.CommunityMembership{}, &graph.Flow{}, &graph.FlowMembership{})
}

func (r testRepository) Snapshot(_ context.Context, namespace string, kinds []graph.NodeKind) (Snapshot, error) {
	var nodes []graph.Node
	q := r.db.Where("kind IN ?", kinds)
	if namespace != "" {
		q = q.Where("namespace = ?", namespace)
	}
	if err := q.Find(&nodes).Error; err != nil {
		return Snapshot{}, err
	}
	ids := make([]uint, len(nodes))
	for i := range nodes {
		ids[i] = nodes[i].ID
	}
	annotations := map[uint]*graph.Annotation{}
	if len(ids) > 0 {
		var rows []graph.Annotation
		if err := r.db.Where("node_id IN ?", ids).Preload("Tags").Find(&rows).Error; err != nil {
			return Snapshot{}, err
		}
		for i := range rows {
			annotations[rows[i].NodeID] = &rows[i]
		}
	}
	return Snapshot{Nodes: nodes, Annotations: annotations}, nil
}

func (r testRepository) OutgoingDocEdges(_ context.Context, namespace string, ids []uint) (map[uint][]graph.Edge, error) {
	result := map[uint][]graph.Edge{}
	if len(ids) == 0 {
		return result, nil
	}
	var edges []graph.Edge
	q := r.db.Preload("ToNode").Where("from_node_id IN ? AND kind IN ?", ids, []graph.EdgeKind{graph.EdgeKindCalls, graph.EdgeKindImportsFrom})
	if namespace != "" {
		q = q.Where("namespace = ?", namespace)
	}
	if err := q.Find(&edges).Error; err != nil {
		return nil, err
	}
	for _, edge := range edges {
		result[edge.FromNodeID] = append(result[edge.FromNodeID], edge)
	}
	return result, nil
}

func (r testRepository) QualifiedNameExists(_ context.Context, namespace, name string) (bool, error) {
	var count int64
	q := r.db.Model(&graph.Node{}).Where("qualified_name = ?", name)
	if namespace != "" {
		q = q.Where("namespace = ?", namespace)
	}
	err := q.Count(&count).Error
	return count > 0, err
}

func (r testRepository) CCGRefExists(_ context.Context, ref reference.Ref) (bool, error) {
	q := r.db.Model(&graph.Node{}).Where("namespace = ?", ref.Namespace)
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
