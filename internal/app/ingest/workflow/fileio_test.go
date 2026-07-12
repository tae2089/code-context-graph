package workflow

import "testing"

func TestShouldSkipDir(t *testing.T) {
	tests := map[string]struct {
		name string
		want bool
	}{
		"git directory":     {name: ".git", want: true},
		"vendor directory":  {name: "vendor", want: true},
		"node modules":      {name: "node_modules", want: true},
		"hidden directory":  {name: ".cache", want: true},
		"current directory": {name: ".", want: false},
		"source directory":  {name: "internal", want: false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := shouldSkipDir(tt.name); got != tt.want {
				t.Fatalf("shouldSkipDir(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}
