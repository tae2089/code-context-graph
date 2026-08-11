package rank_test

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// The intent fixture freezes retrieval input, not scorer output. Replaying it
// must therefore apply the current intent scorer and ignore capture order.
func TestFixtureSearcher_QueryIntentRanksCapturedDocuments(t *testing.T) {
	searcher := fixtureSearcher{intent: goldenIntentFixture{
		Corpus: 40,
		Nodes: map[uint]goldenIntentNode{
			1: {Name: "Sync", QualifiedName: "repo.Sync", Kind: "function", FilePath: "repo/sync.go"},
			2: {Name: "Quarantine", QualifiedName: "repo.Quarantine", Kind: "function", FilePath: "repo/quarantine.go", Intent: "quarantine a repository whose sync keeps failing"},
		},
		Documents: map[uint]goldenIntentDocument{
			10: {NodeID: 1, Content: "sync something else"},
			20: {NodeID: 2, Content: "quarantine a repository whose sync keeps failing"},
		},
		Queries: map[string][]uint{"why quarantine a sync": {10, 20}},
	}}

	got, err := searcher.QueryIntent(context.Background(), "why quarantine a sync", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(got.Hits))
	}
	if ids := []uint{got.Hits[0].Node.ID, got.Hits[1].Node.ID}; !slices.Equal(ids, []uint{2, 1}) {
		t.Fatalf("got node order %v, want scorer order [2 1]", ids)
	}
	if !slices.Equal(got.Hits[0].Terms, []string{"quarantine", "sync"}) {
		t.Errorf("first hit terms = %v, want the scorer's matched terms", got.Hits[0].Terms)
	}
}

func TestValidateGoldenIntentFixtureRejectsBrokenReferences(t *testing.T) {
	valid := func() goldenIntentFixture {
		return goldenIntentFixture{
			Nodes:     map[uint]goldenIntentNode{1: {Name: "One"}},
			Documents: map[uint]goldenIntentDocument{10: {NodeID: 1, Content: "one"}},
			Queries:   map[string][]uint{"query": {10}},
		}
	}
	tests := []struct {
		name string
		edit func(*goldenIntentFixture)
		want string
	}{
		{name: "unsorted", edit: func(f *goldenIntentFixture) {
			f.Documents[20] = goldenIntentDocument{NodeID: 1, Content: "two"}
			f.Queries["query"] = []uint{20, 10}
		}, want: "canonical"},
		{name: "duplicate", edit: func(f *goldenIntentFixture) {
			f.Queries["query"] = []uint{10, 10}
		}, want: "repeat"},
		{name: "dangling document", edit: func(f *goldenIntentFixture) {
			f.Queries["query"] = []uint{99}
		}, want: "missing document"},
		{name: "dangling node", edit: func(f *goldenIntentFixture) {
			f.Documents[10] = goldenIntentDocument{NodeID: 99, Content: "one"}
		}, want: "missing node"},
		{name: "unreachable document", edit: func(f *goldenIntentFixture) {
			f.Documents[20] = goldenIntentDocument{NodeID: 1, Content: "two"}
		}, want: "unreachable"},
		{name: "unreachable node", edit: func(f *goldenIntentFixture) {
			f.Nodes[2] = goldenIntentNode{Name: "Two"}
		}, want: "unreachable"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fixture := valid()
			tc.edit(&fixture)
			if err := validateGoldenIntentFixture(fixture); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestValidateGoldenPoolIdentitiesRejectsNodeIDCollisions(t *testing.T) {
	tests := []struct {
		name   string
		named  map[string][]goldenCandidate
		intent goldenIntentFixture
	}{
		{
			name: "named candidates disagree",
			named: map[string][]goldenCandidate{
				"first":  {{ID: 1, QualifiedName: "repo.First", Kind: "function", FilePath: "first.go"}},
				"second": {{ID: 1, QualifiedName: "repo.Second", Kind: "function", FilePath: "second.go"}},
			},
		},
		{
			name:  "named and intent disagree",
			named: map[string][]goldenCandidate{"query": {{ID: 1, QualifiedName: "repo.Named", Kind: "function", FilePath: "named.go"}}},
			intent: goldenIntentFixture{Nodes: map[uint]goldenIntentNode{
				1: {QualifiedName: "repo.Intent", Kind: "function", FilePath: "intent.go"},
			}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateGoldenPoolIdentities(tc.named, tc.intent); err == nil {
				t.Fatal("expected a shared node ID identity collision")
			}
		})
	}
}
