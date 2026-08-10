package intent_test

import (
	"testing"

	"github.com/tae2089/code-context-graph/internal/app/search/intent"
)

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
