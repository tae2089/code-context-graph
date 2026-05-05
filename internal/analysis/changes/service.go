// @index Git diff 기반 변경 감지 및 리스크 점수 산출. 변경된 함수의 영향 범위를 분석한다.
package changes

import (
	"context"
	"sort"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/paging"
	"gorm.io/gorm"
)

// GitClient provides git diff data needed for change analysis.
// @intent abstract git operations so risk analysis can consume changed files and hunks
type GitClient interface {
	ChangedFiles(ctx context.Context, repoDir, baseRef string) ([]string, error)
	DiffHunks(ctx context.Context, repoDir, baseRef string, paths []string) ([]Hunk, error)
}

// Hunk describes a changed line range within one file.
// @intent represent a diff segment that can be matched against graph nodes
type Hunk struct {
	FilePath  string
	StartLine int
	EndLine   int
}

// RiskEntry captures risk metrics for one changed node.
// @intent return the changed node together with overlap count and computed risk
type RiskEntry struct {
	Node      model.Node
	HunkCount int
	RiskScore float64
}

// Result carries one bounded page of risk entries plus pagination metadata.
// @intent expose paged change-risk results while keeping legacy callers working with []RiskEntry.
type Result struct {
	Items      []RiskEntry
	Pagination paging.Page
}

// Service coordinates git-based change detection and graph-backed scoring.
// @intent identify changed nodes and score how risky they are to modify
type Service struct {
	db  *gorm.DB
	git GitClient
}

// New creates a change analysis service.
// @intent wire database and git dependencies into a reusable analyzer
func New(db *gorm.DB, git GitClient) *Service {
	return &Service{db: db, git: git}
}

// Analyze detects changed functions and calculates risk scores.
// Called from review_changes and pre_merge_check MCP prompts.
//
// @param repoDir git repository root path
// @param baseRef git base reference for diff comparison
// @return risk entries with hunk count and risk score per changed function
// @intent identify high-risk code changes before merge
// @domainRule risk score equals hunk count multiplied by outgoing edge count plus one
// @sideEffect executes git diff via GitClient
// @see impact.Analyzer.ImpactRadius
func (s *Service) Analyze(ctx context.Context, repoDir, baseRef string) ([]RiskEntry, error) {
	hunksByFile, files, err := s.collectDiffHunks(ctx, repoDir, baseRef)
	if err != nil || hunksByFile == nil {
		return nil, err
	}

	hits, err := matchHunksToNodes(s.db, ctx, files, hunksByFile)
	if err != nil || len(hits) == 0 {
		return nil, err
	}

	risks, err := computeRiskScores(s.db, ctx, hits)
	if err != nil {
		return nil, err
	}
	sortRiskEntries(risks)
	return risks, nil
}

// AnalyzePage detects changed functions and returns one bounded page of risk entries.
// @intent push pagination into the change-risk service so handlers expose stable limit/offset windows.
// @domainRule entries are sorted by descending risk_score, then file_path, then qualified_name for stable ordering.
func (s *Service) AnalyzePage(ctx context.Context, repoDir, baseRef string, req paging.Request) (Result, error) {
	normalized, err := paging.Normalize(req)
	if err != nil {
		return Result{}, err
	}
	all, err := s.Analyze(ctx, repoDir, baseRef)
	if err != nil {
		return Result{}, err
	}
	total := len(all)
	if normalized.Offset >= total {
		return Result{Items: []RiskEntry{}, Pagination: paging.BuildPage(normalized, 0, false)}, nil
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
	out := make([]RiskEntry, len(window))
	copy(out, window)
	return Result{Items: out, Pagination: paging.BuildPage(normalized, len(out), hasMore)}, nil
}

// sortRiskEntries orders entries deterministically for stable pagination windows.
// @intent guarantee identical limit/offset slices regardless of map iteration order in computeRiskScores.
func sortRiskEntries(entries []RiskEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].RiskScore != entries[j].RiskScore {
			return entries[i].RiskScore > entries[j].RiskScore
		}
		if entries[i].Node.FilePath != entries[j].Node.FilePath {
			return entries[i].Node.FilePath < entries[j].Node.FilePath
		}
		if entries[i].Node.StartLine != entries[j].Node.StartLine {
			return entries[i].Node.StartLine < entries[j].Node.StartLine
		}
		return entries[i].Node.QualifiedName < entries[j].Node.QualifiedName
	})
}

// collectDiffHunks retrieves changed files and their diff hunks from git,
// returning hunks grouped by file path.
// @intent gather the minimal diff context needed before matching git changes back to graph nodes.
func (s *Service) collectDiffHunks(ctx context.Context, repoDir, baseRef string) (map[string][]Hunk, []string, error) {
	files, err := s.git.ChangedFiles(ctx, repoDir, baseRef)
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, nil
	}

	hunks, err := s.git.DiffHunks(ctx, repoDir, baseRef, files)
	if err != nil {
		return nil, nil, err
	}
	if len(hunks) == 0 {
		return nil, nil, nil
	}

	hunksByFile := map[string][]Hunk{}
	for _, h := range hunks {
		hunksByFile[h.FilePath] = append(hunksByFile[h.FilePath], h)
	}
	return hunksByFile, files, nil
}

// hitInfo pairs a graph node with the number of overlapping diff hunks.
// @intent keep per-node diff overlap counts available until final risk scoring runs.
type hitInfo struct {
	node      model.Node
	hunkCount int
}

// matchHunksToNodes finds graph nodes whose line ranges overlap with diff hunks.
// @intent translate file-level diff hunks into the graph nodes that were actually touched.
func matchHunksToNodes(db *gorm.DB, ctx context.Context, files []string, hunksByFile map[string][]Hunk) (map[uint]*hitInfo, error) {
	var allNodes []model.Node
	if err := db.WithContext(ctx).Where("namespace = ? AND file_path IN ?", ctxns.FromContext(ctx), files).Find(&allNodes).Error; err != nil {
		return nil, err
	}

	hits := map[uint]*hitInfo{}
	for _, n := range allNodes {
		fileHunks := hunksByFile[n.FilePath]
		count := 0
		for _, h := range fileHunks {
			if h.StartLine <= n.EndLine && h.EndLine >= n.StartLine {
				count++
			}
		}
		if count > 0 {
			hits[n.ID] = &hitInfo{node: n, hunkCount: count}
		}
	}
	return hits, nil
}

// outCount aggregates outgoing edge counts per node for risk weighting.
// @intent carry GROUP BY results from the edge query into the risk score loop.
type outCount struct {
	FromNodeID uint
	Count      int64
}

// computeRiskScores calculates risk for each hit node based on outgoing edge count.
// @intent weight changed nodes by both diff overlap and graph connectivity to prioritize risky edits.
func computeRiskScores(db *gorm.DB, ctx context.Context, hits map[uint]*hitInfo) ([]RiskEntry, error) {
	nodeIDs := make([]uint, 0, len(hits))
	for id := range hits {
		nodeIDs = append(nodeIDs, id)
	}
	ns := ctxns.FromContext(ctx)
	var outCounts []outCount
	if err := db.WithContext(ctx).
		Model(&model.Edge{}).
		Select("from_node_id, COUNT(*) as count").
		Where("namespace = ? AND from_node_id IN ?", ns, nodeIDs).
		Group("from_node_id").
		Scan(&outCounts).Error; err != nil {
		return nil, err
	}

	outMap := map[uint]int64{}
	for _, oc := range outCounts {
		outMap[oc.FromNodeID] = oc.Count
	}

	result := make([]RiskEntry, 0, len(hits))
	for id, info := range hits {
		outEdges := outMap[id]
		result = append(result, RiskEntry{
			Node:      info.node,
			HunkCount: info.hunkCount,
			RiskScore: float64(info.hunkCount) * float64(outEdges+1),
		})
	}
	return result, nil
}
