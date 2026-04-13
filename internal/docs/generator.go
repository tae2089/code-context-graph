package docs

import (
	"errors"
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

	ids := symbolNodeIDs(nodes)
	edgesByFromID, err := g.loadEdges(ids)
	if err != nil {
		return fmt.Errorf("load edges: %w", err)
	}

	groups := groupByFile(nodes, annByID, edgesByFromID)

	var errs []error
	for _, grp := range groups {
		if err := g.writeFileDoc(grp); err != nil {
			errs = append(errs, fmt.Errorf("write file doc %s: %w", grp.FilePath, err))
		}
	}
	if err := g.writeIndex(groups); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (g *Generator) loadNodes() ([]model.Node, map[uint]*model.Annotation, error) {
	var nodes []model.Node
	if err := g.DB.Where("kind IN ?", []string{
		string(model.NodeKindFunction),
		string(model.NodeKindClass),
		string(model.NodeKindType),
		string(model.NodeKindTest),
		string(model.NodeKindFile),
	}).Find(&nodes).Error; err != nil {
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

// symbolNodeIDs returns IDs of non-file nodes only.
// File nodes do not originate call/import edges, so excluding them
// keeps the loadEdges IN clause minimal.
func symbolNodeIDs(nodes []model.Node) []uint {
	ids := make([]uint, 0, len(nodes))
	for _, n := range nodes {
		if n.Kind != model.NodeKindFile {
			ids = append(ids, n.ID)
		}
	}
	return ids
}
