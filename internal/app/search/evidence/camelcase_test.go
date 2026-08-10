package evidence

import (
	"slices"
	"testing"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// Full-text search retrieves GetUser for the query getUserId, because the index
// holds the identifier split into get and user. The cut used to drop it again:
// it read the query as one token, and one token nine runes long cannot be a
// subsequence of a seven-rune name. The searcher was handed the answer and
// told there was none.
func TestBuild_KeepsTheNodeACamelCaseQueryNames(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Name: "GetUser", QualifiedName: "user.Service.GetUser", FilePath: "internal/user/service.go"},
	}

	got := Build("getUserId", nodes, Options{Limit: 10})

	if want := []uint{1}; !slices.Equal(ids(got), want) {
		t.Fatalf("kept %v, want %v (WeakFiltered=%d, Note=%q)", ids(got), want, got.WeakFiltered, got.Note)
	}
	if hits := got.Hits(); !slices.Contains(hits[0].Matched, MatchName) {
		t.Errorf("Matched = %v, want it to name the identifier match", hits[0].Matched)
	}
}
