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

// FTS 순위는 순서를 정하지 않는다. 정확히 일치하는 이름은 후보 목록 어디에
// 있든 1등이어야 한다. 후보 풀은 50개까지 커질 수 있는데(FetchLimit), 그 안에서
// FTS가 매긴 3등과 40등의 차이에는 이름 증거를 뒤집을 만한 근거가 없다.
func TestRerank_ExactNameMatchWinsFromDeepInTheFTSList(t *testing.T) {
	const buried = 40
	nodes := make([]graph.Node, 0, buried+1)
	for i := range buried {
		nodes = append(nodes, graph.Node{
			ID:            uint(i + 1),
			Name:          "unrelated",
			QualifiedName: "pkg.unrelated",
			FilePath:      "pkg/other.go",
		})
	}
	nodes = append(nodes, graph.Node{
		ID:            999,
		Name:          "getUserById",
		QualifiedName: "svc.getUserById",
		FilePath:      "svc/user.go",
	})

	got := Rerank("getUserById", nodes, 3)
	if got[0].ID != 999 {
		t.Fatalf("exact name match ranked %d, want first; got order %d", got[0].ID, got[0].ID)
	}
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

// 구조 신호가 0인 후보끼리는 FTS 순위가 순서를 정한다. 그래서 FTS 1위는
// 구조 신호가 없어도 같은 처지의 다른 후보보다는 앞에 남는다.
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

// 구조 점수가 완전히 같은 후보들은 구조 순위도 같아야 한다. 배열 위치로
// 순위를 나누면 뒤쪽 그룹이 동점인데도 앞쪽 그룹 전체 뒤로 밀린다.
func TestRerankGroups_TiedStructScoreDoesNotSinkLaterGroup(t *testing.T) {
	alpha := make([]graph.Node, 0, fetchFloor)
	for i := range fetchFloor {
		alpha = append(alpha, graph.Node{
			ID:            uint(i + 1),
			Namespace:     "alpha",
			Name:          "getUserById",
			QualifiedName: "alpha.getUserById",
			FilePath:      "alpha/user.go",
		})
	}
	// beta의 후보는 alpha 후보들과 구조 신호가 완전히 동일하고, 자기 목록에서 1위다.
	beta := []graph.Node{
		{ID: 9001, Namespace: "beta", Name: "getUserById", QualifiedName: "beta.getUserById", FilePath: "beta/user.go"},
	}

	got := nodeIDs(RerankGroups("getUserById", [][]graph.Node{alpha, beta}, 0))
	// 동점이므로 alpha의 1위와만 순서 경쟁하면 된다: beta는 2번째 자리여야 한다.
	if len(got) < 2 || got[1] != 9001 {
		t.Fatalf("tied later-group hit sank to position %d, want 1 (got %v...)", indexOf(got, 9001), got[:min(5, len(got))])
	}
}

func indexOf(ids []uint, want uint) int {
	for i, id := range ids {
		if id == want {
			return i
		}
	}
	return -1
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

// 서브토큰 일치가 만점으로 포화하면 이름 신호가 변별력을 잃는다. 같은 파일에
// 있어 path 신호가 동일할 때, 쿼리와 정확히 같은 이름이 그 쿼리를 조각으로만
// 품은 더 긴 이름보다 위에 와야 한다.
func TestRerank_ExactNameOutranksLongerNameContainingIt(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Name: "PaymentProcessHelper", QualifiedName: "billing.PaymentProcessHelper", FilePath: "billing/service.go"},
		{ID: 2, Name: "Payment", QualifiedName: "billing.Payment", FilePath: "billing/service.go"},
	}
	got := Rerank("payment", nodes, 10)
	assertNodeIDOrder(t, got, []uint{2, 1})
}

// 이름 신호는 쿼리가 이름의 얼마를 차지하느냐에 따라 단계적으로 낮아져야 한다.
// 정확 일치 > 짧게 감싼 이름 > 길게 감싼 이름 순으로 줄을 서야, 서브토큰을
// 가진 후보들 사이에서도 순위가 결정된다.
func TestNameSim_DecreasesAsNameGrowsAroundQuery(t *testing.T) {
	qTokens := tokenize("payment")
	exact := nameSim(qTokens, graph.Node{Name: "Payment"})
	short := nameSim(qTokens, graph.Node{Name: "paymentProcessor"})
	long := nameSim(qTokens, graph.Node{Name: "PaymentProcessHelper"})

	if !(exact > short && short > long) {
		t.Fatalf("expected exact > short > long, got exact=%.4f short=%.4f long=%.4f", exact, short, long)
	}
	// No absolute anchor is asserted. nameSim's scale is ordinal within a query;
	// the 1.0 an exact match used to hit came from Jaro-Winkler, not from the
	// subsequence scorer, and no caller reads a threshold.
}

// 이름이 일치하는 노드는, 이름은 전혀 안 맞고 경로 세그먼트 하나만 겹치는
// 노드보다 위에 와야 한다. 이름 신호가 긴 이름에서 너무 얕아지면 0.25배로
// 깎인 경로 신호에조차 진다.
func TestRerank_NameMatchOutranksPathOnlyMatch(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Name: "reset", QualifiedName: "repo.reset", FilePath: "internal/user/repo.go"},
		{ID: 2, Name: "userRepositoryFactory", QualifiedName: "factory.userRepositoryFactory", FilePath: "internal/factory/build.go"},
	}
	got := Rerank("user", nodes, 10)
	if got[0].ID != 2 {
		t.Fatalf("expected the name match (id=2) above the path-only match, got id=%d", got[0].ID)
	}
}

// 이름 신호는 관련 없는 이름과 진짜 일치를 갈라야 한다. 무관한 이름 중 최고
// 점수가 진짜 일치 중 최저 점수보다 낮아야, 그 사이에 순위 경계가 생긴다.
func TestNameSim_NoiseScoresBelowEveryRealMatch(t *testing.T) {
	qTokens := tokenize("user")
	matches := []string{
		"user", "userService", "getUserById",
		"userRepositoryFactory", "UserServiceImplementationFactory",
		"findUsersByTenantAndStatus",
	}
	unrelated := []string{
		"reset", "serve", "usage", "close",
		"marshalJSON", "handleRequest", "Error", "Shutdown",
	}

	worstMatch, worstName := 1.0, ""
	for _, name := range matches {
		if s := nameSim(qTokens, graph.Node{Name: name}); s < worstMatch {
			worstMatch, worstName = s, name
		}
	}
	bestNoise, noiseName := 0.0, ""
	for _, name := range unrelated {
		if s := nameSim(qTokens, graph.Node{Name: name}); s > bestNoise {
			bestNoise, noiseName = s, name
		}
	}
	if bestNoise >= worstMatch {
		t.Fatalf("noise %q scored %.4f, not below the weakest real match %q at %.4f",
			noiseName, bestNoise, worstName, worstMatch)
	}
}

// 글자가 빠진 오타(usr, paymnt)는 여전히 부분 수열이므로 잡힌다. 이건 오타
// 관용이 아니라 부분 수열 매칭의 부수 효과이고, 줄임말 검색과 같은 성질이다.
func TestNameSim_MissingLetterStaysASubsequence(t *testing.T) {
	cases := []struct{ query, name string }{
		{"usr", "user"},
		{"paymnt", "payment"},
		{"paymentProcesor", "paymentProcessor"},
	}
	noiseCeiling := nameSim(tokenize("user"), graph.Node{Name: "serve"})
	for _, c := range cases {
		got := nameSim(tokenize(c.query), graph.Node{Name: c.name})
		if got <= noiseCeiling {
			t.Errorf("nameSim(%q, %q)=%.4f, not above the noise level %.4f", c.query, c.name, got, noiseCeiling)
		}
	}
}

// 글자를 여럿 건너뛰며 겨우 부분 수열이 되는 이름(canonicalName 안의 c-o-n-n)은,
// 글자가 붙어서 일치하는 이름보다 확실히 낮아야 한다. 건너뛴 글자 수를 세지
// 않으면 둘이 거의 붙어서 순위가 우연에 좌우된다.
func TestNameSim_ScatteredMatchFallsWellBelowConsecutiveMatch(t *testing.T) {
	const wantMargin = 0.15
	cases := []struct{ query, consecutive, scattered string }{
		{"conn", "connectionPool", "canonicalName"},
		{"repo", "repository", "responseWriter"},
	}
	for _, c := range cases {
		qTokens := tokenize(c.query)
		hit := nameSim(qTokens, graph.Node{Name: c.consecutive})
		miss := nameSim(qTokens, graph.Node{Name: c.scattered})
		if hit-miss < wantMargin {
			t.Errorf("query %q: %s=%.4f vs %s=%.4f, margin %.4f below %.2f",
				c.query, c.consecutive, hit, c.scattered, miss, hit-miss, wantMargin)
		}
	}
}

// 건너뛴 글자를 벌하더라도 줄임말 검색은 살아 있어야 한다. FTS가 접두 검색을
// 하므로 이런 후보는 실제로 리랭커까지 올라온다.
func TestNameSim_AbbreviationsStayAboveZero(t *testing.T) {
	cases := []struct{ query, name string }{
		{"cfg", "configuration"},
		{"ctx", "context"},
		{"conn", "connectionPool"},
		{"auth", "authenticate"},
		{"repo", "repository"},
	}
	for _, c := range cases {
		if got := nameSim(tokenize(c.query), graph.Node{Name: c.name}); got <= 0 {
			t.Errorf("nameSim(%q, %q)=%.4f, want > 0", c.query, c.name, got)
		}
	}
}

// 순서가 어긋난 오타는 지원하지 않기로 했다. 부분 수열이 깨지므로 0이어야
// 한다. 그래야 없는 이름을 물어본 호출자가 그럴듯한 오답 대신 빈 결과를 받는다.
func TestNameSim_TransposedTypoScoresZero(t *testing.T) {
	cases := []struct{ query, name string }{
		{"reciept", "receipt"},
		{"retreival", "retrieval"},
		{"anotaiton", "annotation"},
	}
	for _, c := range cases {
		if got := nameSim(tokenize(c.query), graph.Node{Name: c.name}); got != 0 {
			t.Errorf("nameSim(%q, %q)=%.4f, want 0", c.query, c.name, got)
		}
	}
}

// 이름 신호가 약해도 진짜 일치라면, 이름은 전혀 안 맞고 경로 세그먼트만 겹치는
// 노드보다 위여야 한다. 줄임말이나 아주 긴 이름은 점수가 낮게 나온다.
func TestRerank_WeakNameMatchStillOutranksPathOnlyMatch(t *testing.T) {
	cases := []struct{ query, name string }{
		{"cfg", "loadConfig"},
		{"cfg", "configuration"},
		{"cfg", "ccgConfigFileGlobals"},
		{"repo", "postgresRepositoryAdapterFactory"},
		{"node", "graphNodeAnnotationRepositoryPostgres"},
	}
	for _, c := range cases {
		nodes := []graph.Node{
			// 이름은 무관하고 경로에만 쿼리 토큰이 들어 있다.
			{ID: 1, Name: "handler", QualifiedName: "http.handler", FilePath: "internal/" + c.query + "/handler.go"},
			{ID: 2, Name: c.name, QualifiedName: "app." + c.name, FilePath: "internal/app/boot.go"},
		}
		if got := Rerank(c.query, nodes, 10); got[0].ID != 2 {
			t.Errorf("query %q: path-only node beat the name match %q (nameSim=%.4f)",
				c.query, c.name, nameSim(tokenize(c.query), nodes[1]))
		}
	}
}

// 경로 신호를 낮춰도 이름이 동점일 때는 여전히 순위를 갈라야 한다.
func TestRerank_PathBreaksTiesBetweenEqualNames(t *testing.T) {
	t.Run("equal nonzero name scores", func(t *testing.T) {
		nodes := []graph.Node{
			{ID: 1, Name: "getUserById", QualifiedName: "a.getUserById", FilePath: "pkg/misc.go"},
			{ID: 2, Name: "getUserById", QualifiedName: "b.getUserById", FilePath: "internal/user/svc.go"},
		}
		if got := Rerank("user", nodes, 10); got[0].ID != 2 {
			t.Fatalf("expected the path hit to break the name tie, got id=%d", got[0].ID)
		}
	})
	t.Run("both name scores zero", func(t *testing.T) {
		nodes := []graph.Node{
			{ID: 1, Name: "helper", QualifiedName: "util.helper", FilePath: "util/helper.go"},
			{ID: 2, Name: "handler", QualifiedName: "svc.handler", FilePath: "svc/auth/login.go"},
		}
		if got := Rerank("auth login", nodes, 10); got[0].ID != 2 {
			t.Fatalf("expected the path hit to rank first when no name matches, got id=%d", got[0].ID)
		}
	})
}

func TestFetchLimit(t *testing.T) {
	if got := FetchLimit(10); got <= 10 {
		t.Fatalf("expected fetch limit wider than 10, got %d", got)
	}
	if got := FetchLimit(200); got != fetchCap {
		t.Fatalf("expected fetch limit capped at %d, got %d", fetchCap, got)
	}
}

// 부분 수열이 아니면 0이어야 한다. 이게 무관한 이름을 문턱값 없이 걸러내는
// 근거이므로, 0으로 떨어지는 성질 자체를 못박는다.
func TestSubsequenceScore_ZeroWhenQueryNotContained(t *testing.T) {
	notContained := [][2]string{
		{"user", "reset"}, {"user", "usage"}, {"user", "serve"},
		{"payment", "handler"}, {"user", "usr"}, // 질의가 대상보다 길다
	}
	for _, c := range notContained {
		if got := subsequenceScore(c[0], c[1]); got != 0 {
			t.Errorf("subsequenceScore(%q,%q)=%.4f, want 0", c[0], c[1], got)
		}
	}
	contained := [][2]string{
		{"user", "userService"}, {"user", "getUserById"},
		{"auth", "authenticate"}, {"repo", "responseWriter"},
	}
	for _, c := range contained {
		if got := subsequenceScore(c[0], c[1]); got <= 0 {
			t.Errorf("subsequenceScore(%q,%q)=%.4f, want > 0", c[0], c[1], got)
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

// A name that spells the query out as a whole word must outscore one that only
// contains its runes scattered. A left-to-right greedy matcher fails this: it
// binds the query's first rune to the name's first rune and then pays a gap
// penalty crossing to the real word.
func TestNameSim_WholeWordMatchBeatsScatteredMatch(t *testing.T) {
	cases := []struct {
		query     string
		word      string // contains the query as a whole sub-word
		scattered string // contains the query's runes only in order
	}{
		{"processor", "paymentProcessor", "pathResolverCacheSessionStore"},
		{"server", "startNewServer", "serviceProvider"},
		{"response", "parseJsonResponse", "resolveNamespace"},
	}
	for _, c := range cases {
		t.Run(c.query, func(t *testing.T) {
			word := nameSim([]string{c.query}, graph.Node{Name: c.word})
			scattered := nameSim([]string{c.query}, graph.Node{Name: c.scattered})
			if word <= scattered {
				t.Errorf("nameSim(%q, %q)=%.4f must exceed nameSim(%q, %q)=%.4f",
					c.query, c.word, word, c.query, c.scattered, scattered)
			}
		})
	}
}

// An acronym run hides the boundary that starts the next word: the S in
// HTTPServer follows an uppercase P, so an upper-after-lower test misses it.
func TestNameSim_AcronymRunDoesNotHideTheNextWordBoundary(t *testing.T) {
	acronym := nameSim([]string{"server"}, graph.Node{Name: "HTTPServer"})
	mixed := nameSim([]string{"server"}, graph.Node{Name: "HttpServer"})
	if acronym != mixed {
		t.Errorf("HTTPServer scored %.4f but HttpServer scored %.4f; the word boundary is the same", acronym, mixed)
	}
}

// Any name match at all must outrank a node that only shares a path segment,
// however faint the name evidence is. Adding a weighted path score to the name
// score cannot promise this: it only pushes the crossover point down, and some
// real match always lands under it.
func TestRerank_FaintestNameMatchStillOutranksPathOnlyMatch(t *testing.T) {
	const query = "adir"
	// 74 runes; "adir" matches only as a scattered subsequence, scoring 0.0096.
	const faint = "parseTreeSitterLanguageDefinitionRegistryConfigurationLoaderFactoryGlobals"

	nodes := []graph.Node{
		// The path-only node is the backend's top hit, so only the structural
		// signal can move the real name match above it.
		{ID: 1, Name: "handler", QualifiedName: "http.handler", FilePath: "internal/" + query + "/handler.go"},
		{ID: 2, Name: faint, QualifiedName: "app." + faint, FilePath: "internal/app/boot.go"},
	}
	score := nameSim(tokenize(query), nodes[1])
	if score <= 0 {
		t.Fatalf("test premise broken: %q must be a real name match, scored %.5f", query, score)
	}
	if got := Rerank(query, nodes, 10); got[0].ID != 2 {
		t.Errorf("path-only node beat a real name match scoring %.5f", score)
	}
}

// 타입 이름으로 검색하면 그 타입의 메서드도 근거를 가져야 한다. 메서드는
// 자기 이름과 qualified name의 마지막 조각이 똑같아서, 수신자 타입을 안 보면
// 두 자리 모두 같은 문자열을 재는 데 쓰인다.
func TestNameSim_MethodScoresItsReceiverType(t *testing.T) {
	qTokens := tokenize("syncqueue")
	method := graph.Node{Name: "Add", QualifiedName: "reposync.SyncQueue.Add"}
	if got := nameSim(qTokens, method); got <= 0 {
		t.Fatalf("nameSim(%q, %s) = %.4f, want > 0: the receiver type spells the query", "syncqueue", method.QualifiedName, got)
	}
}

// 수신자로 얻은 점수는 자기 이름으로 얻은 점수 아래여야 한다. 같으면 메서드
// 수십 개가 그 타입 노드와 동점이 되어, 정작 타입을 찾던 사람이 메서드 목록에
// 파묻힌다.
func TestNameSim_ReceiverMatchScoresBelowOwnNameMatch(t *testing.T) {
	qTokens := tokenize("syncqueue")
	own := nameSim(qTokens, graph.Node{Name: "SyncQueue", QualifiedName: "reposync.SyncQueue"})
	viaReceiver := nameSim(qTokens, graph.Node{Name: "Add", QualifiedName: "reposync.SyncQueue.Add"})
	if !(own > viaReceiver && viaReceiver > 0) {
		t.Fatalf("expected own > receiver > 0, got own=%.4f receiver=%.4f", own, viaReceiver)
	}
}

// 패키지 이름은 수신자가 아니다. 조각이 둘뿐인 최상위 함수에서 앞 조각을
// 수신자로 읽으면, 패키지 이름을 친 질의가 그 패키지의 함수 전부를 이름 일치로
// 끌어올린다. 패키지 근접성은 경로 점수가 이미 재고 있다.
func TestNameSim_PackageSegmentIsNotAReceiver(t *testing.T) {
	qTokens := tokenize("reposync")
	fn := graph.Node{Name: "NewQueue", QualifiedName: "reposync.NewQueue"}
	if got := nameSim(qTokens, fn); got != 0 {
		t.Errorf("nameSim(%q, %s) = %.4f, want 0: reposync is the package, not a receiver type", "reposync", fn.QualifiedName, got)
	}
}

// 타입 이름 질의에서 타입 노드가 자기 메서드들 위에 있어야 한다.
func TestRerank_TypeQueryKeepsTheTypeAboveItsMethods(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Name: "Add", QualifiedName: "reposync.SyncQueue.Add", FilePath: "internal/app/reposync/queue.go"},
		{ID: 2, Name: "Shutdown", QualifiedName: "reposync.SyncQueue.Shutdown", FilePath: "internal/app/reposync/queue.go"},
		{ID: 3, Name: "SyncQueue", QualifiedName: "reposync.SyncQueue", FilePath: "internal/app/reposync/queue.go"},
	}
	got := Rerank("syncqueue", nodes, 10)
	if got[0].ID != 3 {
		t.Fatalf("expected the type itself first, got %s", got[0].QualifiedName)
	}
	if got[1].ID != 1 || got[2].ID != 2 {
		t.Errorf("methods should follow in retrieval order, got %s then %s", got[1].QualifiedName, got[2].QualifiedName)
	}
}
