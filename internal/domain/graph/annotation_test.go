package graph

import "testing"

func TestNodeIntent(t *testing.T) {
	tests := []struct {
		name string
		node Node
		want string
	}{
		{
			name: "no annotation at all",
			node: Node{Name: "Build"},
			want: "",
		},
		{
			name: "annotation without an intent tag",
			node: Node{Name: "Build", Annotation: &Annotation{
				Summary: "Build walks source files.",
				Tags:    []DocTag{{Kind: TagRequires, Value: "a directory"}},
			}},
			want: "",
		},
		{
			name: "the intent tag's value",
			node: Node{Name: "Build", Annotation: &Annotation{
				Summary: "Build walks source files.",
				Tags: []DocTag{
					{Kind: TagRequires, Value: "a directory"},
					{Kind: TagIntent, Value: "perform a full graph build"},
				},
			}},
			want: "perform a full graph build",
		},
		{
			// A declaration can only state one purpose, so a second @intent is a
			// writing mistake rather than a list. Taking the first keeps the
			// result stable instead of depending on tag order after a reload.
			name: "the first intent tag when there are several",
			node: Node{Name: "Build", Annotation: &Annotation{Tags: []DocTag{
				{Kind: TagIntent, Value: "first", Ordinal: 0},
				{Kind: TagIntent, Value: "second", Ordinal: 1},
			}}},
			want: "first",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.node.Intent(); got != tt.want {
				t.Errorf("Intent() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The recorded-reason index takes @intent and @domainRule, so a node carrying
// only a domain rule earned its place on that rule. Reading back only @intent
// would present exactly that node with a blank line where its reason goes.
func TestNodeRecordedReason_FallsBackToADomainRule(t *testing.T) {
	node := Node{ID: 7, Name: "charge"}
	node.Annotation = &Annotation{NodeID: 7, Tags: []DocTag{
		{Kind: TagDomainRule, Value: "a refund never exceeds the original charge"},
	}}
	if got := node.RecordedReason(); got != "a refund never exceeds the original charge" {
		t.Errorf("reason = %q, want the recorded domain rule", got)
	}
}

// When both tags are present, @intent wins: it says why the code exists, and
// that is what the index was asked about.
func TestNodeRecordedReason_PrefersIntentOverADomainRule(t *testing.T) {
	node := Node{ID: 7, Name: "charge"}
	node.Annotation = &Annotation{NodeID: 7, Tags: []DocTag{
		{Kind: TagDomainRule, Value: "a refund never exceeds the original charge"},
		{Kind: TagIntent, Value: "collect payment exactly once"},
	}}
	if got := node.RecordedReason(); got != "collect payment exactly once" {
		t.Errorf("reason = %q, want the @intent line", got)
	}
}

func TestNodeRecordedReason_IsEmptyWhenNothingWasRecorded(t *testing.T) {
	if got := (Node{ID: 1, Name: "bare"}).RecordedReason(); got != "" {
		t.Errorf("reason = %q, want empty for an unannotated node", got)
	}
}

// A blank domain rule is not a reason. Without the check the readback would
// hand a caller an empty string it then treats as "a reason exists".
func TestNodeRecordedReason_IgnoresABlankDomainRule(t *testing.T) {
	node := Node{ID: 7, Name: "charge"}
	node.Annotation = &Annotation{NodeID: 7, Tags: []DocTag{
		{Kind: TagDomainRule, Value: "   "},
	}}
	if got := node.RecordedReason(); got != "" {
		t.Errorf("reason = %q, want empty for a blank domain rule", got)
	}
}
