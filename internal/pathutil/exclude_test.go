package pathutil_test

import (
	"testing"

	"github.com/imtaebin/code-context-graph/internal/pathutil"
)

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
			got := pathutil.MatchExcludes(tt.patterns, tt.relPath)
			if got != tt.want {
				t.Errorf("MatchExcludes(%v, %q) = %v, want %v", tt.patterns, tt.relPath, got, tt.want)
			}
		})
	}
}
