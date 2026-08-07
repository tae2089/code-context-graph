package rank

import (
	"testing"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// 경계: 빈 쿼리 또는 빈 노드 슬라이스는 입력을 그대로 돌려준다.
func TestRerank_EmptyInputsReturnedUnchanged(t *testing.T) {
	nodes := []graph.Node{
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
	nodes := []graph.Node{
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
	nodes := []graph.Node{
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
	nodes := []graph.Node{
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
	nodes := []graph.Node{
		{ID: 1, Name: "same", QualifiedName: "pkg.same", FilePath: "pkg/same.go"},
		{ID: 2, Name: "same", QualifiedName: "pkg.same", FilePath: "pkg/same.go"},
		{ID: 3, Name: "same", QualifiedName: "pkg.same", FilePath: "pkg/same.go"},
	}
	got := Rerank("anything", nodes, 10)
	assertNodeIDOrder(t, got, []uint{1, 2, 3})
}

// limit은 리랭크 후에 적용된다: 승격된 노드가 잘려나가면 안 된다.
func TestRerank_LimitAppliedAfterRerank(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Name: "handleRequest", QualifiedName: "http.handleRequest", FilePath: "http/handler.go"},
		{ID: 2, Name: "getUserById", QualifiedName: "svc.getUserById", FilePath: "svc/user.go"},
	}
	got := Rerank("getUsrById", nodes, 1)
	assertNodeIDOrder(t, got, []uint{2})
}

// 공백으로 나뉜 멀티토큰 쿼리도 심볼 서브토큰과 매칭돼 승격된다.
func TestRerank_MultiTokenNameMatch(t *testing.T) {
	nodes := []graph.Node{
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
	nodes := []graph.Node{
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
	nodes := []graph.Node{
		{ID: 1, Name: "isAlnum", QualifiedName: "mcp.isAlnum", FilePath: "mcp/search_rerank.go"},
		{ID: 2, Name: "rerankSearch", QualifiedName: "mcp.rerankSearch", FilePath: "mcp/search_rerank.go"},
	}
	got := Rerank("rerank", nodes, 10)
	if got[0].ID != 2 {
		t.Fatalf("expected name match (id=2) above same-file node, got id=%d", got[0].ID)
	}
}

// federated 검색은 namespace별 후보 목록을 이어붙인다. 이어붙인 배열의 위치를
// 검색 순위로 쓰면 뒤쪽 namespace의 1위가 앞쪽 namespace의 후보 개수만큼
// 밀린다. 두 번째 그룹의 정확한 이름 일치가 첫 그룹의 무관한 후보에게
// 져서는 안 된다.
func TestRerankGroups_LaterGroupTopHitNotPenalizedByPosition(t *testing.T) {
	filler := make([]graph.Node, 0, fetchFloor)
	for i := range fetchFloor {
		filler = append(filler, graph.Node{
			ID:            uint(i + 1),
			Namespace:     "alpha",
			Name:          "unrelated",
			QualifiedName: "alpha.unrelated",
			FilePath:      "alpha/misc.go",
		})
	}
	exact := graph.Node{
		ID:            9001,
		Namespace:     "beta",
		Name:          "getUserById",
		QualifiedName: "beta.getUserById",
		FilePath:      "beta/user.go",
	}

	got := RerankGroups("getUserById", [][]graph.Node{filler, {exact}}, 5)
	if len(got) == 0 || got[0].ID != exact.ID {
		t.Fatalf("expected exact match from the later group on top, got %v", nodeIDs(got))
	}
}

// 구조 점수가 확실히 갈리는 후보라면, 그룹을 넘겨준 순서가 결과 순위를 바꾸면
// 안 된다. alpha는 무관한 후보만 많고 beta에 정확 일치가 하나 있다.
func TestRerankGroups_GroupOrderDoesNotChangeRanking(t *testing.T) {
	alpha := make([]graph.Node, 0, fetchFloor)
	for i := range fetchFloor {
		alpha = append(alpha, graph.Node{
			ID:            uint(i + 1),
			Namespace:     "alpha",
			Name:          "unrelated",
			QualifiedName: "alpha.unrelated",
			FilePath:      "alpha/misc.go",
		})
	}
	beta := []graph.Node{
		{ID: 9001, Namespace: "beta", Name: "login", QualifiedName: "beta.login", FilePath: "beta/auth.go"},
		{ID: 9002, Namespace: "beta", Name: "somethingElse", QualifiedName: "beta.somethingElse", FilePath: "beta/misc.go"},
	}

	forward := nodeIDs(RerankGroups("login", [][]graph.Node{alpha, beta}, 0))
	reversed := nodeIDs(RerankGroups("login", [][]graph.Node{beta, alpha}, 0))

	if len(forward) != len(reversed) {
		t.Fatalf("length mismatch: %d vs %d", len(forward), len(reversed))
	}
	if forward[0] != reversed[0] {
		t.Fatalf("top hit depends on group order: forward=%v reversed=%v", forward[0], reversed[0])
	}
	if forward[0] != 9001 {
		t.Fatalf("expected the exact-name match (id=9001) on top, got %v", forward[0])
	}
}

// 그룹이 하나면 기존 Rerank와 결과가 같아야 한다.
func TestRerankGroups_SingleGroupMatchesRerank(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Name: "handleRequest", QualifiedName: "http.handleRequest", FilePath: "http/handler.go"},
		{ID: 2, Name: "getUserById", QualifiedName: "svc.getUserById", FilePath: "svc/user.go"},
		{ID: 3, Name: "deleteUser", QualifiedName: "svc.deleteUser", FilePath: "svc/user.go"},
	}
	want := nodeIDs(Rerank("getUserById", nodes, 0))
	got := nodeIDs(RerankGroups("getUserById", [][]graph.Node{nodes}, 0))
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("single-group result diverged: got %v, want %v", got, want)
		}
	}
}

// 빈 그룹이 섞여 있어도 나머지 그룹의 지역 순위는 그대로 유지된다.
func TestRerankGroups_EmptyGroupsIgnored(t *testing.T) {
	beta := []graph.Node{
		{ID: 3, Namespace: "beta", Name: "getUserById", QualifiedName: "beta.getUserById", FilePath: "beta/user.go"},
	}
	got := RerankGroups("getUserById", [][]graph.Node{nil, {}, beta}, 0)
	if len(got) != 1 || got[0].ID != 3 {
		t.Fatalf("expected the only non-empty group's node, got %v", nodeIDs(got))
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

func nodeIDs(nodes []graph.Node) []uint {
	ids := make([]uint, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	return ids
}

func assertNodeIDOrder(t *testing.T, got []graph.Node, wantIDs []uint) {
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
