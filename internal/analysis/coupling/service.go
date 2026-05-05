// @index 모듈 간 결합도 분석. 커뮤니티 경계를 넘는 엣지를 집계하여 아키텍처 결합 강도를 측정한다.
package coupling

import (
	"context"
	"sort"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/paging"
	"gorm.io/gorm"
)

// CouplingPair represents coupling strength between two communities.
// @intent expose cross-community dependency counts in a normalized form
type CouplingPair struct {
	FromCommunity string
	ToCommunity   string
	EdgeCount     int64
	Strength      float64
}

// Result carries one bounded page of coupling pairs plus pagination metadata.
// @intent expose paged architecture-coupling results so MCP handlers stop slicing unbounded slices in memory.
type Result struct {
	Items      []CouplingPair
	Pagination paging.Page
}

// Service analyzes architectural coupling from graph edges.
// @intent measure dependency strength between detected communities
type Service struct {
	db *gorm.DB
}

// New creates a coupling analysis service.
// @intent construct a service for cross-community dependency queries
func New(db *gorm.DB) *Service {
	return &Service{db: db}
}

// pairRow holds one cross-community edge aggregation row.
// @intent transport GROUP BY join results from Analyze into post-processing.
type pairRow struct {
	FromCommID uint
	ToCommID   uint
	EdgeCount  int64
}

// Analyze measures coupling strength between communities.
// Used by MCP get_architecture_overview tool and architecture_map prompt.
//
// @return pairs of communities with cross-community edge counts and strength
// @intent detect tightly coupled modules for architecture improvement
// @domainRule strength equals edge count divided by maximum edge count across all pairs
// @domainRule only cross-community edges are counted
func (s *Service) Analyze(ctx context.Context) ([]CouplingPair, error) {
	ns := ctxns.FromContext(ctx)
	var rows []pairRow
	q := s.db.WithContext(ctx).
		Model(&model.Edge{}).
		Select("cm1.community_id as from_comm_id, cm2.community_id as to_comm_id, COUNT(*) as edge_count").
		Joins("JOIN community_memberships cm1 ON cm1.node_id = edges.from_node_id").
		Joins("JOIN community_memberships cm2 ON cm2.node_id = edges.to_node_id").
		Joins("JOIN nodes n1 ON n1.id = edges.from_node_id").
		Where("cm1.community_id != cm2.community_id")
	q = q.Where("edges.namespace = ? AND n1.namespace = ?", ns, ns)
	if err := q.Group("cm1.community_id, cm2.community_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	if len(rows) == 0 {
		return nil, nil
	}

	commIDs := make([]uint, 0, len(rows)*2)
	seen := map[uint]struct{}{}
	for _, r := range rows {
		if _, ok := seen[r.FromCommID]; !ok {
			commIDs = append(commIDs, r.FromCommID)
			seen[r.FromCommID] = struct{}{}
		}
		if _, ok := seen[r.ToCommID]; !ok {
			commIDs = append(commIDs, r.ToCommID)
			seen[r.ToCommID] = struct{}{}
		}
	}

	var communities []model.Community
	if err := s.db.WithContext(ctx).Where("id IN ?", commIDs).Find(&communities).Error; err != nil {
		return nil, err
	}
	commLabel := make(map[uint]string, len(communities))
	for _, c := range communities {
		commLabel[c.ID] = c.Key
	}

	var maxCount int64
	for _, r := range rows {
		if r.EdgeCount > maxCount {
			maxCount = r.EdgeCount
		}
	}

	result := make([]CouplingPair, 0, len(rows))
	for _, r := range rows {
		result = append(result, CouplingPair{
			FromCommunity: commLabel[r.FromCommID],
			ToCommunity:   commLabel[r.ToCommID],
			EdgeCount:     r.EdgeCount,
			Strength:      float64(r.EdgeCount) / float64(maxCount),
		})
	}

	sortCouplingPairs(result)
	return result, nil
}

// AnalyzePage returns one bounded page of coupling pairs.
// @intent push pagination into the coupling service so handlers expose stable limit/offset windows without slicing unbounded slices.
// @domainRule pairs are sorted by descending strength, then descending edge count, then from/to community for stable pagination.
func (s *Service) AnalyzePage(ctx context.Context, req paging.Request) (Result, error) {
	normalized, err := paging.Normalize(req)
	if err != nil {
		return Result{}, err
	}
	all, err := s.Analyze(ctx)
	if err != nil {
		return Result{}, err
	}
	total := len(all)
	if normalized.Offset >= total {
		return Result{Items: []CouplingPair{}, Pagination: paging.BuildPage(normalized, 0, false)}, nil
	}
	end := normalized.Offset + normalized.Limit + 1
	if end > total {
		end = total
	}
	window := all[normalized.Offset:end]
	hasMore := len(window) > normalized.Limit
	if hasMore {
		window = window[:normalized.Limit]
	}
	out := make([]CouplingPair, len(window))
	copy(out, window)
	return Result{Items: out, Pagination: paging.BuildPage(normalized, len(out), hasMore)}, nil
}

// sortCouplingPairs orders pairs deterministically for stable pagination windows.
// @intent guarantee identical limit/offset slices regardless of map iteration order in Analyze.
func sortCouplingPairs(pairs []CouplingPair) {
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].Strength != pairs[j].Strength {
			return pairs[i].Strength > pairs[j].Strength
		}
		if pairs[i].EdgeCount != pairs[j].EdgeCount {
			return pairs[i].EdgeCount > pairs[j].EdgeCount
		}
		if pairs[i].FromCommunity != pairs[j].FromCommunity {
			return pairs[i].FromCommunity < pairs[j].FromCommunity
		}
		return pairs[i].ToCommunity < pairs[j].ToCommunity
	})
}
