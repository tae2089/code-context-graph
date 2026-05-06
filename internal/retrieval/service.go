package retrieval

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

// FromDB builds a retrieve_docs response from persisted graph nodes and annotations.
// @intent orchestrate DB-backed retrieve_docs lookup through search backend first and namespace-scoped scan fallback second.
// @requires Service.DB must be configured and limit must be positive for meaningful results.
// @ensures results are grouped one-per-file, annotation-enriched, and optionally populated with bounded content.
// @sideEffect queries the configured database and may invoke an injected content reader.
func (s *Service) FromDB(ctx context.Context, namespace, query string, limit, contentLimit int, read ContentReader) (Response, error) {
	response := Response{Results: []Result{}}
	if s.DB == nil {
		return response, fmt.Errorf("DB not configured")
	}
	if limit <= 0 {
		return response, nil
	}

	effectiveNamespace := ctxns.Normalize(namespace)
	ctx = ctxns.WithNamespace(ctx, effectiveNamespace)

	candidates := s.searchCandidates(ctx, query, limit)
	groups, nodeIDs := GroupCandidatesByFile(candidates, limit)
	if len(groups) == 0 {
		var err error
		candidates, err = s.scanDBCandidates(ctx, effectiveNamespace, query)
		if err != nil {
			return response, err
		}
		groups, nodeIDs = GroupCandidatesByFile(candidates, limit)
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
		result := Result{RetrieveResult: BuildDBResult(group, annotations, terms, idx)}
		result.Matches = DBMatches(group.Nodes, annotations)
		if read != nil && contentLimit > 0 {
			content, truncated, err := read(ctx, contentNamespace(namespace), result.DocPath, contentLimit)
			if err == nil {
				result.Content = content
				result.ContentTruncated = truncated
			} else if !errors.Is(err, os.ErrNotExist) {
				return response, err
			}
		}
		response.Results = append(response.Results, result)
	}
	return response, nil
}

// @intent ask the configured FTS backend for a bounded candidate set while letting callers fall back when search is unavailable.
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

// scanDBCandidates collects fallback candidates by scanning namespace-scoped nodes and annotations.
// @intent preserve retrieve_docs availability when backend FTS is missing, failing, or returns no grouped files.
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
		Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("retrieve docs DB candidates: %w", err)
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
