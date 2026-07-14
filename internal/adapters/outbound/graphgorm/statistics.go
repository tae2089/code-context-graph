// @index GORM graph statistics adapter for operator and protocol read surfaces.
package graphgorm

import (
	"context"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/app/analyze"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

var _ analyze.StatisticsReader = (*Store)(nil)

// groupedCount is the adapter-local scan target for kind/language aggregates.
// @intent keep GORM aggregate rows private to the statistics adapter.
type groupedCount struct {
	Kind  string
	Count int64
}

// GraphStatistics loads totals and grouped distributions for the active namespace.
// @intent implement the application statistics port while preserving namespace filtering and aggregate semantics.
func (s *Store) GraphStatistics(ctx context.Context) (analyze.GraphStatistics, error) {
	ns := requestctx.FromContext(ctx)
	nodeQ := s.db.WithContext(ctx).Model(&graph.Node{}).Where("namespace = ?", ns)
	edgeQ := s.db.WithContext(ctx).Model(&graph.Edge{}).Where("namespace = ?", ns)
	result := analyze.GraphStatistics{
		NodesByKind:     map[string]int64{},
		NodesByLanguage: map[string]int64{},
		EdgesByKind:     map[string]int64{},
	}
	if err := nodeQ.Count(&result.NodeCount).Error; err != nil {
		return result, trace.Wrap(err, "count nodes")
	}
	if err := edgeQ.Count(&result.EdgeCount).Error; err != nil {
		return result, trace.Wrap(err, "count edges")
	}
	if err := nodeQ.Distinct("file_path").Count(&result.FileCount).Error; err != nil {
		return result, trace.Wrap(err, "count files")
	}

	var nodeKinds []groupedCount
	if err := nodeQ.Select("kind, count(*) as count").Group("kind").Scan(&nodeKinds).Error; err != nil {
		return result, trace.Wrap(err, "group nodes by kind")
	}
	for _, row := range nodeKinds {
		result.NodesByKind[row.Kind] = row.Count
		result.NodeKinds = append(result.NodeKinds, analyze.KindCount{Kind: row.Kind, Count: row.Count})
	}
	var nodeLanguages []groupedCount
	if err := nodeQ.Select("language as kind, count(*) as count").Where("language != ''").Group("language").Scan(&nodeLanguages).Error; err != nil {
		return result, trace.Wrap(err, "group nodes by language")
	}
	for _, row := range nodeLanguages {
		result.NodesByLanguage[row.Kind] = row.Count
	}
	var edgeKinds []groupedCount
	if err := edgeQ.Select("kind, count(*) as count").Group("kind").Scan(&edgeKinds).Error; err != nil {
		return result, trace.Wrap(err, "group edges by kind")
	}
	for _, row := range edgeKinds {
		result.EdgesByKind[row.Kind] = row.Count
		result.EdgeKinds = append(result.EdgeKinds, analyze.KindCount{Kind: row.Kind, Count: row.Count})
	}
	result.StrictCalls = result.EdgesByKind[string(graph.EdgeKindCalls)]
	result.FallbackCalls = result.EdgesByKind[string(graph.EdgeKindFallbackCalls)]
	return result, nil
}
