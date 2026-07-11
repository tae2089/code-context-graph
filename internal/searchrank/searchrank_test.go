package searchrank

import (
	"testing"

	"github.com/tae2089/code-context-graph/internal/model"
)

// 경계: 빈 쿼리 또는 빈 노드 슬라이스는 입력을 그대로 돌려준다.
func TestRerank_EmptyInputsReturnedUnchanged(t *testing.T) {
	nodes := []model.Node{
		{ID: 1, Name: "getUserById", QualifiedName: "svc.getUserById", FilePath: "svc/user.go"},
		{ID: 2, Name: "deleteUser", QualifiedName: "svc.deleteUser", FilePath: "svc/user.go"},
	}

	t.Run("empty query preserves order", func(t *testing.T) {
		got := Rerank("", nodes, 10)
		assertNodeIDOrder(t, got, []uint{1, 2})
	})

	t.Run("nil nodes returns empty", func(t *testing.T) {
		got := Rerank("user", nil, 10)
		if len(got) != 0 {
			t.Fatalf("expected empty result, got %d nodes", len(got))
		}
	})
}

// 오타 쿼리라도 이름 fuzzy 신호가 정확한 심볼을 FTS 하위에서 상위로 끌어올린다.
func TestRerank_TypoPromotesFuzzyNameMatch(t *testing.T) {
	nodes := []model.Node{
		{ID: 1, Name: "handleRequest", QualifiedName: "http.handleRequest", FilePath: "http/handler.go"},
		{ID: 2, Name: "getUserById", QualifiedName: "svc.getUserById", FilePath: "svc/user.go"},
	}
	got := Rerank("getUsrById", nodes, 10)
	if got[0].ID != 2 {
		t.Fatalf("expected fuzzy-name match (id=2) promoted to top, got id=%d", got[0].ID)
	}
}

// 경로 세그먼트와 겹치는 쿼리 토큰이 해당 노드를 부스트한다.
func TestRerank_PathProximityBoost(t *testing.T) {
	nodes := []model.Node{
		{ID: 1, Name: "helper", QualifiedName: "util.helper", FilePath: "util/helper.go"},
		{ID: 2, Name: "handler", QualifiedName: "svc.handler", FilePath: "svc/auth/login.go"},
	}
	got := Rerank("auth login", nodes, 10)
	if got[0].ID != 2 {
		t.Fatalf("expected path-matching node (id=2) promoted, got id=%d", got[0].ID)
	}
}

// FTS 1위가 구조 신호 0이어도 RRF의 FTS 항 덕분에 완전히 탈락하지 않는다.
func TestRerank_TopFTSHitSurvivesZeroStructSignal(t *testing.T) {
	nodes := []model.Node{
		{ID: 1, Name: "alpha", QualifiedName: "pkg.alpha", FilePath: "pkg/alpha.go"},
		{ID: 2, Name: "getUserById", QualifiedName: "svc.getUserById", FilePath: "svc/user.go"},
		{ID: 3, Name: "beta", QualifiedName: "pkg.beta", FilePath: "pkg/beta.go"},
	}
	got := Rerank("getUserById", nodes, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 results after limit, got %d", len(got))
	}
	assertNodeIDOrder(t, got, []uint{2, 1})
}

// 구조 신호가 전부 동점이면 원래 FTS 순서를 보존한다(stable).
func TestRerank_TiePreservesFTSOrder(t *testing.T) {
	nodes := []model.Node{
		{ID: 1, Name: "same", QualifiedName: "pkg.same", FilePath: "pkg/same.go"},
		{ID: 2, Name: "same", QualifiedName: "pkg.same", FilePath: "pkg/same.go"},
		{ID: 3, Name: "same", QualifiedName: "pkg.same", FilePath: "pkg/same.go"},
	}
	got := Rerank("anything", nodes, 10)
	assertNodeIDOrder(t, got, []uint{1, 2, 3})
}

// limit은 리랭크 후에 적용된다: 승격된 노드가 잘려나가면 안 된다.
func TestRerank_LimitAppliedAfterRerank(t *testing.T) {
	nodes := []model.Node{
		{ID: 1, Name: "handleRequest", QualifiedName: "http.handleRequest", FilePath: "http/handler.go"},
		{ID: 2, Name: "getUserById", QualifiedName: "svc.getUserById", FilePath: "svc/user.go"},
	}
	got := Rerank("getUsrById", nodes, 1)
	assertNodeIDOrder(t, got, []uint{2})
}

// 공백으로 나뉜 멀티토큰 쿼리도 심볼 서브토큰과 매칭돼 승격된다.
func TestRerank_MultiTokenNameMatch(t *testing.T) {
	nodes := []model.Node{
		{ID: 1, Name: "handleRequest", QualifiedName: "http.handleRequest", FilePath: "http/handler.go"},
		{ID: 2, Name: "getUserById", QualifiedName: "svc.getUserById", FilePath: "svc/user.go"},
	}
	got := Rerank("get user by id", nodes, 10)
	if got[0].ID != 2 {
		t.Fatalf("expected multi-token match (id=2) promoted, got id=%d", got[0].ID)
	}
}

// 이름 안의 서브토큰(camelCase 조각)과 일치하는 짧은 토큰도 강한 신호가 된다.
func TestRerank_SubtokenMatch(t *testing.T) {
	nodes := []model.Node{
		{ID: 1, Name: "handleRequest", QualifiedName: "http.handleRequest", FilePath: "http/handler.go"},
		{ID: 2, Name: "getUserById", QualifiedName: "svc.getUserById", FilePath: "svc/user.go"},
	}
	got := Rerank("user", nodes, 10)
	if got[0].ID != 2 {
		t.Fatalf("expected subtoken 'user' match (id=2) promoted, got id=%d", got[0].ID)
	}
}

// 쿼리 토큰이 경로에도 있어 path 신호가 포화될 때, 이름이 일치하는 노드가
// 단지 같은 파일에 있는 노드보다 위로 올라와야 한다(name > path).
func TestRerank_NameOutranksSaturatedPath(t *testing.T) {
	nodes := []model.Node{
		{ID: 1, Name: "isAlnum", QualifiedName: "mcp.isAlnum", FilePath: "mcp/search_rerank.go"},
		{ID: 2, Name: "rerankSearch", QualifiedName: "mcp.rerankSearch", FilePath: "mcp/search_rerank.go"},
	}
	got := Rerank("rerank", nodes, 10)
	if got[0].ID != 2 {
		t.Fatalf("expected name match (id=2) above same-file node, got id=%d", got[0].ID)
	}
}

func TestFetchLimit(t *testing.T) {
	if got := FetchLimit(10); got <= 10 {
		t.Fatalf("expected fetch limit wider than 10, got %d", got)
	}
	if got := FetchLimit(200); got != fetchCap {
		t.Fatalf("expected fetch limit capped at %d, got %d", fetchCap, got)
	}
}

func TestSplitIdentifier(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"getUserById", []string{"get", "user", "by", "id"}},
		{"HTTPServer", []string{"http", "server"}},
		{"user_id", []string{"user", "id"}},
		{"parseHTML5", []string{"parse", "html", "5"}},
	}
	for _, c := range cases {
		got := splitIdentifier(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitIdentifier(%q)=%v, want %v", c.in, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("splitIdentifier(%q)=%v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "abd", 1},
		{"abc", "abxc", 1},
		{"abc", "ac", 1},
		{"kitten", "sitting", 3},
	}
	for _, c := range cases {
		if got := levenshtein([]rune(c.a), []rune(c.b)); got != c.want {
			t.Errorf("levenshtein(%q,%q)=%d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func assertNodeIDOrder(t *testing.T, got []model.Node, wantIDs []uint) {
	t.Helper()
	if len(got) != len(wantIDs) {
		t.Fatalf("length mismatch: got %d, want %d", len(got), len(wantIDs))
	}
	for i, id := range wantIDs {
		if got[i].ID != id {
			gotIDs := make([]uint, len(got))
			for j, n := range got {
				gotIDs[j] = n.ID
			}
			t.Fatalf("order mismatch: got %v, want %v", gotIDs, wantIDs)
		}
	}
}
