package pathspec_test

import (
	"testing"

	"github.com/tae2089/code-context-graph/internal/pathspec"
)

func TestMatchIncludePaths(t *testing.T) {
	tests := []struct {
		name         string
		relPath      string
		includePaths []string
		want         bool
	}{
		{name: "exact include path", relPath: "src/api", includePaths: []string{"src/api"}, want: true},
		{name: "child file under include path", relPath: "src/api/handler.go", includePaths: []string{"src/api"}, want: true},
		{name: "parent dir kept for traversal", relPath: "src", includePaths: []string{"src/api"}, want: true},
		{name: "sibling path not matched", relPath: "src/api2/handler.go", includePaths: []string{"src/api"}, want: false},
		{name: "short prefix not matched", relPath: "src/a", includePaths: []string{"src/api"}, want: false},
		{name: "normalized separators", relPath: `src\\api\\handler.go`, includePaths: []string{"src/api"}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pathspec.MatchIncludePaths(tt.relPath, tt.includePaths)
			if got != tt.want {
				t.Errorf("MatchIncludePaths(%q, %v) = %v, want %v", tt.relPath, tt.includePaths, got, tt.want)
			}
		})
	}
}

func TestMatchExcludes(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		relPath  string
		want     bool
	}{
		// 빈 패턴 → 항상 false
		{name: "no patterns", patterns: nil, relPath: "internal/foo.go", want: false},

		// 경로 프리픽스 매칭 (디렉토리 제외)
		{name: "prefix match vendor", patterns: []string{"vendor"}, relPath: "vendor/pkg/foo.go", want: true},
		{name: "prefix match dir", patterns: []string{"internal/legacy"}, relPath: "internal/legacy/bar.go", want: true},
		{name: "prefix no match", patterns: []string{"internal/legacy"}, relPath: "internal/legacyx/bar.go", want: false},
		{name: "prefix exact dir", patterns: []string{"internal/legacy"}, relPath: "internal/legacy", want: true},
		{name: "prefix trailing slash", patterns: []string{"vendor/"}, relPath: "vendor/foo.go", want: true},

		// 파일명 glob (패턴에 / 없음)
		{name: "filename glob pb.go", patterns: []string{"*.pb.go"}, relPath: "internal/foo.pb.go", want: true},
		{name: "filename glob no match", patterns: []string{"*.pb.go"}, relPath: "internal/foo.go", want: false},
		{name: "filename glob deep", patterns: []string{"*.pb.go"}, relPath: "a/b/c/foo.pb.go", want: true},
		{name: "filename glob test", patterns: []string{"*_test.go"}, relPath: "pkg/foo_test.go", want: true},

		// 전체 경로 glob (패턴에 / 포함)
		{name: "path glob", patterns: []string{"internal/gen/*.go"}, relPath: "internal/gen/foo.go", want: true},
		{name: "path glob no match dir", patterns: []string{"internal/gen/*.go"}, relPath: "internal/other/foo.go", want: false},

		// 복수 패턴
		{name: "multi pattern first matches", patterns: []string{"vendor", "*.pb.go"}, relPath: "vendor/foo.go", want: true},
		{name: "multi pattern second matches", patterns: []string{"vendor", "*.pb.go"}, relPath: "internal/foo.pb.go", want: true},
		{name: "multi pattern none match", patterns: []string{"vendor", "*.pb.go"}, relPath: "internal/foo.go", want: false},

		// OS 경로 구분자 (Windows 스타일 입력)
		{name: "backslash relpath", patterns: []string{"internal/legacy"}, relPath: `internal\legacy\foo.go`, want: true},

		{name: "regex test suffix", patterns: []string{`.*_test\.go$`}, relPath: "pkg/foo_test.go", want: true},
		{name: "regex test suffix no match", patterns: []string{`.*_test\.go$`}, relPath: "pkg/foo.go", want: false},
		{name: "regex pb.go suffix", patterns: []string{`.*\.pb\.go$`}, relPath: "internal/foo.pb.go", want: true},
		{name: "regex pb.go suffix no match", patterns: []string{`.*\.pb\.go$`}, relPath: "internal/foo.go", want: false},
		{name: "regex vendor prefix", patterns: []string{`vendor/.*`}, relPath: "vendor/pkg/foo.go", want: true},
		{name: "regex vendor prefix no match", patterns: []string{`vendor/.*`}, relPath: "internal/foo.go", want: false},
		{name: "regex deep path", patterns: []string{`.*_test\.go$`}, relPath: "a/b/c/foo_test.go", want: true},
		{name: "regex mixed with glob", patterns: []string{`.*_test\.go$`, "vendor"}, relPath: "vendor/foo.go", want: true},
		{name: "regex anchored end only", patterns: []string{`_generated\.go$`}, relPath: "internal/foo_generated.go", want: true},
		{name: "regex anchored end no match", patterns: []string{`_generated\.go$`}, relPath: "internal/foo_generated.go.bak", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pathspec.MatchExcludes(tt.patterns, tt.relPath)
			if got != tt.want {
				t.Errorf("MatchExcludes(%v, %q) = %v, want %v", tt.patterns, tt.relPath, got, tt.want)
			}
		})
	}
}

func TestHasPathPrefix(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		prefix string
		want   bool
	}{
		{name: "exact match", path: "internal/api", prefix: "internal/api", want: true},
		{name: "child path", path: "internal/api/handler.go", prefix: "internal/api", want: true},
		{name: "sibling not matched", path: "internal/api2/handler.go", prefix: "internal/api", want: false},
		{name: "shorter path not matched", path: "internal", prefix: "internal/api", want: false},
		{name: "windows separators normalized", path: `internal\api\handler.go`, prefix: "internal/api", want: true},
		{name: "empty prefix matches all", path: "internal/api/handler.go", prefix: "", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pathspec.HasPathPrefix(tt.path, tt.prefix)
			if got != tt.want {
				t.Errorf("HasPathPrefix(%q, %q) = %v, want %v", tt.path, tt.prefix, got, tt.want)
			}
		})
	}
}
