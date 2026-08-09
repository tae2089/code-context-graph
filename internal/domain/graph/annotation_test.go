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
