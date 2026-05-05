package fallback

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/store"
	"gorm.io/gorm"
)

type Options struct{}

type SuspectEdge struct {
	Edge    model.Edge
	Source  model.Node
	Target  model.Node
	Suspect bool
}

type Service struct {
	db    *gorm.DB
	store store.GraphStore
}

func New(db *gorm.DB, graphStore store.GraphStore) *Service {
	return &Service{db: db, store: graphStore}
}

func (s *Service) FindSuspects(ctx context.Context, opts Options) ([]SuspectEdge, error) {
	_ = opts
	ns := ctxns.FromContext(ctx)
	var edges []model.Edge
	if err := s.db.WithContext(ctx).
		Where("namespace = ? AND kind = ?", ns, model.EdgeKindFallbackCalls).
		Order("fingerprint ASC").
		Find(&edges).Error; err != nil {
		return nil, err
	}

	results := make([]SuspectEdge, 0, len(edges))
	for _, edge := range edges {
		source, err := s.store.GetNodeByID(ctx, edge.FromNodeID)
		if err != nil || source == nil {
			return nil, err
		}
		target, err := s.store.GetNodeByID(ctx, edge.ToNodeID)
		if err != nil || target == nil {
			return nil, err
		}
		sourceAnn, err := s.store.GetAnnotation(ctx, source.ID)
		if err != nil {
			return nil, err
		}
		targetAnn, err := s.store.GetAnnotation(ctx, target.ID)
		if err != nil {
			return nil, err
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
	return results, nil
}

var tokenSplitter = regexp.MustCompile(`[^a-z0-9]+`)

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
