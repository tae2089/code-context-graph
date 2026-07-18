package configfiles_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/configfiles"
)

func TestBuildScopeLoad_IncludePaths(t *testing.T) {
	tests := []struct {
		name    string
		content *string
		want    []string
		wantErr bool
	}{
		{name: "missing file", want: nil},
		{name: "missing key", content: stringPtr("exclude_patterns:\n  - vendor\n"), want: nil},
		{name: "valid include paths", content: stringPtr("include_paths:\n  - src/api\n  - src/auth\n"), want: []string{"src/api", "src/auth"}},
		{name: "malformed yaml", content: stringPtr("include_paths: [src/api\n"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.content != nil {
				if err := os.WriteFile(filepath.Join(dir, ".ccg.yaml"), []byte(*tt.content), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			got, err := (configfiles.BuildScope{}).Load(dir)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if len(got.IncludePaths) != len(tt.want) {
				t.Fatalf("IncludePaths = %v, want %v", got.IncludePaths, tt.want)
			}
			for i := range tt.want {
				if got.IncludePaths[i] != tt.want[i] {
					t.Fatalf("IncludePaths[%d] = %q, want %q", i, got.IncludePaths[i], tt.want[i])
				}
			}
		})
	}
}

func TestBuildScopeLoad_ProvidesExcludePatterns(t *testing.T) {
	dir := t.TempDir()
	content := "include_paths:\n  - cmd\n  - internal\nexclude:\n  - vendor\n  - \"*_generated.go\"\n"
	if err := os.WriteFile(filepath.Join(dir, ".ccg.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	scope, err := (configfiles.BuildScope{}).Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	wantIncludes := []string{"cmd", "internal"}
	if len(scope.IncludePaths) != len(wantIncludes) {
		t.Fatalf("IncludePaths = %v, want %v", scope.IncludePaths, wantIncludes)
	}
	for i := range wantIncludes {
		if scope.IncludePaths[i] != wantIncludes[i] {
			t.Fatalf("IncludePaths[%d] = %q, want %q", i, scope.IncludePaths[i], wantIncludes[i])
		}
	}
	wantExcludes := []string{"vendor", "*_generated.go"}
	if len(scope.ExcludePatterns) != len(wantExcludes) {
		t.Fatalf("ExcludePatterns = %v, want %v", scope.ExcludePatterns, wantExcludes)
	}
	for i := range wantExcludes {
		if scope.ExcludePatterns[i] != wantExcludes[i] {
			t.Fatalf("ExcludePatterns[%d] = %q, want %q", i, scope.ExcludePatterns[i], wantExcludes[i])
		}
	}
}

func stringPtr(value string) *string { return &value }
