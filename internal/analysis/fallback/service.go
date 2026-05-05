// @index Suspect fallback call-edge analysis based on overlapping annotation vocabulary between source and target nodes.
package fallback

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/paging"
	"github.com/tae2089/code-context-graph/internal/store"
	"gorm.io/gorm"
)

// @intent carry bounded pagination inputs for suspect fallback analysis.
type Options struct {
	Page paging.Request
}

// Result carries one suspect fallback edge page plus pagination metadata.
// @intent let callers expose bounded fallback suspect responses while preserving legacy fields.
type Result struct {
	Items      []SuspectEdge
	Pagination paging.Page
}

// @intent carry one fallback edge with its endpoint nodes and suspect classification for downstream reporting.
type SuspectEdge struct {
	Edge    model.Edge
	Source  model.Node
	Target  model.Node
	Suspect bool
}

// @intent bundle graph DB and annotation-store access needed to detect low-confidence fallback edges.
type Service struct {
	db    *gorm.DB
	store store.GraphStore
}

// @intent construct the suspect fallback analyzer from the active graph DB and annotation-capable store.
func New(db *gorm.DB, graphStore store.GraphStore) *Service {
	return &Service{db: db, store: graphStore}
}

// @intent report fallback call edges whose source and target annotations share no intent or domain-rule vocabulary.
// @param ctx carries the namespace that ctxns.FromContext uses to scope analysis.
// @param opts carries pagination bounds for the returned suspect edge window.
// @return returns suspect edges sorted by source qualified name and then target qualified name.
// @domainRule exclude an edge from suspect results as soon as source and target share any intent/domainRule token.
// @see mcp.handlers.findSuspectFallbackEdges
func (s *Service) FindSuspects(ctx context.Context, opts Options) ([]SuspectEdge, error) {
	page, err := s.FindSuspectsPage(ctx, opts)
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

// FindSuspectsPage reports suspect fallback call edges and returns one bounded page.
// @intent bound fallback suspect analysis before annotation lookups so large graphs cannot expand unbounded responses.
// @domainRule fetch limit+offset+1 fallback edges before suspect filtering; paging bounds the scanned fallback-edge window, so returned suspect items may be fewer than limit while HasMore still reflects additional fallback edges beyond that window.
func (s *Service) FindSuspectsPage(ctx context.Context, opts Options) (Result, error) {
	req, err := paging.Normalize(opts.Page)
	if err != nil {
		return Result{}, err
	}

	ns := ctxns.FromContext(ctx)
	var edges []model.Edge
	if err := s.db.WithContext(ctx).
		Where("namespace = ? AND kind = ?", ns, model.EdgeKindFallbackCalls).
		Order("fingerprint ASC").
		Limit(req.Limit + 1).
		Offset(req.Offset).
		Find(&edges).Error; err != nil {
		return Result{}, err
	}
	hasMore := len(edges) > req.Limit
	if hasMore {
		edges = edges[:req.Limit]
	}

	results := make([]SuspectEdge, 0, len(edges))
	for _, edge := range edges {
		source, err := s.store.GetNodeByID(ctx, edge.FromNodeID)
		if err != nil || source == nil {
			return Result{}, err
		}
		target, err := s.store.GetNodeByID(ctx, edge.ToNodeID)
		if err != nil || target == nil {
			return Result{}, err
		}
		sourceAnn, err := s.store.GetAnnotation(ctx, source.ID)
		if err != nil {
			return Result{}, err
		}
		targetAnn, err := s.store.GetAnnotation(ctx, target.ID)
		if err != nil {
			return Result{}, err
		}
		if annotationsOverlap(sourceAnn, targetAnn) {
			continue
		}
		results = append(results, SuspectEdge{Edge: edge, Source: *source, Target: *target, Suspect: true})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Source.QualifiedName != results[j].Source.QualifiedName {
			return results[i].Source.QualifiedName < results[j].Source.QualifiedName
		}
		return results[i].Target.QualifiedName < results[j].Target.QualifiedName
	})
	return Result{Items: results, Pagination: paging.BuildPage(req, len(results), hasMore)}, nil
}

var tokenSplitter = regexp.MustCompile(`[^a-z0-9]+`)

// @intent suppress suspect reports when source and target annotations already share meaningful intent or domain-rule vocabulary.
func annotationsOverlap(source, target *model.Annotation) bool {
	left := annotationTokens(source)
	right := annotationTokens(target)
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	for token := range left {
		if _, ok := right[token]; ok {
			return true
		}
	}
	return false
}

// @intent extract normalized intent/domainRule tokens so overlap checks can ignore punctuation and casing.
// @domainRule compare only TagIntent and TagDomainRule categories.
// @domainRule discard tokens shorter than three characters as noise.
func annotationTokens(ann *model.Annotation) map[string]struct{} {
	if ann == nil {
		return nil
	}
	tokens := map[string]struct{}{}
	for _, tag := range ann.Tags {
		if tag.Kind != model.TagIntent && tag.Kind != model.TagDomainRule {
			continue
		}
		for _, token := range tokenSplitter.Split(strings.ToLower(tag.Value), -1) {
			if len(token) < 3 {
				continue
			}
			tokens[token] = struct{}{}
		}
	}
	return tokens
}
