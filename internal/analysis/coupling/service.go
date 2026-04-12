package coupling

import (
	"context"

	"github.com/imtaebin/code-context-graph/internal/model"
	"gorm.io/gorm"
)

type CouplingPair struct {
	FromCommunity string
	ToCommunity   string
	EdgeCount     int64
	Strength      float64
}

type Service struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Service {
	return &Service{db: db}
}

// Analyze measures coupling strength between communities.
// Used by MCP get_architecture_overview tool and architecture_map prompt.
//
// @return pairs of communities with cross-community edge counts and strength
// @intent detect tightly coupled modules for architecture improvement
// @domainRule strength equals edge count divided by maximum edge count across all pairs
// @domainRule only cross-community edges are counted
func (s *Service) Analyze(ctx context.Context) ([]CouplingPair, error) {
	var memberships []model.CommunityMembership
	if err := s.db.WithContext(ctx).Find(&memberships).Error; err != nil {
		return nil, err
	}

	nodeComm := map[uint]uint{}
	for _, m := range memberships {
		nodeComm[m.NodeID] = m.CommunityID
	}

	if len(nodeComm) == 0 {
		return nil, nil
	}

	var communities []model.Community
	if err := s.db.WithContext(ctx).Find(&communities).Error; err != nil {
		return nil, err
	}
	commLabel := map[uint]string{}
	for _, c := range communities {
		commLabel[c.ID] = c.Key
	}

	var edges []model.Edge
	if err := s.db.WithContext(ctx).Find(&edges).Error; err != nil {
		return nil, err
	}

	type pairKey struct{ from, to uint }
	counts := map[pairKey]int64{}

	for _, e := range edges {
		fromComm, fOK := nodeComm[e.FromNodeID]
		toComm, tOK := nodeComm[e.ToNodeID]
		if !fOK || !tOK || fromComm == toComm {
			continue
		}
		counts[pairKey{fromComm, toComm}]++
	}

	if len(counts) == 0 {
		return nil, nil
	}

	var maxCount int64
	for _, c := range counts {
		if c > maxCount {
			maxCount = c
		}
	}

	result := make([]CouplingPair, 0, len(counts))
	for pk, cnt := range counts {
		result = append(result, CouplingPair{
			FromCommunity: commLabel[pk.from],
			ToCommunity:   commLabel[pk.to],
			EdgeCount:     cnt,
			Strength:      float64(cnt) / float64(maxCount),
		})
	}

	return result, nil
}
