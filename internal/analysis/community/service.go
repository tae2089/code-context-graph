// @index 디렉토리 기반 커뮤니티 탐지. 코드베이스를 논리적 모듈로 분할하고 응집도를 측정한다.
package community

import (
	"context"
	"path"
	"strings"

	"github.com/imtaebin/code-context-graph/internal/ctxns"
	"github.com/imtaebin/code-context-graph/internal/model"
	"gorm.io/gorm"
)

// Config controls directory-based community grouping.
// @intent define how file paths are collapsed into module community keys
type Config struct {
	BaseDir string
	Depth   int
}

// Stats summarizes one rebuilt community.
// @intent report membership and cohesion metrics for a detected community
type Stats struct {
	Community     model.Community
	NodeCount     int64
	InternalEdges int64
	ExternalEdges int64
	Cohesion      float64
}

// Builder rebuilds communities from graph data.
// @intent persist directory-based module boundaries into community tables
type Builder struct {
	db *gorm.DB
}

// New creates a community builder.
// @intent construct a builder that writes detected communities to the database
func New(db *gorm.DB) *Builder {
	return &Builder{db: db}
}

// Rebuild creates communities by grouping nodes by directory path.
// Used by MCP run_postprocess tool and architecture_map prompt.
//
// @return community stats with node count, internal/external edges, cohesion score
// @intent partition codebase into logical modules for architecture analysis
// @domainRule groups nodes by file path directory up to configured depth
// @domainRule cohesion equals internal edges divided by total edges
// @sideEffect deletes all existing communities and memberships before rebuilding
// @mutates Community CommunityMembership tables
func (b *Builder) Rebuild(ctx context.Context, cfg Config) ([]Stats, error) {
	var result []Stats
	ns := ctxns.FromContext(ctx)

	err := b.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := deleteCommunities(tx, ns); err != nil {
			return err
		}

		groups, err := groupNodesByDirectory(tx, ctx, cfg.Depth)
		if err != nil {
			return err
		}

		communityMap, nodeComm, err := createCommunitiesAndMemberships(tx, groups, ns)
		if err != nil {
			return err
		}

		counts, err := countEdgesByCommunity(tx, groups, nodeComm)
		if err != nil {
			return err
		}

		if err := aggregateDescriptions(tx, groups, communityMap); err != nil {
			return err
		}

		result = buildStats(groups, communityMap, counts)
		return nil
	})

	return result, err
}

func deleteCommunities(tx *gorm.DB, ns string) error {
	if ns == "" {
		if err := tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.CommunityMembership{}).Error; err != nil {
			return err
		}
		return tx.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.Community{}).Error
	}
	var ids []uint
	if err := tx.Table("community_memberships").
		Select("DISTINCT community_id").
		Joins("JOIN nodes ON nodes.id = community_memberships.node_id").
		Where("nodes.namespace = ?", ns).
		Pluck("community_id", &ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if err := tx.Where("community_id IN ?", ids).Delete(&model.CommunityMembership{}).Error; err != nil {
		return err
	}
	return tx.Where("id IN ?", ids).Delete(&model.Community{}).Error
}

func groupNodesByDirectory(tx *gorm.DB, ctx context.Context, depth int) (map[string][]model.Node, error) {
	var nodes []model.Node
	ns := ctxns.FromContext(ctx)
	if err := tx.Where("namespace = ?", ns).Find(&nodes).Error; err != nil {
		return nil, err
	}

	groups := map[string][]model.Node{}
	for _, n := range nodes {
		key := directoryKey(n.FilePath, depth)
		groups[key] = append(groups[key], n)
	}
	return groups, nil
}

func createCommunitiesAndMemberships(tx *gorm.DB, groups map[string][]model.Node, ns string) (map[string]*model.Community, map[uint]string, error) {
	communityMap := map[string]*model.Community{}
	for key := range groups {
		c := model.Community{
			Namespace: ns,
			Key:       key,
			Label:     key,
			Strategy:  "directory",
		}
		if err := tx.Create(&c).Error; err != nil {
			return nil, nil, err
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
				return nil, nil, err
			}
			nodeComm[n.ID] = key
		}
	}

	return communityMap, nodeComm, nil
}

type edgeCounts struct {
	internal int64
	external int64
}

func countEdgesByCommunity(tx *gorm.DB, groups map[string][]model.Node, nodeComm map[uint]string) (map[string]*edgeCounts, error) {
	counts := map[string]*edgeCounts{}
	for key := range groups {
		counts[key] = &edgeCounts{}
	}

	var batchEdges []model.Edge
	if err := tx.FindInBatches(&batchEdges, 500, func(tx *gorm.DB, batch int) error {
		for _, e := range batchEdges {
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
		return nil
	}).Error; err != nil {
		return nil, err
	}

	return counts, nil
}

func aggregateDescriptions(tx *gorm.DB, groups map[string][]model.Node, communityMap map[string]*model.Community) error {
	fileNodeIDs := []uint{}
	for _, ns := range groups {
		for _, n := range ns {
			if n.Kind == model.NodeKindFile {
				fileNodeIDs = append(fileNodeIDs, n.ID)
			}
		}
	}
	if len(fileNodeIDs) == 0 {
		return nil
	}

	annByNode := map[uint]*model.Annotation{}
	var annotations []model.Annotation
	if err := tx.Where("node_id IN ?", fileNodeIDs).Preload("Tags").Find(&annotations).Error; err != nil {
		return err
	}
	for i := range annotations {
		annByNode[annotations[i].NodeID] = &annotations[i]
	}

	for key, c := range communityMap {
		var descriptions []string
		for _, n := range groups[key] {
			if n.Kind != model.NodeKindFile {
				continue
			}
			if ann := annByNode[n.ID]; ann != nil {
				for _, tag := range ann.Tags {
					if tag.Kind == model.TagIndex {
						descriptions = append(descriptions, tag.Value)
					}
				}
			}
		}
		if len(descriptions) > 0 {
			c.Description = strings.Join(descriptions, "; ")
			if err := tx.Save(c).Error; err != nil {
				return err
			}
		}
	}

	return nil
}

func buildStats(groups map[string][]model.Node, communityMap map[string]*model.Community, counts map[string]*edgeCounts) []Stats {
	var result []Stats
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
	return result
}

// directoryKey derives a community key from a file path.
// @intent normalize file paths into stable directory group identifiers
// @param filePath repository-relative source file path
// @param depth maximum number of directory segments to keep
// @return grouped directory prefix used as the community key
// @ensures returned key contains at most depth path segments
func directoryKey(filePath string, depth int) string {
	dir := path.Dir(filePath)
	parts := strings.Split(dir, "/")
	if len(parts) > depth {
		parts = parts[:depth]
	}
	return strings.Join(parts, "/")
}
