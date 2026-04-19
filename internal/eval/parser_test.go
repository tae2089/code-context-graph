package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadGoldenCorpus(t *testing.T) {
	dir := t.TempDir()

	corpus := GoldenCorpus{
		Language: "go",
		File:     "sample.go",
		Nodes: []EvalNode{
			{ID: "n1", Kind: "function", Name: "Hello", File: "sample.go", StartLine: 3},
		},
		Edges: []EvalEdge{
			{Kind: "calls", From: "n1", To: "n2"},
		},
	}

	data, _ := json.MarshalIndent(corpus, "", "  ")
	langDir := filepath.Join(dir, "go")
	os.MkdirAll(langDir, 0o755)
	os.WriteFile(filepath.Join(langDir, "sample.golden.json"), data, 0o644)

	loaded, err := LoadGoldenDir(dir)
	if err != nil {
		t.Fatalf("LoadGoldenDir: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("got %d corpora, want 1", len(loaded))
	}
	if loaded[0].Language != "go" {
		t.Errorf("language: got %s, want go", loaded[0].Language)
	}
	if len(loaded[0].Nodes) != 1 {
		t.Errorf("nodes: got %d, want 1", len(loaded[0].Nodes))
	}
}

func TestNormalizeNodes(t *testing.T) {
	nodes := []EvalNode{
		{ID: "n1", Kind: "function", Name: "Hello", File: "sample.go", StartLine: 3},
		{ID: "n2", Kind: "class", Name: "World", File: "sample.go", StartLine: 10},
	}
	keys := NodeKeys(nodes)
	if len(keys) != 2 {
		t.Fatalf("got %d keys, want 2", len(keys))
	}
	if keys[0] != "function:Hello@sample.go" {
		t.Errorf("key[0]: got %s", keys[0])
	}
}

func TestCompareCorpus(t *testing.T) {
	expected := GoldenCorpus{
		Language: "go",
		File:     "sample.go",
		Nodes: []EvalNode{
			{ID: "n1", Kind: "function", Name: "Hello", File: "sample.go", StartLine: 3},
			{ID: "n2", Kind: "function", Name: "World", File: "sample.go", StartLine: 10},
		},
		Edges: []EvalEdge{
			{Kind: "calls", From: "n1", To: "n2"},
		},
	}

	actual := GoldenCorpus{
		Language: "go",
		File:     "sample.go",
		Nodes: []EvalNode{
			{ID: "n1", Kind: "function", Name: "Hello", File: "sample.go", StartLine: 3},
			{ID: "n3", Kind: "function", Name: "Extra", File: "sample.go", StartLine: 20},
		},
		Edges: []EvalEdge{
			{Kind: "calls", From: "n1", To: "n2"},
		},
	}

	report := CompareCorpus(expected, actual)
	if report.NodeMetrics.TruePositive != 1 {
		t.Errorf("node TP: got %d, want 1", report.NodeMetrics.TruePositive)
	}
	if report.NodeMetrics.FalsePositive != 1 {
		t.Errorf("node FP: got %d, want 1", report.NodeMetrics.FalsePositive)
	}
	if report.NodeMetrics.FalseNegative != 1 {
		t.Errorf("node FN: got %d, want 1", report.NodeMetrics.FalseNegative)
	}
	if report.EdgeMetrics.TruePositive != 1 {
		t.Errorf("edge TP: got %d, want 1", report.EdgeMetrics.TruePositive)
	}
}

func TestUpdateGolden(t *testing.T) {
	dir := t.TempDir()
	langDir := filepath.Join(dir, "go")
	os.MkdirAll(langDir, 0o755)

	corpus := GoldenCorpus{
		Language: "go",
		File:     "sample.go",
		Nodes: []EvalNode{
			{ID: "n1", Kind: "function", Name: "Hello", File: "sample.go", StartLine: 3},
		},
	}

	outPath := filepath.Join(langDir, "sample.golden.json")
	err := WriteGolden(outPath, corpus)
	if err != nil {
		t.Fatalf("WriteGolden: %v", err)
	}

	loaded, err := LoadGoldenDir(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Nodes[0].Name != "Hello" {
		t.Errorf("round-trip failed: %+v", loaded)
	}
}
