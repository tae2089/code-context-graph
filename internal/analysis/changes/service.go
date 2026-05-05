// @index Git diff 기반 변경 감지 및 리스크 점수 산출. 변경된 함수의 영향 범위를 분석한다.
package changes

import (
	"container/heap"
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
// @intent bound handler response allocation while preserving the same sorted risk window that legacy Analyze would expose.
// @domainRule pagination limits returned items, but compute cost still scales with scoring every changed node to preserve legacy ordering.
// @domainRule entries are sorted by descending risk_score, then file_path, then qualified_name for stable ordering.
func (s *Service) AnalyzePage(ctx context.Context, repoDir, baseRef string, req paging.Request) (Result, error) {
	normalized, err := paging.Normalize(req)
	if err != nil {
		return Result{}, err
	}

	hits, err := s.changedNodeHits(ctx, repoDir, baseRef)
	if err != nil || len(hits) == 0 {
		return Result{}, err
	}
	total := len(hits)
	if normalized.Offset >= total {
		return Result{Items: []RiskEntry{}, Pagination: paging.BuildPage(normalized, 0, false)}, nil
	}

	windowSize := min(total, normalized.Offset+normalized.Limit+1)
	candidates, err := selectTopRiskCandidates(s.db, ctx, hits, windowSize)
	if err != nil {
		return Result{}, err
	}
	sortRiskCandidates(candidates)

	end := min(normalized.Offset+normalized.Limit, len(candidates))
	window := candidates[normalized.Offset:end]
	hasMore := total > normalized.Offset+normalized.Limit
	out := riskCandidatesToEntries(window)
	return Result{Items: out, Pagination: paging.BuildPage(normalized, len(out), hasMore)}, nil
}

// ChangedNodeIDs returns the unique graph node IDs touched by the current diff.
// @intent let downstream analyzers reuse change detection without paying risk-score or AnalyzePage pagination-loop costs.
// @sideEffect executes git diff via GitClient.
// @ensures returned IDs are deterministic by file path, start line, qualified name, then node ID.
func (s *Service) ChangedNodeIDs(ctx context.Context, repoDir, baseRef string) ([]uint, error) {
	hits, err := s.changedNodeHits(ctx, repoDir, baseRef)
	if err != nil || len(hits) == 0 {
		return nil, err
	}
	nodes := make([]model.Node, 0, len(hits))
	for _, hit := range hits {
		nodes = append(nodes, hit.node)
	}
	sortNodesForChangeOrder(nodes)
	ids := make([]uint, len(nodes))
	for i, node := range nodes {
		ids[i] = node.ID
	}
	return ids, nil
}

// changedNodeHits matches the repository diff to graph nodes without risk scoring.
// @intent share the git-diff and node-overlap pipeline between legacy Analyze, paged AnalyzePage, and flow impact lookup.
// @sideEffect executes git diff via GitClient.
func (s *Service) changedNodeHits(ctx context.Context, repoDir, baseRef string) (map[uint]*hitInfo, error) {
	hunksByFile, files, err := s.collectDiffHunks(ctx, repoDir, baseRef)
	if err != nil || hunksByFile == nil {
		return nil, err
	}
	return matchHunksToNodes(s.db, ctx, files, hunksByFile)
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

// sortNodesForChangeOrder orders changed nodes deterministically for downstream set consumers.
// @intent prevent flow lookups from depending on database or map iteration order.
func sortNodesForChangeOrder(nodes []model.Node) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].FilePath != nodes[j].FilePath {
			return nodes[i].FilePath < nodes[j].FilePath
		}
		if nodes[i].StartLine != nodes[j].StartLine {
			return nodes[i].StartLine < nodes[j].StartLine
		}
		if nodes[i].QualifiedName != nodes[j].QualifiedName {
			return nodes[i].QualifiedName < nodes[j].QualifiedName
		}
		return nodes[i].ID < nodes[j].ID
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
	if err := db.WithContext(ctx).
		Where("namespace = ? AND file_path IN ?", ctxns.FromContext(ctx), files).
		Order("file_path ASC").
		Order("start_line ASC").
		Order("qualified_name ASC").
		Order("id ASC").
		Find(&allNodes).Error; err != nil {
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

// riskCandidate keeps score data before callers choose whether to materialize all entries or one page.
// @intent separate risk ordering from response entry allocation for paged consumers.
type riskCandidate struct {
	node      model.Node
	hunkCount int
	riskScore float64
}

// computeRiskScores calculates risk for each hit node based on outgoing edge count.
// @intent weight changed nodes by both diff overlap and graph connectivity to prioritize risky edits.
func computeRiskScores(db *gorm.DB, ctx context.Context, hits map[uint]*hitInfo) ([]RiskEntry, error) {
	candidates, err := computeRiskCandidates(db, ctx, hits)
	if err != nil {
		return nil, err
	}
	return riskCandidatesToEntries(candidates), nil
}

// computeRiskCandidates calculates sortable risk candidates for each hit node.
// @intent allow paged callers to sort and slice before converting candidates into wire-facing risk entries.
func computeRiskCandidates(db *gorm.DB, ctx context.Context, hits map[uint]*hitInfo) ([]riskCandidate, error) {
	return computeTopRiskCandidates(db, ctx, hits, len(hits))
}

// selectTopRiskCandidates returns the best N risk candidates without materializing the full candidate slice.
// @intent preserve AnalyzePage ordering while capping page-path memory and sort work to the requested window.
func selectTopRiskCandidates(db *gorm.DB, ctx context.Context, hits map[uint]*hitInfo, limit int) ([]riskCandidate, error) {
	return computeTopRiskCandidates(db, ctx, hits, limit)
}

// computeTopRiskCandidates scores all hit nodes, but only retains the top limit candidates when limit is smaller than the hit set.
// @intent keep legacy Analyze behavior available while letting paged callers avoid full candidate allocation and full sorting.
func computeTopRiskCandidates(db *gorm.DB, ctx context.Context, hits map[uint]*hitInfo, limit int) ([]riskCandidate, error) {
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

	if limit <= 0 || limit >= len(hits) {
		result := make([]riskCandidate, 0, len(hits))
		for id, info := range hits {
			outEdges := outMap[id]
			result = append(result, riskCandidate{
				node:      info.node,
				hunkCount: info.hunkCount,
				riskScore: float64(info.hunkCount) * float64(outEdges+1),
			})
		}
		return result, nil
	}

	h := make(riskCandidateHeap, 0, limit)
	result := make([]riskCandidate, 0, len(hits))
	for id, info := range hits {
		outEdges := outMap[id]
		candidate := riskCandidate{
			node:      info.node,
			hunkCount: info.hunkCount,
			riskScore: float64(info.hunkCount) * float64(outEdges+1),
		}
		if len(h) < limit {
			heap.Push(&h, candidate)
			continue
		}
		if compareRiskCandidates(candidate, h[0]) < 0 {
			h[0] = candidate
			heap.Fix(&h, 0)
		}
	}
	result = make([]riskCandidate, len(h))
	copy(result, h)
	return result, nil
}

// compareRiskCandidates returns -1 when a should sort before b, 1 when after, and 0 when tied.
// @intent centralize legacy Analyze ordering so heap selection and final sorting stay consistent.
func compareRiskCandidates(a, b riskCandidate) int {
	if a.riskScore != b.riskScore {
		if a.riskScore > b.riskScore {
			return -1
		}
		return 1
	}
	if a.node.FilePath != b.node.FilePath {
		if a.node.FilePath < b.node.FilePath {
			return -1
		}
		return 1
	}
	if a.node.StartLine != b.node.StartLine {
		if a.node.StartLine < b.node.StartLine {
			return -1
		}
		return 1
	}
	if a.node.QualifiedName != b.node.QualifiedName {
		if a.node.QualifiedName < b.node.QualifiedName {
			return -1
		}
		return 1
	}
	if a.node.ID != b.node.ID {
		if a.node.ID < b.node.ID {
			return -1
		}
		return 1
	}
	return 0
}

// sortRiskCandidates mirrors sortRiskEntries before entries are materialized.
// @intent preserve Analyze ordering for AnalyzePage while reducing page response allocation work.
func sortRiskCandidates(entries []riskCandidate) {
	sort.SliceStable(entries, func(i, j int) bool {
		return compareRiskCandidates(entries[i], entries[j]) < 0
	})
}

// riskCandidateHeap keeps the worst retained candidate at the root so better candidates can replace it.
// @intent select the top AnalyzePage window without allocating or sorting the full candidate set.
type riskCandidateHeap []riskCandidate

// @intent report retained candidate count for container/heap operations.
func (h riskCandidateHeap) Len() int { return len(h) }

// @intent invert risk ordering so the heap root stays the worst retained candidate.
func (h riskCandidateHeap) Less(i, j int) bool {
	return compareRiskCandidates(h[i], h[j]) > 0
}

// @intent exchange retained candidates during heap rebalancing.
// @mutates h.
func (h riskCandidateHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

// @intent append a retained risk candidate supplied by container/heap.
// @requires x is riskCandidate.
// @mutates h.
func (h *riskCandidateHeap) Push(x any) {
	*h = append(*h, x.(riskCandidate))
}

// @intent remove and return the last retained candidate during container/heap pop operations.
// @requires len(*h) > 0.
// @mutates h.
func (h *riskCandidateHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// riskCandidatesToEntries converts internal score candidates into public RiskEntry values.
// @intent keep candidate-based compute paths from changing the legacy Analyze return type.
func riskCandidatesToEntries(candidates []riskCandidate) []RiskEntry {
	entries := make([]RiskEntry, len(candidates))
	for i, candidate := range candidates {
		entries[i] = RiskEntry{
			Node:      candidate.node,
			HunkCount: candidate.hunkCount,
			RiskScore: candidate.riskScore,
		}
	}
	return entries
}
