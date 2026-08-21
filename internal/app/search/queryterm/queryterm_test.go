package queryterm

import "testing"

func TestIsNaturalLanguage(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{name: "question marker", query: "why graph updates", want: true},
		{name: "explanation with question marker", query: "explain why graph updates", want: true},
		{name: "long descriptive phrase", query: "graph updates use a background job", want: true},
		{name: "three code terms", query: "session token credentials", want: false},
		{name: "function word does not widen", query: "get user by id", want: false},
		{name: "single identifier", query: "buildOrUpdateGraph", want: false},
		{name: "ambiguous command name", query: "explain", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNaturalLanguage(tt.query); got != tt.want {
				t.Fatalf("IsNaturalLanguage(%q) = %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}
