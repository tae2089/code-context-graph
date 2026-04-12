package query

import (
	"context"

	"github.com/imtaebin/code-context-graph/internal/model"
	"gorm.io/gorm"
)

type FileSummary struct {
	FilePath  string
	Functions int
	Classes   int
	Types     int
	Tests     int
	Total     int
}

type Service struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Service {
	return &Service{db: db}
}

func (s *Service) nodesByEdge(ctx context.Context, nodeID uint, kind model.EdgeKind, direction string) ([]model.Node, error) {
	var nodes []model.Node
	var q *gorm.DB
	switch direction {
	case "incoming":
		q = s.db.WithContext(ctx).
			Joins("JOIN edges ON edges.from_node_id = nodes.id").
			Where("edges.to_node_id = ? AND edges.kind = ?", nodeID, kind)
	default:
		q = s.db.WithContext(ctx).
			Joins("JOIN edges ON edges.to_node_id = nodes.id").
			Where("edges.from_node_id = ? AND edges.kind = ?", nodeID, kind)
	}
	if err := q.Find(&nodes).Error; err != nil {
		return nil, err
	}
	return nodes, nil
}

func (s *Service) CallersOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	return s.nodesByEdge(ctx, nodeID, model.EdgeKindCalls, "incoming")
}

func (s *Service) CalleesOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	return s.nodesByEdge(ctx, nodeID, model.EdgeKindCalls, "outgoing")
}

func (s *Service) ImportsOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	return s.nodesByEdge(ctx, nodeID, model.EdgeKindImportsFrom, "outgoing")
}

func (s *Service) ImportersOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	return s.nodesByEdge(ctx, nodeID, model.EdgeKindImportsFrom, "incoming")
}

func (s *Service) ChildrenOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	return s.nodesByEdge(ctx, nodeID, model.EdgeKindContains, "outgoing")
}

func (s *Service) TestsFor(ctx context.Context, nodeID uint) ([]model.Node, error) {
	return s.nodesByEdge(ctx, nodeID, model.EdgeKindTestedBy, "incoming")
}

func (s *Service) InheritorsOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	return s.nodesByEdge(ctx, nodeID, model.EdgeKindInherits, "incoming")
}

func (s *Service) FileSummaryOf(ctx context.Context, filePath string) (*FileSummary, error) {
	var nodes []model.Node
	if err := s.db.WithContext(ctx).Where("file_path = ?", filePath).Find(&nodes).Error; err != nil {
		return nil, err
	}

	summary := &FileSummary{FilePath: filePath, Total: len(nodes)}
	for _, n := range nodes {
		switch n.Kind {
		case model.NodeKindFunction:
			summary.Functions++
		case model.NodeKindClass:
			summary.Classes++
		case model.NodeKindType:
			summary.Types++
		case model.NodeKindTest:
			summary.Tests++
		}
	}
	return summary, nil
}
