package reference

import "testing"

func TestCommonSuffixDepth(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{name: "exact path", a: "github.com/acme/api", b: "github.com/acme/api", want: 3},
		{name: "shared trailing segments", a: "github.com/acme/api", b: "example.com/team/api", want: 1},
		{name: "leading and trailing slashes", a: "/acme/api/", b: "/team/api/", want: 1},
		{name: "no shared suffix", a: "acme/api", b: "team/web", want: 0},
		{name: "empty input", a: "", b: "team/api", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CommonSuffixDepth(tt.a, tt.b); got != tt.want {
				t.Fatalf("CommonSuffixDepth(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
