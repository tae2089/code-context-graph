// @index 미리 정의된 그래프 쿼리 서비스. 호출자, 피호출자, import, 상속 등 관계 질의를 제공한다.
package query

import (
	"context"

	"github.com/imtaebin/code-context-graph/internal/model"
	"gorm.io/gorm"
)

// FileSummary aggregates node counts for one file.
// @intent summarize the kinds of graph nodes stored for a source file
type FileSummary struct {
	FilePath  string
	Functions int
	Classes   int
	Types     int
	Tests     int
	Total     int
}

// Service serves predefined graph relationship queries.
// @intent provide reusable higher-level graph lookups for MCP queries
type Service struct {
	db *gorm.DB
}

// New creates a predefined query service.
// @intent construct a service for common graph traversal queries
func New(db *gorm.DB) *Service {
	return &Service{db: db}
}

// nodesByEdge loads nodes connected by a specific edge kind and direction.
// @intent centralize directional edge-query logic shared by predefined graph queries
// @param nodeID anchor node for the relationship lookup
// @param kind edge kind to follow
// @param direction incoming selects source nodes, otherwise destination nodes
// @return nodes connected to the anchor node by the requested relationship
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

// CallersOf returns nodes that call the target node.
// @intent find upstream callers of a function or method node
// @see query.Service.CalleesOf
func (s *Service) CallersOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	return s.nodesByEdge(ctx, nodeID, model.EdgeKindCalls, "incoming")
}

// CalleesOf returns nodes called by the target node.
// @intent find downstream call dependencies of a function or method node
// @see query.Service.CallersOf
func (s *Service) CalleesOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	return s.nodesByEdge(ctx, nodeID, model.EdgeKindCalls, "outgoing")
}

// ImportsOf returns nodes imported by the target node.
// @intent reveal outgoing import dependencies for a file or package node
func (s *Service) ImportsOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	return s.nodesByEdge(ctx, nodeID, model.EdgeKindImportsFrom, "outgoing")
}

// ImportersOf returns nodes that import the target node.
// @intent reveal reverse import dependencies pointing at the target node
func (s *Service) ImportersOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	return s.nodesByEdge(ctx, nodeID, model.EdgeKindImportsFrom, "incoming")
}

// ChildrenOf returns nodes contained by the target node.
// @intent enumerate structural children contained within a file or type node
func (s *Service) ChildrenOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	return s.nodesByEdge(ctx, nodeID, model.EdgeKindContains, "outgoing")
}

// TestsFor returns tests that exercise the target node.
// @intent find test nodes linked to the target via tested_by edges
func (s *Service) TestsFor(ctx context.Context, nodeID uint) ([]model.Node, error) {
	return s.nodesByEdge(ctx, nodeID, model.EdgeKindTestedBy, "incoming")
}

// InheritorsOf returns nodes inheriting from the target node.
// @intent find derived types that point to the target through inheritance edges
func (s *Service) InheritorsOf(ctx context.Context, nodeID uint) ([]model.Node, error) {
	return s.nodesByEdge(ctx, nodeID, model.EdgeKindInherits, "incoming")
}

// FileSummaryOf returns node counts grouped by kind for one file.
// @intent summarize how much graph structure exists within a specific file
// @param filePath repository-relative source file path to summarize
// @return per-kind node counts and total node count for the file
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
