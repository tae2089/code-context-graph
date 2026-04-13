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
	return nil
}

// LoadNodes는 테스트 접근을 위해 내보낸 래퍼다.
func (g *Generator) LoadNodes() ([]model.Node, map[uint]*model.Annotation, error) {
	return g.loadNodes()
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

func nodeIDsFrom(nodes []model.Node) []uint {
	ids := make([]uint, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return ids
}
