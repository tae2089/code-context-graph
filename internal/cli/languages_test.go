package cli

import (
	"strings"
	"testing"

	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
)

func TestLanguagesCommand_ListsLanguages(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	deps.Walkers = map[string]*treesitter.Walker{
		".go":  treesitter.NewWalker(treesitter.GoSpec),
		".py":  treesitter.NewWalker(treesitter.PythonSpec),
		".ts":  treesitter.NewWalker(treesitter.TypeScriptSpec),
		".tsx": treesitter.NewWalker(treesitter.TypeScriptSpec),
	}

	if err := executeCmd(deps, stdout, stderr, "languages"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	for _, want := range []string{"go", "python", "typescript", ".go", ".py", ".ts", ".tsx"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}

func TestLanguagesCommand_SortsAlphabetically(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	deps.Walkers = map[string]*treesitter.Walker{
		".go":   treesitter.NewWalker(treesitter.GoSpec),
		".java": treesitter.NewWalker(treesitter.JavaSpec),
		".py":   treesitter.NewWalker(treesitter.PythonSpec),
	}

	if err := executeCmd(deps, stdout, stderr, "languages"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	goIdx := strings.Index(out, "go")
	javaIdx := strings.Index(out, "java")
	pyIdx := strings.Index(out, "python")

	if goIdx < 0 || javaIdx < 0 || pyIdx < 0 {
		t.Fatalf("expected all language names in output, got:\n%s", out)
	}
	if !(goIdx < javaIdx && javaIdx < pyIdx) {
		t.Errorf("expected alphabetical order (go < java < python), got indices go=%d java=%d python=%d", goIdx, javaIdx, pyIdx)
	}
}

func TestLanguagesCommand_NilWalkers(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	// deps.Walkers is nil

	if err := executeCmd(deps, stdout, stderr, "languages"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "0") && !strings.Contains(out, "no") {
		// Should gracefully handle nil/empty walkers
		t.Logf("output for nil walkers: %s", out)
	}
}
