// @index GORM read models for MCP graph, context, namespace, and change surfaces.
package graphgorm

import (
	"context"

	"github.com/tae2089/code-context-graph/internal/app/analyze"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// @intent load one stable global namespace page with node counts.
func (s *Store) NamespacesPage(ctx context.Context, limit, offset int) ([]analyze.NamespaceSummary, bool, error) {
	var rows []analyze.NamespaceSummary
	if err := s.db.WithContext(ctx).Model(&graph.Node{}).Select("namespace, COUNT(*) AS node_count").Group("namespace").Order("namespace ASC").Limit(limit + 1).Offset(offset).Scan(&rows).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	return rows, hasMore, nil
}

// @intent load one stable namespace-scoped stored-flow page with member counts.
func (s *Store) FlowsPage(ctx context.Context, sortBy string, limit, offset int) ([]analyze.FlowSummary, bool, error) {
	var rows []analyze.FlowSummary
	q := s.db.WithContext(ctx).Model(&graph.Flow{}).
		Select("flows.id AS id, flows.name AS name, flows.description AS description, COALESCE(COUNT(flow_memberships.id),0) AS node_count").
		Joins("LEFT JOIN flow_memberships ON flow_memberships.flow_id = flows.id AND flow_memberships.namespace = flows.namespace").
		Where("flows.namespace = ?", requestctx.FromContext(ctx)).Group("flows.id, flows.name, flows.description")
	if sortBy == "node_count" {
		q = q.Order("node_count DESC").Order("flows.name ASC").Order("flows.id ASC")
	} else {
		q = q.Order("flows.name ASC").Order("flows.id ASC")
	}
	if err := q.Limit(limit + 1).Offset(offset).Find(&rows).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	return rows, hasMore, nil
}

// @intent select the strongest call evidence edge for each requested peer node.
func (s *Store) CallEdges(ctx context.Context, anchorID uint, peerIDs []uint, direction analyze.EdgeDirection) (map[uint]graph.Edge, error) {
	result := map[uint]graph.Edge{}
	if len(peerIDs) == 0 {
		return result, nil
	}
	q := s.db.WithContext(ctx).Model(&graph.Edge{}).Where("namespace = ? AND kind IN ?", requestctx.FromContext(ctx), graph.CallEdgeKinds())
	if direction == analyze.EdgeDirectionIncoming {
		q = q.Where("from_node_id IN ? AND to_node_id = ?", peerIDs, anchorID)
	} else {
		q = q.Where("to_node_id IN ? AND from_node_id = ?", peerIDs, anchorID)
	}
	var edges []graph.Edge
	if err := q.Find(&edges).Error; err != nil {
		return nil, err
	}
	for _, edge := range edges {
		peerID := edge.ToNodeID
		if direction == analyze.EdgeDirectionIncoming {
			peerID = edge.FromNodeID
		}
		if existing, ok := result[peerID]; ok && !(existing.Kind == graph.EdgeKindFallbackCalls && edge.Kind == graph.EdgeKindCalls) {
			continue
		}
		result[peerID] = edge
	}
	return result, nil
}

// @intent map changed nodes to one deterministic page of namespace-scoped stored flows.
func (s *Store) AffectedFlowsPage(ctx context.Context, changedNodeIDs []uint, limit, offset int) ([]analyze.AffectedFlow, bool, error) {
	if len(changedNodeIDs) == 0 {
		return []analyze.AffectedFlow{}, false, nil
	}
	ns := requestctx.FromContext(ctx)
	var memberships []graph.FlowMembership
	if err := s.db.WithContext(ctx).Where("node_id IN ? AND namespace = ?", changedNodeIDs, ns).Find(&memberships).Error; err != nil {
		return nil, false, err
	}
	flowNodes := map[uint][]uint{}
	for _, row := range memberships {
		flowNodes[row.FlowID] = append(flowNodes[row.FlowID], row.NodeID)
	}
	if len(flowNodes) == 0 {
		return []analyze.AffectedFlow{}, false, nil
	}
	ids := make([]uint, 0, len(flowNodes))
	for id := range flowNodes {
		ids = append(ids, id)
	}
	var flows []graph.Flow
	if err := s.db.WithContext(ctx).Where("id IN ? AND namespace = ?", ids, ns).Order("name ASC").Order("id ASC").Limit(limit + 1).Offset(offset).Find(&flows).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(flows) > limit
	if hasMore {
		flows = flows[:limit]
	}
	result := make([]analyze.AffectedFlow, len(flows))
	for i, flow := range flows {
		result[i] = analyze.AffectedFlow{ID: flow.ID, Name: flow.Name, AffectedNodes: flowNodes[flow.ID]}
	}
	return result, hasMore, nil
}

// @intent count requested nodes without a namespace-scoped tested_by edge.
func (s *Store) UntestedCount(ctx context.Context, nodeIDs []uint) (int, error) {
	if len(nodeIDs) == 0 {
		return 0, nil
	}
	var tested []uint
	if err := s.db.WithContext(ctx).Model(&graph.Edge{}).Where("namespace = ? AND kind = ? AND to_node_id IN ?", requestctx.FromContext(ctx), graph.EdgeKindTestedBy, nodeIDs).Distinct("to_node_id").Pluck("to_node_id", &tested).Error; err != nil {
		return 0, err
	}
	return len(nodeIDs) - len(tested), nil
}

// @intent rank namespace communities by stored membership count.
func (s *Store) TopCommunities(ctx context.Context, limit int) ([]analyze.NamedCount, error) {
	var rows []analyze.NamedCount
	err := s.db.WithContext(ctx).Model(&graph.CommunityMembership{}).Joins("JOIN communities ON communities.id = community_memberships.community_id").Where("communities.namespace = ?", requestctx.FromContext(ctx)).Select("communities.label AS name, COUNT(*) AS count").Group("community_id").Group("communities.label").Order("count DESC").Order("community_id ASC").Limit(limit).Scan(&rows).Error
	return rows, err
}

// @intent rank namespace flows by stored membership count.
func (s *Store) TopFlows(ctx context.Context, limit int) ([]analyze.NamedCount, error) {
	var rows []analyze.NamedCount
	err := s.db.WithContext(ctx).Model(&graph.FlowMembership{}).Joins("JOIN flows ON flows.id = flow_memberships.flow_id").Where("flow_memberships.namespace = ?", requestctx.FromContext(ctx)).Select("flows.name AS name, COUNT(*) AS count").Group("flow_id").Group("flows.name").Order("count DESC").Order("flow_id ASC").Limit(limit).Scan(&rows).Error
	return rows, err
}

var _ analyze.GraphReadRepository = (*Store)(nil)
