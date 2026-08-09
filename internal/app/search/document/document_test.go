package document

import (
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
func TestBuildIntentContent_KeepsOnlyTheRecordedReason(t *testing.T) {
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

	content := BuildIntentContent(node, annotations)

	for _, want := range []string{"keep self-hosted server flags separate", "a server build never reads the local config file"} {
		if !strings.Contains(content, want) {
			t.Errorf("intent content %q is missing the recorded reason %q", content, want)
		}
	}
	// Every one of these is why the shared index cannot answer an intent question.
	for _, unwanted := range []string{"newRootCmd", "main.newRootCmd", "function", "main.go", "opens the DB", "server options"} {
		if strings.Contains(content, unwanted) {
			t.Errorf("intent content %q carries %q, which belongs to the name index", content, unwanted)
		}
	}
}

// A node with nothing recorded must produce nothing, not an empty-ish row. The
// tool promises to say "no reason was written down here" rather than guess, and
// it can only keep that promise if these nodes stay out of the index.
func TestBuildIntentContent_EmptyWhenNoReasonWasRecorded(t *testing.T) {
	node := graph.Node{ID: 3, Name: "helper", QualifiedName: "pkg.helper", Kind: graph.NodeKindFunction, FilePath: "pkg/helper.go"}
	cases := map[string]map[uint]*graph.Annotation{
		"no annotation at all": nil,
		"annotation without intent or domain rule": {
			3: {NodeID: 3, Summary: "helper does things.", Tags: []graph.DocTag{
				{Kind: graph.TagSideEffect, Value: "writes a file"},
			}},
		},
	}
	for name, annotations := range cases {
		t.Run(name, func(t *testing.T) {
			if content := BuildIntentContent(node, annotations); content != "" {
				t.Errorf("expected no intent content, got %q", content)
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
