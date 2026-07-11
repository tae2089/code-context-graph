package pathutil_test

import (
	"testing"

	"github.com/tae2089/code-context-graph/internal/pathutil"
)

func TestValidateNamespacePath(t *testing.T) {
	cases := []struct {
		name      string
		namespace string
		filePath  string
		wantErr   bool
	}{
		{"simple name", "api", "", false},
		{"with dash", "org-api", "", false},
		{"name + relative file", "api", "docs/x.md", false},
		{"empty namespace", "", "", true},
		{"dot", ".", "", true},
		{"dotdot", "..", "", true},
		{"traversal prefix", "../etc", "", true},
		{"forward slash in name", "a/b", "", true},
		{"back slash in name", `a\b`, "", true},
		{"absolute namespace", "/etc", "", true},
		{"absolute file", "api", "/etc/passwd", true},
		{"traversal file", "api", "../secret", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := pathutil.ValidateNamespacePath(tc.namespace, tc.filePath)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateNamespacePath(%q, %q) = nil, want error", tc.namespace, tc.filePath)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateNamespacePath(%q, %q) = %v, want nil", tc.namespace, tc.filePath, err)
			}
		})
	}
}
