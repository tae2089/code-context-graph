package retrieval

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// FromDB builds a doc search/retrieval response from persisted graph nodes and annotations.
// @intent orchestrate DB-backed doc lookup through search backend first and namespace-scoped scan supplementation second.
// @requires Service.Repository must be configured and limit must be positive for meaningful results.
// @ensures results are grouped one-per-file, annotation-enriched, and optionally populated with bounded content.
// @sideEffect queries the configured database and may invoke an injected content reader.
func (s *Service) FromDB(ctx context.Context, namespace, query string, limit, contentLimit int, read ContentReader) (Response, error) {
	response := Response{Results: []Result{}}
	if s.Repository == nil {
		return response, fmt.Errorf("retrieval repository not configured")
	}
	if limit <= 0 {
		return response, nil
	}

	effectiveNamespace := requestctx.Normalize(namespace)
	ctx = requestctx.WithNamespace(ctx, effectiveNamespace)

	candidateGroupLimit := DBCandidateLimit(limit)
	candidates := s.searchCandidates(ctx, query, limit)
	// Capture the search engine's per-file rank order before the scan supplement is merged in,
	// so engine hits keep their relevance ordering and outrank scan-only supplements.
	ftsRanks := ftsFileRanks(candidates)
	groups, nodeIDs := GroupCandidatesByFile(candidates, candidateGroupLimit)
	// @intent supplement with a DB scan only when FTS underfills the caller's requested
	// result count, not whenever it returns fewer than the wide candidate ceiling.
	// @domainRule scanning the whole namespace on every query makes retrieval O(namespace);
	// gating on limit keeps the scan a genuine fallback for sparse-FTS queries.
	if len(groups) < limit {
		scanned, err := s.scanDBCandidates(ctx, query)
		if err != nil {
			return response, err
		}
		candidates = mergeCandidates(candidates, scanned)
		groups, nodeIDs = GroupCandidatesByFile(candidates, candidateGroupLimit)
	}
	if len(groups) == 0 {
		return response, nil
	}

	annotations, err := s.batchAnnotations(ctx, nodeIDs)
	if err != nil {
		return response, err
	}
	terms := MatchedTerms(query)
	response.Results = make([]Result, 0, len(groups))
	for idx, group := range groups {
		result := Result{RetrieveResult: BuildDBResult(group, annotations, terms, idx)}
		result.Matches = DBMatches(group.Nodes, annotations)
		response.Results = append(response.Results, result)
	}
	sortRetrieveResults(response.Results, ftsRanks)
	if len(response.Results) > limit {
		response.Results = response.Results[:limit]
	}
	if read != nil && contentLimit > 0 {
		for i := range response.Results {
			content, truncated, err := read(ctx, contentNamespace(namespace), response.Results[i].DocPath, contentLimit)
			if err == nil {
				response.Results[i].Content = content
				response.Results[i].ContentTruncated = truncated
			} else if !errors.Is(err, os.ErrNotExist) {
				return response, err
			}
		}
	}
	return response, nil
}

// @intent ask the configured FTS search backend for a bounded candidate set while letting callers supplement from DB scan when search is unavailable.
// @domainRule FTS query errors are non-fatal for doc retrieval because DB scan can still produce namespace-scoped file candidates.
func (s *Service) searchCandidates(ctx context.Context, query string, limit int) []graph.Node {
	if s.Search == nil {
		return nil
	}
	candidates, err := s.Search.Query(ctx, query, DBCandidateLimit(limit))
	if err != nil {
		return nil
	}
	return candidates
}

// scanDBCandidates collects supplemental candidates by scanning namespace-scoped nodes and annotations.
// @intent supplement doc retrieval candidates when backend FTS is missing, failing, or too narrow.
func (s *Service) scanDBCandidates(ctx context.Context, query string) ([]graph.Node, error) {
	terms := MatchedTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}

	nodes, err := s.Repository.ScanCandidates(ctx, retrievableNodeKinds, scanRowCap)
	if err != nil {
		return nil, fmt.Errorf("doc retrieval DB candidates: %w", err)
	}
	if len(nodes) == scanRowCap {
		// The scan hit its ceiling: matching nodes sorting after the cap are not considered.
		slog.WarnContext(ctx, "doc retrieval fallback scan truncated at cap; some matches may be omitted",
			"namespace", requestctx.FromContext(ctx), "cap", scanRowCap)
	}
	if len(nodes) == 0 {
		return nil, nil
	}

	filtered := make([]graph.Node, 0, len(nodes))
	for _, node := range nodes {
		if NodeMatchesTerms(node, terms) {
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}

// @intent merge FTS and DB-scan candidates without duplicating node evidence already returned by the backend.
func mergeCandidates(primary, supplemental []graph.Node) []graph.Node {
	if len(primary) == 0 {
		return supplemental
	}
	if len(supplemental) == 0 {
		return primary
	}
	seen := make(map[uint]struct{}, len(primary)+len(supplemental))
	merged := make([]graph.Node, 0, len(primary)+len(supplemental))
	for _, node := range primary {
		if node.ID != 0 {
			seen[node.ID] = struct{}{}
		}
		merged = append(merged, node)
	}
	for _, node := range supplemental {
		if node.ID != 0 {
			if _, ok := seen[node.ID]; ok {
				continue
			}
			seen[node.ID] = struct{}{}
		}
		merged = append(merged, node)
	}
	return merged
}

// ftsFileRanks records each file's best (earliest) position in the search engine's
// rank-ordered candidate list, keyed by file path.
// @intent preserve the search backend's relevance ordering as an authoritative ranking signal.
func ftsFileRanks(candidates []graph.Node) map[string]int {
	ranks := make(map[string]int, len(candidates))
	for pos, node := range candidates {
		if !IsRetrievableNodeKind(node.Kind) {
			continue
		}
		filePath := strings.TrimSpace(node.FilePath)
		if filePath == "" {
			continue
		}
		if _, seen := ranks[filePath]; !seen {
			ranks[filePath] = pos
		}
	}
	return ranks
}

// @intent order DB retrieve results engine-first: search-backend hits keep their relevance rank
// and outrank scan-only supplements, with the structured annotation score as the refining signal.
func sortRetrieveResults(results []Result, ftsRanks map[string]int) {
	rankOf := func(r Result) (int, bool) {
		pos, ok := ftsRanks[strings.TrimPrefix(r.ID, "file:")]
		return pos, ok
	}
	sort.SliceStable(results, func(i, j int) bool {
		ri, iHit := rankOf(results[i])
		rj, jHit := rankOf(results[j])
		if iHit != jHit {
			return iHit // engine hits before scan-only supplements
		}
		if iHit && ri != rj {
			return ri < rj // earlier engine rank wins
		}
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if len(results[i].MatchedTerms) != len(results[j].MatchedTerms) {
			return len(results[i].MatchedTerms) > len(results[j].MatchedTerms)
		}
		return strings.Join(results[i].Path, "/") < strings.Join(results[j].Path, "/")
	})
}

// @intent load structured annotations for candidate nodes in one namespace-scoped query so reranking evidence stays bounded.
func (s *Service) batchAnnotations(ctx context.Context, nodeIDs []uint) (map[uint]*graph.Annotation, error) {
	annotations := make(map[uint]*graph.Annotation, len(nodeIDs))
	if len(nodeIDs) == 0 {
		return annotations, nil
	}
	rows, err := s.Repository.Annotations(ctx, nodeIDs)
	if err != nil {
		return nil, fmt.Errorf("batch doc retrieval annotations: %w", err)
	}
	for nodeID, annotation := range rows {
		annotations[nodeID] = annotation
	}
	return annotations, nil
}

// @intent map the default namespace to shared docs paths while preserving named namespace-relative content lookup.
func contentNamespace(namespace string) string {
	if namespace == requestctx.DefaultNamespace {
		return ""
	}
	return namespace
}
