package document

import (
	"slices"
	"strings"
	"testing"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func TestBuildSearchDocuments_IndexesFileBaseAndLanguageTokens(t *testing.T) {
	tests := []struct {
		name     string
		node     graph.Node
		contains []string
	}{
		{name: "java file includes base and language", node: graph.Node{Name: "UserService", QualifiedName: "UserService", Kind: graph.NodeKindClass, FilePath: "java/Sample.java", Language: "java"}, contains: []string{"userservice", "sample", "java"}},
		{name: "rust file includes alias", node: graph.Node{Name: "get_user", QualifiedName: "get_user", Kind: graph.NodeKindFunction, FilePath: "rust/sample.rs", Language: "rust"}, contains: []string{"get_user", "sample", "rs", "rust"}},
		{name: "javascript file includes alias", node: graph.Node{Name: "getUser", QualifiedName: "UserService.getUser", Kind: graph.NodeKindFunction, FilePath: "javascript/sample.js", Language: "javascript"}, contains: []string{"getuser", "sample", "js", "javascript"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := BuildContent(tt.node, nil)
			for _, want := range tt.contains {
				if !strings.Contains(strings.ToLower(content), want) {
					t.Fatalf("content %q missing token %q", content, want)
				}
			}
		})
	}
}

func TestBuildSearchContent_EmitsIdentifierSubtokens(t *testing.T) {
	tests := []struct {
		name     string
		node     graph.Node
		contains []string
	}{
		{name: "camelCase name and qualified name split", node: graph.Node{Name: "getUserById", QualifiedName: "svc.getUserById", Kind: graph.NodeKindFunction, FilePath: "svc/user.go", Language: "go"}, contains: []string{"getuserbyid", "get", "user", "by", "id"}},
		{name: "PascalCase class split", node: graph.Node{Name: "UserService", QualifiedName: "pkg.UserService", Kind: graph.NodeKindClass, FilePath: "pkg/svc.go", Language: "go"}, contains: []string{"userservice", "user", "service"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := strings.ToLower(BuildContent(tt.node, nil))
			for _, want := range tt.contains {
				if !containsToken(content, want) {
					t.Fatalf("content %q missing subtoken %q", content, want)
				}
			}
		})
	}
}

// The intent index exists to answer "why was this built", asked as a sentence.
// Mixing the identifier into that text is what made the existing index unable to
// answer it: `newRootCmd main.newRootCmd function new root cmd main main go` sits
// in the same bag as the sentence a person actually wrote, so a query word
// matching a name scores the same as one matching the reason.
func TestBuildReasons_KeepsOnlyTheRecordedReasons(t *testing.T) {
	node := graph.Node{
		ID:            7,
		Name:          "newRootCmd",
		QualifiedName: "main.newRootCmd",
		Kind:          graph.NodeKindFunction,
		FilePath:      "cmd/server/main.go",
		Language:      "go",
	}
	annotations := map[uint]*graph.Annotation{
		7: {
			NodeID:  7,
			Summary: "newRootCmd builds the ccg-server command.",
			Tags: []graph.DocTag{
				{Kind: graph.TagIntent, Value: "keep self-hosted server flags separate from the local CLI"},
				{Kind: graph.TagDomainRule, Value: "a server build never reads the local config file"},
				{Kind: graph.TagSideEffect, Value: "opens the DB and starts a long-running HTTP server"},
				{Kind: graph.TagParam, Name: "opts", Value: "server options"},
			},
		},
	}

	reasons := BuildReasons(node, annotations)

	want := []string{
		"keep self-hosted server flags separate from the local CLI",
		"a server build never reads the local config file",
	}
	if !slices.Equal(reasons, want) {
		t.Errorf("reasons are %q, want %q", reasons, want)
	}
	// Every one of these is why the shared index cannot answer an intent question.
	joined := strings.Join(reasons, " ")
	for _, unwanted := range []string{"newRootCmd", "main.newRootCmd", "function", "main.go", "opens the DB", "server options"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("the reasons carry %q, which belongs to the name index", unwanted)
		}
	}
}

// The whole point of the split: a reason is a document, so three domain rules
// are three documents and not one long one. Joined, the intent that answers a
// question would be scored on the length of two rules that answer nothing.
func TestBuildReasons_GivesEveryReasonTagItsOwnDocument(t *testing.T) {
	node := graph.Node{ID: 9, Name: "deliver", QualifiedName: "webhook.deliver", Kind: graph.NodeKindFunction, FilePath: "webhook/deliver.go"}
	annotations := map[uint]*graph.Annotation{
		9: {NodeID: 9, Tags: []graph.DocTag{
			{Kind: graph.TagIntent, Value: "verify the signature so a push from anywhere else is rejected", Ordinal: 0},
			{Kind: graph.TagDomainRule, Value: "a rejected delivery is retried at most five times", Ordinal: 1},
			{Kind: graph.TagDomainRule, Value: "the retry delay doubles each attempt", Ordinal: 2},
			{Kind: graph.TagDomainRule, Value: "a dropped delivery is written to the audit log", Ordinal: 3},
		}},
	}

	reasons := BuildReasons(node, annotations)

	want := []string{
		"verify the signature so a push from anywhere else is rejected",
		"a rejected delivery is retried at most five times",
		"the retry delay doubles each attempt",
		"a dropped delivery is written to the audit log",
	}
	if !slices.Equal(reasons, want) {
		t.Errorf("reasons are %q, want one document per reason tag: %q", reasons, want)
	}
}

// A declaration states one purpose, so a second @intent is a writing mistake
// rather than a list, and graph.Node.Intent has always shown the first one.
// Indexing both would make the second one findable and then impossible to
// display, which is a search result whose stated reason does not contain the
// words that found it.
func TestBuildReasons_IndexesTheSameIntentTheReaderIsShown(t *testing.T) {
	node := graph.Node{ID: 4, Name: "Build", QualifiedName: "pkg.Build", Kind: graph.NodeKindFunction, FilePath: "pkg/build.go"}
	node.Annotation = &graph.Annotation{NodeID: 4, Tags: []graph.DocTag{
		{Kind: graph.TagIntent, Value: "perform a full graph build", Ordinal: 0},
		{Kind: graph.TagIntent, Value: "also refresh the search index", Ordinal: 1},
	}}
	annotations := map[uint]*graph.Annotation{4: node.Annotation}

	reasons := BuildReasons(node, annotations)

	if !slices.Equal(reasons, []string{node.Intent()}) {
		t.Errorf("indexed %q, want exactly the one reason the reader is shown: %q", reasons, node.Intent())
	}
}

// A node with nothing recorded must produce nothing, not an empty-ish row. The
// tool promises to say "no reason was written down here" rather than guess, and
// it can only keep that promise if these nodes stay out of the index.
func TestBuildReasons_EmptyWhenNoReasonWasRecorded(t *testing.T) {
	node := graph.Node{ID: 3, Name: "helper", QualifiedName: "pkg.helper", Kind: graph.NodeKindFunction, FilePath: "pkg/helper.go"}
	cases := map[string]map[uint]*graph.Annotation{
		"no annotation at all": nil,
		"annotation without intent or domain rule": {
			3: {NodeID: 3, Summary: "helper does things.", Tags: []graph.DocTag{
				{Kind: graph.TagSideEffect, Value: "writes a file"},
			}},
		},
		"a reason tag holding only whitespace": {
			3: {NodeID: 3, Tags: []graph.DocTag{{Kind: graph.TagIntent, Value: "   "}}},
		},
	}
	for name, annotations := range cases {
		t.Run(name, func(t *testing.T) {
			if reasons := BuildReasons(node, annotations); len(reasons) != 0 {
				t.Errorf("expected no reasons, got %q", reasons)
			}
		})
	}
}

func containsToken(content, token string) bool {
	for _, field := range strings.Fields(content) {
		if field == token {
			return true
		}
	}
	return false
}
