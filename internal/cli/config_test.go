package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/tae2089/code-context-graph/internal/model"
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

func TestResolveOutDir_UsesConfigWhenDefault(t *testing.T) {
	viper.Reset()
	viper.Set("docs.out", "generated-docs")

	result := resolveOutDir("docs")
	if result != "generated-docs" {
		t.Errorf("expected 'generated-docs', got %q", result)
	}

	viper.Reset()
}

func TestResolveOutDir_PrefersFlagOverConfig(t *testing.T) {
	viper.Reset()
	viper.Set("docs.out", "generated-docs")

	result := resolveOutDir("my-output")
	if result != "my-output" {
		t.Errorf("expected 'my-output', got %q", result)
	}

	viper.Reset()
}

func TestResolveIncludePaths_MergesConfigAndFlag(t *testing.T) {
	viper.Reset()
	viper.Set("include_paths", []string{"src/api"})

	result := resolveIncludePaths([]string{"src/auth"})

	if len(result) != 2 {
		t.Fatalf("expected 2 paths, got %d: %v", len(result), result)
	}

	has := func(s string) bool {
		for _, r := range result {
			if r == s {
				return true
			}
		}
		return false
	}

	for _, want := range []string{"src/api", "src/auth"} {
		if !has(want) {
			t.Errorf("expected %q in result %v", want, result)
		}
	}

	viper.Reset()
}

func TestResolveIncludePaths_EmptyFlagUsesConfig(t *testing.T) {
	viper.Reset()
	viper.Set("include_paths", []string{"src/core", "src/api"})

	result := resolveIncludePaths(nil)
	if len(result) != 2 {
		t.Fatalf("expected 2 paths, got %d: %v", len(result), result)
	}

	viper.Reset()
}

func TestResolveIncludePaths_EmptyBothReturnsNil(t *testing.T) {
	viper.Reset()

	result := resolveIncludePaths(nil)
	if len(result) != 0 {
		t.Fatalf("expected empty, got %v", result)
	}

	viper.Reset()
}

func TestBuildCommand_PathFromConfig(t *testing.T) {
	deps, stdout, stderr, db := setupBuildTest(t)

	dir := t.TempDir()

	apiDir := filepath.Join(dir, "src", "api")
	os.MkdirAll(apiDir, 0755)
	writeGoFile(t, apiDir, "handler.go", `package api
func Handler() {}
`)

	otherDir := filepath.Join(dir, "src", "other")
	os.MkdirAll(otherDir, 0755)
	writeGoFile(t, otherDir, "other.go", `package other
func Other() {}
`)

	viper.Reset()
	viper.Set("include_paths", []string{"src/api"})
	defer viper.Reset()

	err := executeCmd(deps, stdout, stderr, "build", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var nodes []model.Node
	db.Find(&nodes)

	for _, n := range nodes {
		if n.Name == "Other" {
			t.Error("expected Other NOT to be parsed when config has include_paths=[src/api]")
		}
	}

	foundHandler := false
	for _, n := range nodes {
		if n.Name == "Handler" {
			foundHandler = true
		}
	}
	if !foundHandler {
		t.Error("expected Handler to be parsed (in config include_paths)")
	}
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
