package coverage

import (
	"context"
	"errors"

	"github.com/imtaebin/code-context-graph/internal/model"
	"gorm.io/gorm"
)

type FileCoverage struct {
	FilePath string
	Total    int
	Tested   int
	Ratio    float64
}

type CommunityCoverage struct {
	CommunityID uint
	Label       string
	Total       int
	Tested      int
	Ratio       float64
}

type Service struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Service {
	return &Service{db: db}
}

// ByFile calculates test coverage ratio for a single file.
// Used by review_changes and pre_merge_check prompts.
//
// @param filePath source file path to analyze
// @return coverage ratio of functions with tested_by edges
// @intent measure how well a file is covered by tests
// @domainRule coverage ratio equals tested functions divided by total functions
// @domainRule files with no functions return ratio 0.0
func (s *Service) ByFile(ctx context.Context, filePath string) (*FileCoverage, error) {
	var functions []model.Node
	if err := s.db.WithContext(ctx).
		Where("file_path = ? AND kind = ?", filePath, model.NodeKindFunction).
		Find(&functions).Error; err != nil {
		return nil, err
	}

	cov := &FileCoverage{FilePath: filePath, Total: len(functions)}
	if cov.Total == 0 {
		return cov, nil
	}

	funcIDs := make([]uint, len(functions))
	for i, f := range functions {
		funcIDs[i] = f.ID
	}

	var testedCount int64
	err := s.db.WithContext(ctx).
		Model(&model.Node{}).
		Where("id IN ? AND id IN (?)",
			funcIDs,
			s.db.Model(&model.Edge{}).Select("to_node_id").Where("kind = ?", model.EdgeKindTestedBy),
		).
		Count(&testedCount).Error
	if err != nil {
		return nil, err
	}

	cov.Tested = int(testedCount)
	cov.Ratio = float64(cov.Tested) / float64(cov.Total)
	return cov, nil
}

func (s *Service) ByCommunity(ctx context.Context, communityID uint) (*CommunityCoverage, error) {
	var comm model.Community
	if err := s.db.WithContext(ctx).First(&comm, communityID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("community not found")
		}
		return nil, err
	}

	var funcIDs []uint
	err := s.db.WithContext(ctx).
		Model(&model.Node{}).
		Select("nodes.id").
		Joins("JOIN community_memberships ON community_memberships.node_id = nodes.id").
		Where("community_memberships.community_id = ? AND nodes.kind = ?", communityID, model.NodeKindFunction).
		Pluck("nodes.id", &funcIDs).Error
	if err != nil {
		return nil, err
	}

	cov := &CommunityCoverage{
		CommunityID: communityID,
		Label:       comm.Label,
		Total:       len(funcIDs),
	}
	if cov.Total == 0 {
		return cov, nil
	}

	var testedCount int64
	err = s.db.WithContext(ctx).
		Model(&model.Node{}).
		Where("id IN ? AND id IN (?)",
			funcIDs,
			s.db.Model(&model.Edge{}).Select("to_node_id").Where("kind = ?", model.EdgeKindTestedBy),
		).
		Count(&testedCount).Error
	if err != nil {
		return nil, err
	}

	cov.Tested = int(testedCount)
	cov.Ratio = float64(cov.Tested) / float64(cov.Total)
	return cov, nil
}
