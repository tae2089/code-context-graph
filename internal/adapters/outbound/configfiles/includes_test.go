package configfiles_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/configfiles"
)

func TestIncludePathsLoad(t *testing.T) {
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

			got, err := (configfiles.IncludePaths{}).Load(dir)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("Load() = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("Load()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func stringPtr(value string) *string { return &value }
