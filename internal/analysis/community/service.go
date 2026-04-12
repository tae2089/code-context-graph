package community

import (
	"context"
	"path"
	"strings"

	"github.com/imtaebin/code-context-graph/internal/model"
	"gorm.io/gorm"
)

type Config struct {
	BaseDir string
	Depth   int
}

type Stats struct {
	Community     model.Community
	NodeCount     int64
	InternalEdges int64
	ExternalEdges int64
	Cohesion      float64
}

type Builder struct {
	db *gorm.DB
}

func New(db *gorm.DB) *Builder {
	return &Builder{db: db}
}

func (b *Builder) Rebuild(ctx context.Context, cfg Config) ([]Stats, error) {
	var result []Stats

	err := b.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tx.Where("1 = 1").Delete(&model.CommunityMembership{})
		tx.Where("1 = 1").Delete(&model.Community{})

		var nodes []model.Node
		if err := tx.Find(&nodes).Error; err != nil {
			return err
		}

		groups := map[string][]model.Node{}
		for _, n := range nodes {
			key := directoryKey(n.FilePath, cfg.Depth)
			groups[key] = append(groups[key], n)
		}

		communityMap := map[string]*model.Community{}
		for key := range groups {
			c := model.Community{
				Key:      key,
				Label:    key,
				Strategy: "directory",
			}
			if err := tx.Create(&c).Error; err != nil {
				return err
			}
			communityMap[key] = &c
		}

		nodeComm := map[uint]string{}
		for key, ns := range groups {
			for _, n := range ns {
				m := model.CommunityMembership{
					CommunityID: communityMap[key].ID,
					NodeID:      n.ID,
				}
				if err := tx.Create(&m).Error; err != nil {
					return err
				}
				nodeComm[n.ID] = key
			}
		}

		var edges []model.Edge
		if err := tx.Find(&edges).Error; err != nil {
			return err
		}

		type edgeCounts struct {
			internal int64
			external int64
		}
		counts := map[string]*edgeCounts{}
		for key := range groups {
			counts[key] = &edgeCounts{}
		}

		for _, e := range edges {
			fromKey, fromOK := nodeComm[e.FromNodeID]
			toKey, toOK := nodeComm[e.ToNodeID]
			if !fromOK || !toOK {
				continue
			}
			if fromKey == toKey {
				counts[fromKey].internal++
			} else {
				counts[fromKey].external++
			}
		}

		for key, c := range communityMap {
			ec := counts[key]
			var cohesion float64
			total := ec.internal + ec.external
			if total > 0 {
				cohesion = float64(ec.internal) / float64(total)
			}
			result = append(result, Stats{
				Community:     *c,
				NodeCount:     int64(len(groups[key])),
				InternalEdges: ec.internal,
				ExternalEdges: ec.external,
				Cohesion:      cohesion,
			})
		}

		return nil
	})

	return result, err
}

func directoryKey(filePath string, depth int) string {
	dir := path.Dir(filePath)
	parts := strings.Split(dir, "/")
	if len(parts) > depth {
		parts = parts[:depth]
	}
	return strings.Join(parts, "/")
}
