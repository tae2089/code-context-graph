package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestResolveExcludes_MergesConfigAndFlag(t *testing.T) {
	viper.Reset()
	viper.Set("exclude", []string{"vendor", "*.pb.go"})

	result := resolveExcludes([]string{"*_test.go"})

	if len(result) != 3 {
		t.Fatalf("expected 3 patterns, got %d: %v", len(result), result)
	}

	has := func(s string) bool {
		for _, r := range result {
			if r == s {
				return true
			}
		}
		return false
	}

	for _, want := range []string{"vendor", "*.pb.go", "*_test.go"} {
		if !has(want) {
			t.Errorf("expected %q in result %v", want, result)
		}
	}

	viper.Reset()
}

func TestResolveExcludes_NoConfigUsesFlag(t *testing.T) {
	viper.Reset()

	result := resolveExcludes([]string{"vendor"})
	if len(result) != 1 || result[0] != "vendor" {
		t.Errorf("expected [vendor], got %v", result)
	}

	viper.Reset()
}

func TestResolveExcludes_EmptyFlagUsesConfig(t *testing.T) {
	viper.Reset()
	viper.Set("exclude", []string{"vendor", "*.gen.go"})

	result := resolveExcludes(nil)
	if len(result) != 2 {
		t.Fatalf("expected 2 patterns, got %d: %v", len(result), result)
	}

	viper.Reset()
}

func TestConfigFlag_LoadsYAML(t *testing.T) {
	cfgFile := filepath.Join(t.TempDir(), ".ccg.yaml")
	if err := os.WriteFile(cfgFile, []byte("exclude:\n  - vendor\n  - \"*.pb.go\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deps, stdout, stderr := newTestDeps()
	// tags command doesn't need DB, so it works without InitFunc
	if err := executeCmd(deps, stdout, stderr, "--config", cfgFile, "tags"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(stdout.String(), "@index") {
		t.Errorf("expected tags output, got: %s", stdout.String())
	}
}
