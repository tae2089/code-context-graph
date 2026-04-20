package pathutil_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tae2089/code-context-graph/internal/pathutil"
)

func TestLoadIncludePathsFromConfig_WithFile(t *testing.T) {
	dir := t.TempDir()
	content := []byte("include_paths:\n  - src/api\n  - src/auth\n")
	if err := os.WriteFile(filepath.Join(dir, ".ccg.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}

	got := pathutil.LoadIncludePathsFromConfig(dir)
	want := []string{"src/api", "src/auth"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLoadIncludePathsFromConfig_NoFile(t *testing.T) {
	dir := t.TempDir()
	got := pathutil.LoadIncludePathsFromConfig(dir)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestLoadIncludePathsFromConfig_NoKey(t *testing.T) {
	dir := t.TempDir()
	content := []byte("exclude_patterns:\n  - vendor\n")
	if err := os.WriteFile(filepath.Join(dir, ".ccg.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}

	got := pathutil.LoadIncludePathsFromConfig(dir)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestLoadBinderMaxGapFromConfig_WithKey(t *testing.T) {
	dir := t.TempDir()
	content := []byte("binder:\n  max_gap: 5\n")
	if err := os.WriteFile(filepath.Join(dir, ".ccg.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}

	got := pathutil.LoadBinderMaxGapFromConfig(dir)
	if got != 5 {
		t.Errorf("expected 5, got %d", got)
	}
}

func TestLoadBinderMaxGapFromConfig_NoFile(t *testing.T) {
	dir := t.TempDir()
	got := pathutil.LoadBinderMaxGapFromConfig(dir)
	if got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestLoadBinderMaxGapFromConfig_NoKey(t *testing.T) {
	dir := t.TempDir()
	content := []byte("exclude:\n  - vendor\n")
	if err := os.WriteFile(filepath.Join(dir, ".ccg.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}

	got := pathutil.LoadBinderMaxGapFromConfig(dir)
	if got != 0 {
		t.Errorf("expected 0 (key absent), got %d", got)
	}
}

func TestLoadBinderMaxGapFromConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	content := []byte("binder: !!invalid\n  max_gap: [not a number\n")
	if err := os.WriteFile(filepath.Join(dir, ".ccg.yaml"), content, 0644); err != nil {
		t.Fatal(err)
	}

	got := pathutil.LoadBinderMaxGapFromConfig(dir)
	if got != 0 {
		t.Errorf("expected 0 on parse error, got %d", got)
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
			got := pathutil.MatchExcludes(tt.patterns, tt.relPath)
			if got != tt.want {
				t.Errorf("MatchExcludes(%v, %q) = %v, want %v", tt.patterns, tt.relPath, got, tt.want)
			}
		})
	}
}
