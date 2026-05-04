package search

import (
	"testing"

	"github.com/tae2089/code-context-graph/internal/model"
)

func TestExtractExactNameToken(t *testing.T) {
	tests := []struct {
		query string
		want  string
	}{
		{query: "GetUser", want: "getuser"},
		{query: "  GetUser  ", want: "getuser"},
		{query: "GetUser Kotlin", want: ""},
		{query: "get_user", want: "get_user"},
		{query: "UserService:getUser", want: ""},
		{query: "", want: ""},
		{query: "!!!", want: ""},
	}

	for _, tt := range tests {
		if got := extractExactNameToken(tt.query); got != tt.want {
			t.Fatalf("extractExactNameToken(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
}

func TestPromoteExactNameMatch_DoesNotPromoteMultiTokenQuery(t *testing.T) {
	nodes := []model.Node{
		{Name: "getUser", QualifiedName: "cpp.UserService.getUser"},
		{Name: "GetUser", QualifiedName: "go.UserService.GetUser"},
	}

	got := promoteExactNameMatch(nodes, "GetUser Kotlin")
	if got[0].QualifiedName != "cpp.UserService.getUser" {
		t.Fatalf("multi-token query should preserve original order, got %q first", got[0].QualifiedName)
	}
}

func TestPromoteExactNameMatch_DoesNotPromoteSubstringMatch(t *testing.T) {
	nodes := []model.Node{
		{Name: "UserService", QualifiedName: "pkg.UserService"},
		{Name: "User", QualifiedName: "pkg.User"},
	}

	got := promoteExactNameMatch(nodes, "Use")
	if got[0].QualifiedName != "pkg.UserService" {
		t.Fatalf("substring query should not promote any result, got %q first", got[0].QualifiedName)
	}
}

func TestPromoteExactNameMatch_PreservesStableOrderAmongNonMatches(t *testing.T) {
	nodes := []model.Node{
		{Name: "Alpha", QualifiedName: "pkg.Alpha"},
		{Name: "Beta", QualifiedName: "pkg.Beta"},
		{Name: "Gamma", QualifiedName: "pkg.Gamma"},
	}

	got := promoteExactNameMatch(nodes, "Delta")
	for i, want := range []string{"pkg.Alpha", "pkg.Beta", "pkg.Gamma"} {
		if got[i].QualifiedName != want {
			t.Fatalf("stable order mismatch at %d: got %q, want %q", i, got[i].QualifiedName, want)
		}
	}
}
