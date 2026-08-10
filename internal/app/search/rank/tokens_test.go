package rank

import (
	"slices"
	"strings"
	"testing"

	"github.com/tae2089/code-context-graph/internal/app/search/identtoken"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// The index, the full-text query and the evidence cut all cut an identifier with
// identtoken.Split. Scoring is the fourth reader of the same text, so it has to
// cut it the same way — a scorer with its own rule judges a document the index
// matched for reasons the scorer cannot see.
func TestQueryTokens_PartsAreCutTheWayIdentifiersAre(t *testing.T) {
	queries := []string{
		"getUserId", "HTTPServer", "user_id", "parseHTML5",
		"search document", "getUserById handler", "SanitizeFTS5",
		"네임스페이스 설정이", "",
	}
	for _, query := range queries {
		got := newQueryTokens(query).parts
		if want := identtoken.Split(query); !slices.Equal(got, want) {
			t.Errorf("newQueryTokens(%q).parts = %v, want identtoken.Split's %v", query, got, want)
		}
	}
}

// Cutting camelCase is the change; cutting underscores is what was already
// there. tokenize reads user_id as two tokens because a path segment spelled
// user_id is two segments, and keeping the underscore whole would silently
// unmatch every path that holds one.
func TestQueryTokens_WholeTokensSplitOnUnderscore(t *testing.T) {
	cases := []struct {
		query string
		want  []string
	}{
		{"user_id", []string{"user", "id"}},
		{"sync_queue handler", []string{"sync", "queue", "handler"}},
		{"getUserId", []string{"getuserid"}},
		{"", nil},
	}
	for _, c := range cases {
		if got := newQueryTokens(c.query).whole; !slices.Equal(got, c.want) {
			t.Errorf("newQueryTokens(%q).whole = %v, want %v", c.query, got, c.want)
		}
	}
}

// A path segment match is an exact one, so it is scored against the token the
// searcher typed rather than its pieces: internal/syncqueue/ holds the segment
// syncqueue, and neither sync nor queue is spelled anywhere in it.
func TestPathScore_MatchesAWholeSegmentTheQueryPartsSplitApart(t *testing.T) {
	node := graph.Node{Name: "push", QualifiedName: "syncqueue.Queue.push", FilePath: "internal/syncqueue/queue.go"}

	if got := pathScore(newQueryTokens("syncQueue"), node); got != 1.0 {
		t.Errorf("pathScore(%q, %q) = %.4f, want 1.0 — the whole token is the segment", "syncQueue", node.FilePath, got)
	}
}

// The underscore path, from the same angle: the query and the path agree because
// both are cut on the underscore.
func TestPathScore_UnderscoreSegmentsStillMatch(t *testing.T) {
	node := graph.Node{Name: "push", QualifiedName: "reposync.SyncQueue.push", FilePath: "internal/reposync/sync_queue.go"}

	if got := pathScore(newQueryTokens("sync_queue"), node); got != 1.0 {
		t.Errorf("pathScore(%q, %q) = %.4f, want 1.0", "sync_queue", node.FilePath, got)
	}
}

// scoreTargets also scores the parts run back together with nothing between
// them, which is how a query reaches the identifier that spells it without
// separators. That reading only survives while the parts rejoin to the query:
// listing a token beside its own parts would rejoin to that token written twice,
// and nothing matches that.
func TestQueryTokens_PartsRejoinToTheQueryWithoutSeparators(t *testing.T) {
	cases := []struct{ query, want string }{
		{"getUserId", "getuserid"},
		{"search document", "searchdocument"},
		{"user_id", "userid"},
		{"HTTPServer", "httpserver"},
		{"SanitizeFTS5", "sanitizefts5"},
	}
	for _, c := range cases {
		if got := strings.Join(newQueryTokens(c.query).parts, ""); got != c.want {
			t.Errorf("the parts of %q rejoin to %q, want %q", c.query, got, c.want)
		}
	}
}

// The same rule as a score: the run-together name earns the joined reading, not
// the weaker average of the parts taken one at a time.
func TestNameSim_JoinedPartsStillReachTheRunTogetherName(t *testing.T) {
	node := graph.Node{Name: "getuserid"}

	want := subsequenceScore("getuserid", node.Name)
	if got := nameSim(newQueryTokens("getUserId"), node); got != want {
		t.Errorf("nameSim(\"getUserId\", %q) = %.4f, want the joined reading's %.4f", node.Name, got, want)
	}
}

// A camelCase query longer than the identifier it names cannot match as one
// token: the query has more runes than the name, so the subsequence scorer
// gives up before comparing anything. Full-text search still retrieves the
// node, because indexing splits GetUser into get and user, so the scorer is
// the only thing that cannot see the match.
func TestSignals_CamelCaseQueryLeavesNameEvidence(t *testing.T) {
	node := graph.Node{Name: "GetUser", QualifiedName: "user.Service.GetUser", FilePath: "internal/user/service.go"}

	got := Signals("getUserId", node)
	if !got.Any() {
		t.Fatalf("Signals(\"getUserId\", GetUser) left no evidence (name=%.4f path=%.4f), want the query's sub-tokens to justify the node", got.Name, got.Path)
	}
}

// The order has to follow the evidence, not just the membership: a node whose
// name holds the query's sub-tokens outranks one that holds none of them.
func TestRerank_CamelCaseQueryPutsTheIdentifierItNamesFirst(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Name: "marshalJSON", QualifiedName: "codec.marshalJSON", FilePath: "internal/codec/json.go"},
		{ID: 2, Name: "GetUser", QualifiedName: "user.Service.GetUser", FilePath: "internal/user/service.go"},
	}

	got := Rerank("getUserId", nodes, 10)
	if got[0].ID != 2 {
		t.Fatalf("query \"getUserId\" ranked id=%d first, want the sub-token match id=2 (nameSim GetUser=%.4f, marshalJSON=%.4f)",
			got[0].ID, Signals("getUserId", nodes[1]).Name, Signals("getUserId", nodes[0]).Name)
	}
}
