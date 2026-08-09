package rank

import (
	"testing"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func TestSignals_ReportTheScoresTheRankerOrdersBy(t *testing.T) {
	node := graph.Node{
		Name:          "Rerank",
		QualifiedName: "rank.Rerank",
		FilePath:      "internal/app/search/rank/rank.go",
	}

	tests := []struct {
		name             string
		query            string
		wantName         bool
		wantPath         bool
		hasAnyStructural bool
	}{
		// The path signal wants a whole path segment, so the identifier scores on
		// name alone: "rerank" is not one of internal, app, search, rank, rank.go.
		{name: "the identifier itself", query: "Rerank", wantName: true, hasAnyStructural: true},
		{name: "a directory the file sits in", query: "search", wantPath: true, hasAnyStructural: true},
		{name: "both signals at once", query: "rank", wantName: true, wantPath: true, hasAnyStructural: true},
		{name: "neither name nor path", query: "webhook", hasAnyStructural: false},
		{name: "an empty query", query: "   ", hasAnyStructural: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Signals(tt.query, node)
			if (got.Name > 0) != tt.wantName {
				t.Errorf("Name = %v, want positive=%v", got.Name, tt.wantName)
			}
			if (got.Path > 0) != tt.wantPath {
				t.Errorf("Path = %v, want positive=%v", got.Path, tt.wantPath)
			}
			if got.Any() != tt.hasAnyStructural {
				t.Errorf("Any() = %v, want %v", got.Any(), tt.hasAnyStructural)
			}
		})
	}
}

// Signals has to agree with the ordering Rerank produces, or a result could be
// labelled as having no evidence while the ranker put it first for having some.
func TestSignals_AgreeWithRerankOrdering(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Name: "unrelated", QualifiedName: "other.unrelated", FilePath: "internal/other/other.go"},
		{ID: 2, Name: "Rerank", QualifiedName: "rank.Rerank", FilePath: "internal/app/search/rank/rank.go"},
	}
	ranked := Rerank("rerank", nodes, 10)
	if ranked[0].ID != 2 {
		t.Fatalf("expected the name match first, got id %d", ranked[0].ID)
	}
	if !Signals("rerank", nodes[1]).Any() {
		t.Error("the node the ranker put first reports no evidence")
	}
	if Signals("rerank", nodes[0]).Any() {
		t.Error("the node the ranker put last reports evidence")
	}
}
