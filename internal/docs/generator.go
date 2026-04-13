package docs

import (
	"fmt"

	"gorm.io/gorm"

	"github.com/imtaebin/code-context-graph/internal/model"
)

// Generator reads the SQLite graph and writes markdown documentation.
type Generator struct {
	DB     *gorm.DB
	OutDir string
}

// Run generates index.md and per-file docs into g.OutDir.
func (g *Generator) Run() error {
	nodes, annByID, err := g.loadNodes()
	if err != nil {
		return fmt.Errorf("load nodes: %w", err)
	}

	ids := nodeIDsFrom(nodes)
	edgesByFromID, err := g.loadEdges(ids)
	if err != nil {
		return fmt.Errorf("load edges: %w", err)
	}

	groups := groupByFile(nodes, annByID, edgesByFromID)

	for _, grp := range groups {
		if err := g.writeFileDoc(grp); err != nil {
			return fmt.Errorf("write file doc %s: %w", grp.FilePath, err)
		}
	}

	return g.writeIndex(groups)
}

func (g *Generator) loadNodes() ([]model.Node, map[uint]*model.Annotation, error) {
	var nodes []model.Node
	if err := g.DB.Where("kind IN ?", []string{"function", "class", "type", "test", "file"}).Find(&nodes).Error; err != nil {
		return nil, nil, fmt.Errorf("query nodes: %w", err)
	}

	ids := nodeIDsFrom(nodes)
	annByID := make(map[uint]*model.Annotation)
	if len(ids) > 0 {
		var annotations []model.Annotation
		if err := g.DB.Where("node_id IN ?", ids).Preload("Tags").Find(&annotations).Error; err != nil {
			return nil, nil, fmt.Errorf("query annotations: %w", err)
		}
		for i := range annotations {
			annByID[annotations[i].NodeID] = &annotations[i]
		}
	}

	return nodes, annByID, nil
}

func (g *Generator) loadEdges(nodeIDs []uint) (map[uint][]model.Edge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	var edges []model.Edge
	if err := g.DB.Preload("ToNode").
		Where("from_node_id IN ? AND kind IN ?", nodeIDs,
			[]string{string(model.EdgeKindCalls), string(model.EdgeKindImportsFrom)}).
		Find(&edges).Error; err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	result := make(map[uint][]model.Edge, len(edges))
	for _, e := range edges {
		result[e.FromNodeID] = append(result[e.FromNodeID], e)
	}
	return result, nil
}

func nodeIDsFrom(nodes []model.Node) []uint {
	ids := make([]uint, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return ids
}
