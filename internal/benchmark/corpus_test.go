package benchmark_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tae2089/code-context-graph/internal/benchmark"
)

func TestLoadCorpus_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "queries.yaml")
	content := `version: "1"
queries:
  - id: q1
    description: "결제 실패 복구 로직"
    expected_files:
      - internal/webhook/handler.go
    expected_symbols:
      - WebhookHandler
    difficulty: medium
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	corpus, err := benchmark.LoadCorpus(path)
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	if len(corpus.Queries) != 1 {
		t.Errorf("Queries count: got %d, want 1", len(corpus.Queries))
	}
	if corpus.Queries[0].ID != "q1" {
		t.Errorf("ID: got %q, want q1", corpus.Queries[0].ID)
	}
}

func TestLoadCorpus_FileNotFound(t *testing.T) {
	_, err := benchmark.LoadCorpus("/nonexistent/queries.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadCorpus_MissingID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "queries.yaml")
	content := `queries:
  - description: "no id here"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := benchmark.LoadCorpus(path)
	if err == nil {
		t.Error("expected validation error for missing id")
	}
}

func TestLoadCorpus_MissingDescription(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "queries.yaml")
	content := `queries:
  - id: q1
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := benchmark.LoadCorpus(path)
	if err == nil {
		t.Error("expected validation error for missing description")
	}
}

func TestLoadCorpus_DuplicateID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "queries.yaml")
	content := `queries:
  - id: q1
    description: "first"
  - id: q1
    description: "duplicate"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := benchmark.LoadCorpus(path)
	if err == nil {
		t.Error("expected validation error for duplicate id")
	}
}

func TestSaveCorpus_WritesYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "queries.yaml")
	corpus := &benchmark.Corpus{
		Version: "1",
		Queries: []benchmark.Query{
			{ID: "q1", Description: "테스트 쿼리", Difficulty: "easy"},
		},
	}
	if err := benchmark.SaveCorpus(path, corpus); err != nil {
		t.Fatalf("SaveCorpus: %v", err)
	}
	loaded, err := benchmark.LoadCorpus(path)
	if err != nil {
		t.Fatalf("LoadCorpus after save: %v", err)
	}
	if len(loaded.Queries) != 1 || loaded.Queries[0].ID != "q1" {
		t.Errorf("round-trip failed: got %+v", loaded)
	}
}

func TestValidateCorpus_ValidQuery(t *testing.T) {
	corpus := &benchmark.Corpus{
		Queries: []benchmark.Query{
			{ID: "q1", Description: "valid query"},
		},
	}
	if err := benchmark.ValidateCorpus(corpus); err != nil {
		t.Errorf("expected nil error for valid corpus, got: %v", err)
	}
}

func TestValidateCorpus_EmptyCorpus(t *testing.T) {
	corpus := &benchmark.Corpus{Queries: []benchmark.Query{}}
	if err := benchmark.ValidateCorpus(corpus); err == nil {
		t.Error("expected error for empty corpus")
	}
}
