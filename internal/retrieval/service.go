package retrieval

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

// FromDB builds a retrieve_docs response from persisted graph nodes and annotations.
// @intent orchestrate DB-backed retrieve_docs lookup through search backend first and namespace-scoped scan supplementation second.
// @requires Service.DB must be configured and limit must be positive for meaningful results.
// @ensures results are grouped one-per-file, annotation-enriched, and optionally populated with bounded content.
// @sideEffect queries the configured database and may invoke an injected content reader.
func (s *Service) FromDB(ctx context.Context, namespace, query string, limit, contentLimit int, read ContentReader) (Response, error) {
	return s.FromDBWithOptions(ctx, namespace, query, limit, contentLimit, read, Options{})
}

// Options controls optional DB-backed retrieval diagnostics.
// @intent let DB-backed retrieve_docs expose opt-in scoring diagnostics without changing the default response shape.
type Options struct {
	Explain bool
}

// FromDBWithOptions builds a retrieve_docs response from DB data and optional diagnostics.
// @intent support DB-primary retrieve_docs while preserving doc-index-compatible explain response fields.
// @requires Service.DB must be configured and limit must be positive for meaningful results.
// @ensures results are grouped one-per-file, annotation-enriched, and optionally populated with bounded content.
// @sideEffect queries the configured database and may invoke an injected content reader.
func (s *Service) FromDBWithOptions(ctx context.Context, namespace, query string, limit, contentLimit int, read ContentReader, opts Options) (Response, error) {
	response := Response{Results: []Result{}}
	if s.DB == nil {
		return response, fmt.Errorf("DB not configured")
	}
	if limit <= 0 {
		return response, nil
	}

	effectiveNamespace := ctxns.Normalize(namespace)
	ctx = ctxns.WithNamespace(ctx, effectiveNamespace)

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
		scanned, err := s.scanDBCandidates(ctx, effectiveNamespace, query)
		if err != nil {
			return response, err
		}
		candidates = mergeCandidates(candidates, scanned)
		groups, nodeIDs = GroupCandidatesByFile(candidates, candidateGroupLimit)
	}
	if len(groups) == 0 {
		return response, nil
	}

	annotations, err := s.batchAnnotations(ctx, effectiveNamespace, nodeIDs)
	if err != nil {
		return response, err
	}
	terms := MatchedTerms(query)
	response.Results = make([]Result, 0, len(groups))
	for idx, group := range groups {
		result := Result{RetrieveResult: BuildDBResultWithOptions(group, annotations, terms, idx, opts)}
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
// @domainRule FTS query errors are non-fatal for retrieve_docs because DB scan can still produce namespace-scoped file candidates.
func (s *Service) searchCandidates(ctx context.Context, query string, limit int) []model.Node {
	if s.SearchBackend == nil {
		return nil
	}
	candidates, err := s.SearchBackend.Query(ctx, s.DB, query, DBCandidateLimit(limit))
	if err != nil {
		return nil
	}
	return candidates
}

// scanDBCandidates collects supplemental candidates by scanning namespace-scoped nodes and annotations.
// @intent supplement retrieve_docs candidates when backend FTS is missing, failing, or too narrow.
func (s *Service) scanDBCandidates(ctx context.Context, namespace, query string) ([]model.Node, error) {
	terms := MatchedTerms(query)
	if len(terms) == 0 {
		return nil, nil
	}

	var nodes []model.Node
	if err := s.DB.WithContext(ctx).
		Where("namespace = ?", namespace).
		Where("kind IN ?", retrievableNodeKinds).
		Preload("Annotation.Tags").
		Order("file_path ASC, qualified_name ASC, id ASC").
		Limit(scanRowCap).
		Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("retrieve docs DB candidates: %w", err)
	}
	if len(nodes) == scanRowCap {
		// The scan hit its ceiling: matching nodes sorting after the cap are not considered.
		slog.WarnContext(ctx, "retrieve_docs fallback scan truncated at cap; some matches may be omitted",
			"namespace", namespace, "cap", scanRowCap)
	}
	if len(nodes) == 0 {
		return nil, nil
	}

	filtered := make([]model.Node, 0, len(nodes))
	for _, node := range nodes {
		if NodeMatchesTerms(node, terms) {
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}

// @intent merge FTS and DB-scan candidates without duplicating node evidence already returned by the backend.
func mergeCandidates(primary, supplemental []model.Node) []model.Node {
	if len(primary) == 0 {
		return supplemental
	}
	if len(supplemental) == 0 {
		return primary
	}
	seen := make(map[uint]struct{}, len(primary)+len(supplemental))
	merged := make([]model.Node, 0, len(primary)+len(supplemental))
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
func ftsFileRanks(candidates []model.Node) map[string]int {
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
func (s *Service) batchAnnotations(ctx context.Context, namespace string, nodeIDs []uint) (map[uint]*model.Annotation, error) {
	annotations := make(map[uint]*model.Annotation, len(nodeIDs))
	if len(nodeIDs) == 0 {
		return annotations, nil
	}
	var rows []model.Annotation
	if err := s.DB.WithContext(ctx).
		Joins("JOIN nodes ON nodes.id = annotations.node_id").
		Where("annotations.node_id IN ? AND nodes.namespace = ?", nodeIDs, namespace).
		Preload("Tags").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("batch retrieve_docs annotations: %w", err)
	}
	for i := range rows {
		annotations[rows[i].NodeID] = &rows[i]
	}
	return annotations, nil
}

// @intent map the default namespace to shared docs paths while preserving named namespace-relative content lookup.
func contentNamespace(namespace string) string {
	if namespace == ctxns.DefaultNamespace {
		return ""
	}
	return namespace
}
