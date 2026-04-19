package eval

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/imtaebin/code-context-graph/internal/parse/treesitter"
)

func TestRunParserEval_Update(t *testing.T) {
	walker := treesitter.NewWalker(treesitter.GoSpec)

	dir := t.TempDir()
	copyTestCorpus(t, dir, "go", "sample.go", `package sample

func Hello() {}

func World() {
	Hello()
}
`)

	var buf bytes.Buffer
	opts := RunOptions{
		CorpusDir: dir,
		Suite:     "parser",
		Format:    "table",
		Update:    true,
		Walkers:   map[string]*treesitter.Walker{"go": walker},
		Writer:    &buf,
	}

	_, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("Run update: %v", err)
	}

	corpora, err := LoadGoldenDir(dir)
	if err != nil {
		t.Fatalf("LoadGoldenDir: %v", err)
	}
	if len(corpora) != 1 {
		t.Fatalf("corpora: got %d, want 1", len(corpora))
	}

	found := false
	for _, n := range corpora[0].Nodes {
		if n.Name == "Hello" && n.Kind == "function" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Hello function in golden, got: %+v", corpora[0].Nodes)
	}
}

func TestRunParserEval_Compare(t *testing.T) {
	walker := treesitter.NewWalker(treesitter.GoSpec)

	dir := t.TempDir()
	copyTestCorpus(t, dir, "go", "sample.go", `package sample

func Hello() {}

func World() {
	Hello()
}
`)

	walkers := map[string]*treesitter.Walker{"go": walker}

	var buf bytes.Buffer
	_, err := Run(context.Background(), RunOptions{
		CorpusDir: dir, Suite: "parser", Format: "table",
		Update: true, Walkers: walkers, Writer: &buf,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	buf.Reset()
	report, err := Run(context.Background(), RunOptions{
		CorpusDir: dir, Suite: "parser", Format: "table",
		Walkers: walkers, Writer: &buf,
	})
	if err != nil {
		t.Fatalf("compare: %v", err)
	}

	if len(report.Languages) != 1 {
		t.Fatalf("languages: got %d, want 1", len(report.Languages))
	}

	lr := report.Languages[0]
	if !approxEqual(lr.NodeMetrics.F1, 1.0) {
		t.Errorf("node F1: got %f, want 1.0", lr.NodeMetrics.F1)
	}
	if buf.Len() == 0 {
		t.Error("expected table output")
	}
}

func copyTestCorpus(t *testing.T, dir, lang, filename, content string) {
	t.Helper()
	langDir := filepath.Join(dir, lang)
	os.MkdirAll(langDir, 0o755)
	os.WriteFile(filepath.Join(langDir, filename), []byte(content), 0o644)
}
