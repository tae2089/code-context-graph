package evidence

import (
	"slices"
	"testing"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// Korean glues the particle onto the noun, so a reason about 네임스페이스 is
// written 네임스페이스를 and the two are one token. The index knows that — it
// asks for 네임스페이스* — and the cut used to disagree, comparing the two for
// equality and dropping a node whose reason answers the question in the
// language it was asked in. Writing reasons in Korean and searching them is
// what this tool is for, so that path was the first one cut.
func TestBuild_KeepsTheNodeAKoreanReasonAnswers(t *testing.T) {
	nodes := []graph.Node{{
		ID: 1, Name: "isolate", QualifiedName: "postgres.isolate",
		FilePath:   "internal/adapters/outbound/searchsql/postgres.go",
		Annotation: annotated("테스트마다 네임스페이스를 격리해서 서로 간섭하지 않게 한다"),
	}}

	got := Build("네임스페이스 격리", nodes, Options{Limit: 10})

	if want := []uint{1}; !slices.Equal(ids(got), want) {
		t.Fatalf("kept %v, want %v (WeakFiltered=%d, Note=%q)", ids(got), want, got.WeakFiltered, got.Note)
	}
	if hits := got.Hits(); !slices.Contains(hits[0].Matched, MatchIntent) {
		t.Errorf("Matched = %v, want it to name the recorded reason", hits[0].Matched)
	}
}

// The minimum length is a rule about Latin words, where the indexed token
// already is the whole word and a prefix only buys inflections. It does not
// apply outside ASCII, where a prefix is the only way a two-syllable noun
// reaches the reason that glued a particle onto it.
func TestBuild_AShortKoreanWordStillReachesItsReason(t *testing.T) {
	nodes := []graph.Node{{
		ID: 1, Name: "resume", QualifiedName: "reposync.resume",
		FilePath:   "internal/app/reposync/queue.go",
		Annotation: annotated("장애 뒤 복구를 어디서부터 시작할지 정한다"),
	}}

	got := Build("복구", nodes, Options{Limit: 10})

	if want := []uint{1}; !slices.Equal(ids(got), want) {
		t.Fatalf("kept %v, want %v (WeakFiltered=%d, Note=%q)", ids(got), want, got.WeakFiltered, got.Note)
	}
	if hits := got.Hits(); !slices.Contains(hits[0].Matched, MatchIntent) {
		t.Errorf("Matched = %v, want it to name the recorded reason", hits[0].Matched)
	}
}

// The other half of the same rule, and the reason it is not "prefix always":
// `run` is three runes, and letting it reach `runtime` is how a short Latin
// word starts matching every reason that happens to begin with it. The index
// does not ask for `run*`, so the cut must not answer as though it had.
func TestBuild_AShortLatinWordDoesNotReachALongerReasonWord(t *testing.T) {
	nodes := []graph.Node{{
		ID: 1, Name: "check", QualifiedName: "db.check",
		FilePath:   "internal/db/schema.go",
		Annotation: annotated("refuse a runtime whose schema is older than the binary"),
	}}

	got := Build("run", nodes, Options{Limit: 10})

	if len(got.Files) != 0 {
		t.Fatalf("kept %v, want nothing — a three-rune Latin term is not a prefix match", ids(got))
	}
}
