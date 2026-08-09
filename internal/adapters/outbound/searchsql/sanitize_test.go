package searchsql

import (
	"testing"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func TestSanitizeFTS5_SplitsCamelCaseTokens(t *testing.T) {
	tests := []struct {
		query string
		want  string
	}{
		{query: "", want: ""},
		{query: "user", want: `"user"*`},                                 // 단일 단어 불변
		{query: "get user", want: `"get"* "user"*`},                      // 소문자 멀티토큰 불변
		{query: "getUser", want: `("getuser"* OR ("get"* AND "user"*))`}, // camelCase 분할
		{query: "UserService", want: `("userservice"* OR ("user"* AND "service"*))`},
	}
	for _, tt := range tests {
		if got := SanitizeFTS5(tt.query); got != tt.want {
			t.Fatalf("SanitizeFTS5(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
}

func TestSanitizePostgresTSQuery_SplitsCamelCaseTokens(t *testing.T) {
	tests := []struct {
		query string
		want  string
	}{
		{query: "", want: ""},
		{query: "user", want: "user:*"},
		{query: "get user", want: "get:* & user:*"},
		{query: "getUser", want: "(getuser:* | (get:* & user:*))"},
	}
	for _, tt := range tests {
		if got := SanitizePostgresTSQuery(tt.query); got != tt.want {
			t.Fatalf("SanitizePostgresTSQuery(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
}

func TestSanitize_DropsFunctionWords(t *testing.T) {
	tests := []struct {
		name  string
		query string
		fts5  string
		pg    string
	}{
		{
			name:  "a question keeps only its content words",
			query: "what stops the server",
			fts5:  `"stops"* "server"*`,
			pg:    "stops:* & server:*",
		},
		{
			name:  "a query made only of function words keeps them",
			query: "how does the",
			fts5:  `"how"* "does"* "the"*`,
			pg:    "how:* & does:* & the:*",
		},
		{
			name:  "code words that read like function words survive",
			query: "get set list new",
			fts5:  `"get"* "set"* "list"* "new"*`,
			pg:    "get:* & set:* & list:* & new:*",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeFTS5(tt.query); got != tt.fts5 {
				t.Errorf("SanitizeFTS5(%q) = %q, want %q", tt.query, got, tt.fts5)
			}
			if got := SanitizePostgresTSQuery(tt.query); got != tt.pg {
				t.Errorf("SanitizePostgresTSQuery(%q) = %q, want %q", tt.query, got, tt.pg)
			}
		})
	}
}

// A short term is the one place prefix expansion turns against the intent index.
// "why does an invoice get a loyalty discount" matched three recorded reasons
// because `get*` reached the identifiers getAnnotation, getImpactRadius, and
// getAffectedFlows written inside the prose. The term is not common — `get*`
// reaches 3 of 1702 reasons — so no stopword list or frequency cut would have
// caught it. Only the prefix did.
//
// The rule stops at ASCII because scripts differ in where they put a word
// boundary. English puts a space there, so the indexed token already is the
// word and a prefix only adds inflections. Korean glues the particle on, so
// "네임스페이스가" is one token and a prefix is the only way a question asking
// about "네임스페이스" can reach it.
func TestSanitizeIntent_KeepsShortLatinTermsExact(t *testing.T) {
	tests := []struct {
		name  string
		query string
		fts5  string
		pg    string
	}{
		{
			name:  "three letters match exactly, four or more match by prefix",
			query: "lock get keeps",
			fts5:  `"lock"* OR "get" OR "keeps"*`,
			pg:    "lock:* | get | keeps:*",
		},
		{
			name:  "a camelCase sub-token follows the same rule as a typed term",
			query: "getUser",
			fts5:  `("getuser"* OR ("get" AND "user"*))`,
			pg:    "(getuser:* | (get & user:*))",
		},
		{
			name:  "a short Korean term still matches by prefix, because its particle is in the token",
			query: "락이 무엇",
			fts5:  `"락이"* OR "무엇"*`,
			pg:    "락이:* | 무엇:*",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeIntentFTS5(tt.query); got != tt.fts5 {
				t.Errorf("SanitizeIntentFTS5(%q) = %q, want %q", tt.query, got, tt.fts5)
			}
			if got := SanitizePostgresIntentTSQuery(tt.query); got != tt.pg {
				t.Errorf("SanitizePostgresIntentTSQuery(%q) = %q, want %q", tt.query, got, tt.pg)
			}
		})
	}
}

// The shared index keeps prefix expansion for every term. A caller there typed a
// symbol out of code it already read, so `get` reaching getAnnotation is the
// answer rather than the mistake.
func TestSanitizeSearch_KeepsShortTermsAsPrefixes(t *testing.T) {
	if got, want := SanitizeFTS5("lock get keeps"), `"lock"* "get"* "keeps"*`; got != want {
		t.Errorf("SanitizeFTS5 = %q, want %q", got, want)
	}
	if got, want := SanitizePostgresTSQuery("lock get keeps"), "lock:* & get:* & keeps:*"; got != want {
		t.Errorf("SanitizePostgresTSQuery = %q, want %q", got, want)
	}
}

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
	nodes := []graph.Node{
		{Name: "getUser", QualifiedName: "cpp.UserService.getUser"},
		{Name: "GetUser", QualifiedName: "go.UserService.GetUser"},
	}

	got := promoteExactNameMatch(nodes, "GetUser Kotlin")
	if got[0].QualifiedName != "cpp.UserService.getUser" {
		t.Fatalf("multi-token query should preserve original order, got %q first", got[0].QualifiedName)
	}
}

func TestPromoteExactNameMatch_DoesNotPromoteSubstringMatch(t *testing.T) {
	nodes := []graph.Node{
		{Name: "UserService", QualifiedName: "pkg.UserService"},
		{Name: "User", QualifiedName: "pkg.User"},
	}

	got := promoteExactNameMatch(nodes, "Use")
	if got[0].QualifiedName != "pkg.UserService" {
		t.Fatalf("substring query should not promote any result, got %q first", got[0].QualifiedName)
	}
}

func TestPromoteExactNameMatch_PreservesStableOrderAmongNonMatches(t *testing.T) {
	nodes := []graph.Node{
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
