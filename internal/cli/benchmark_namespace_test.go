package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tae2089/code-context-graph/internal/benchmark"
	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
)

func TestBenchmarkTokenBench_RespectsNamespace(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)

	nodeA := model.Node{Namespace: "ns-a", Name: "SharedA", QualifiedName: "pkg.SharedA", Kind: model.NodeKindFunction, FilePath: "a.go", StartLine: 1, EndLine: 1, Language: "go"}
	nodeB := model.Node{Namespace: "ns-b", Name: "SharedB", QualifiedName: "pkg.SharedB", Kind: model.NodeKindFunction, FilePath: "b.go", StartLine: 1, EndLine: 1, Language: "go"}
	if err := db.Create(&nodeA).Error; err != nil {
		t.Fatalf("create nodeA: %v", err)
	}
	if err := db.Create(&nodeB).Error; err != nil {
		t.Fatalf("create nodeB: %v", err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: "ns-a", NodeID: nodeA.ID, Content: "sharedterm alpha", Language: "go"}).Error; err != nil {
		t.Fatalf("create docA: %v", err)
	}
	if err := db.Create(&model.SearchDocument{Namespace: "ns-b", NodeID: nodeB.ID, Content: "sharedterm beta", Language: "go"}).Error; err != nil {
		t.Fatalf("create docB: %v", err)
	}
	if err := deps.SearchBackend.Rebuild(ctxns.WithNamespace(context.Background(), "ns-a"), db); err != nil {
		t.Fatalf("rebuild ns-a search: %v", err)
	}
	if err := deps.SearchBackend.Rebuild(ctxns.WithNamespace(context.Background(), "ns-b"), db); err != nil {
		t.Fatalf("rebuild ns-b search: %v", err)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "b.go"), []byte("package p\nfunc SharedB() {}\n"), 0o644); err != nil {
		t.Fatalf("write repo file: %v", err)
	}
	corpusPath := filepath.Join(t.TempDir(), "queries.yaml")
	if err := os.WriteFile(corpusPath, []byte(`queries:
  - id: q1
    description: sharedterm
    expected_symbols:
      - SharedB
`), 0o644); err != nil {
		t.Fatalf("write corpus: %v", err)
	}

	if err := executeCmd(deps, stdout, stderr, "--namespace", "ns-b", "benchmark", "token-bench", "--corpus", corpusPath, "--repo", root, "--exts", ".go"); err != nil {
		t.Fatalf("token-bench: %v\nstderr=%s", err, stderr.String())
	}

	var results []benchmark.TokenBenchResult
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("decode output: %v\nstdout=%s", err, stdout.String())
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].ResultCount == 0 || results[0].SymbolsHit != 1 {
		t.Fatalf("token-bench did not use ns-b search results: %+v", results[0])
	}
}
