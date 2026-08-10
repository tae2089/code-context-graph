package intent_test

import (
	"testing"

	"github.com/tae2089/code-context-graph/internal/app/search/intent"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// The index takes @intent and @domainRule, so a node carrying only a domain
// rule earned its place on that rule. Reading back only @intent would present
// exactly that node with a blank line where its reason goes.
func TestRecordedReason_FallsBackToADomainRule(t *testing.T) {
	node := graph.Node{ID: 7, Name: "charge"}
	node.Annotation = &graph.Annotation{NodeID: 7, Tags: []graph.DocTag{
		{Kind: graph.TagDomainRule, Value: "a refund never exceeds the original charge"},
	}}
	if got := intent.RecordedReason(node); got != "a refund never exceeds the original charge" {
		t.Errorf("reason = %q, want the recorded domain rule", got)
	}
}

// When both tags are present, @intent wins: it says why the code exists, and
// that is what the index was asked about.
func TestRecordedReason_PrefersIntentOverADomainRule(t *testing.T) {
	node := graph.Node{ID: 7, Name: "charge"}
	node.Annotation = &graph.Annotation{NodeID: 7, Tags: []graph.DocTag{
		{Kind: graph.TagDomainRule, Value: "a refund never exceeds the original charge"},
		{Kind: graph.TagIntent, Value: "collect payment exactly once"},
	}}
	if got := intent.RecordedReason(node); got != "collect payment exactly once" {
		t.Errorf("reason = %q, want the @intent line", got)
	}
}

func TestRecordedReason_IsEmptyWhenNothingWasRecorded(t *testing.T) {
	if got := intent.RecordedReason(graph.Node{ID: 1, Name: "bare"}); got != "" {
		t.Errorf("reason = %q, want empty for an unannotated node", got)
	}
}

// CanAnswer is the membership gate: a question mostly made of words nobody
// ever wrote down was not answered by the one common word it shares with many
// reasons, while a real question keeps its hits even when each hit matched only
// the couple of words that mattered.
func TestCanAnswer_RequiresHalfTheTermsToBeKnown(t *testing.T) {
	cases := []struct {
		name  string
		terms []intent.Term
		want  bool
	}{
		{"mostly unknown words", []intent.Term{
			{Text: "zzz", InReasons: 0}, {Text: "nonexistent", InReasons: 0},
			{Text: "symbol", InReasons: 52}, {Text: "qqq", InReasons: 0},
		}, false},
		{"exactly half known", []intent.Term{
			{Text: "push", InReasons: 12}, {Text: "trigger", InReasons: 8},
			{Text: "loyalty", InReasons: 0}, {Text: "voucher", InReasons: 0},
		}, true},
		{"all known", []intent.Term{
			{Text: "push", InReasons: 12}, {Text: "build", InReasons: 40},
		}, true},
		{"no scored terms", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := intent.Result{Terms: tc.terms}
			if got := result.CanAnswer(); got != tc.want {
				t.Errorf("CanAnswer() = %v, want %v", got, tc.want)
			}
		})
	}
}
