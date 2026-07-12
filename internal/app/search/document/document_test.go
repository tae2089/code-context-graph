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

func containsToken(content, token string) bool {
	for _, field := range strings.Fields(content) {
		if field == token {
			return true
		}
	}
	return false
}
