package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tae2089/code-context-graph/internal/cli"
)

// runBenchmarkCmd is a helper that runs `ccg benchmark <args>` with a fresh Deps.
func runBenchmarkCmd(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	var buf bytes.Buffer
	deps := &cli.Deps{}
	root := cli.NewRootCmd(deps)
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"benchmark"}, args...))
	err = root.Execute()
	return buf.String(), err
}

func TestBenchmarkCmd_HasSubcommands(t *testing.T) {
	var buf bytes.Buffer
	deps := &cli.Deps{}
	root := cli.NewRootCmd(deps)
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"benchmark", "--help"})
	_ = root.Execute()
	out := buf.String()
	for _, sub := range []string{"init", "validate", "run", "analyze", "compare", "report"} {
		if !strings.Contains(out, sub) {
			t.Errorf("help output missing subcommand %q:\n%s", sub, out)
		}
	}
}

func TestBenchmarkInitCmd_CreatesDir(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, "corpus")
	_, err := runBenchmarkCmd(t, "init", "--out", outDir)
	if err != nil {
		t.Fatalf("benchmark init: %v", err)
	}
	queriesPath := filepath.Join(outDir, "queries.yaml")
	if _, err := os.Stat(queriesPath); os.IsNotExist(err) {
		t.Errorf("queries.yaml not created at %s", queriesPath)
	}
}

func TestBenchmarkValidateCmd_ValidCorpus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "queries.yaml")
	content := `queries:
  - id: q1
    description: "valid query"
`
	_ = os.WriteFile(path, []byte(content), 0o644)
	_, err := runBenchmarkCmd(t, "validate", "--corpus", path)
	if err != nil {
		t.Errorf("expected success for valid corpus, got: %v", err)
	}
}

func TestBenchmarkValidateCmd_InvalidCorpus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "queries.yaml")
	content := `queries:
  - id: q1
    description: "first"
  - id: q1
    description: "duplicate"
`
	_ = os.WriteFile(path, []byte(content), 0o644)
	_, err := runBenchmarkCmd(t, "validate", "--corpus", path)
	if err == nil {
		t.Error("expected error for duplicate ID corpus")
	}
}

func TestBenchmarkAnalyzeCmd_MissingSession(t *testing.T) {
	_, err := runBenchmarkCmd(t, "analyze")
	if err == nil {
		t.Error("expected error when --session flag is missing")
	}
}

func TestBenchmarkAnalyzeCmd_ReadsJSONL(t *testing.T) {
	dir := t.TempDir()
	// Write minimal JSONL with markers
	jsonlContent := `{"type":"tool_result","tool_use_id":"x","content":"===BENCHMARK_QUERY_START id=q1==="}` + "\n" +
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"answer here"}],"usage":{"input_tokens":10,"output_tokens":5}}}` + "\n" +
		`{"type":"tool_result","tool_use_id":"y","content":"===BENCHMARK_QUERY_END id=q1==="}` + "\n"
	sessionPath := filepath.Join(dir, "session.jsonl")
	_ = os.WriteFile(sessionPath, []byte(jsonlContent), 0o644)

	out, err := runBenchmarkCmd(t, "analyze", "--session", sessionPath)
	if err != nil {
		t.Fatalf("benchmark analyze: %v", err)
	}
	// Output should be valid JSON containing query results
	var result interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &result); err != nil {
		t.Errorf("output is not valid JSON: %v\noutput: %s", err, out)
	}
}

func TestBenchmarkCompareCmd_TwoFiles(t *testing.T) {
	dir := t.TempDir()

	writeRun := func(name, mode string) string {
		path := filepath.Join(dir, name)
		data, _ := json.Marshal(map[string]interface{}{
			"mode":    mode,
			"run_at":  "2026-01-01T00:00:00Z",
			"results": []interface{}{},
		})
		_ = os.WriteFile(path, data, 0o644)
		return path
	}

	withPath := writeRun("with.json", "with-ccg")
	withoutPath := writeRun("without.json", "without-ccg")

	out, err := runBenchmarkCmd(t, "compare", "--with", withPath, "--without", withoutPath)
	if err != nil {
		t.Fatalf("benchmark compare: %v", err)
	}
	var result interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &result); err != nil {
		t.Errorf("compare output is not valid JSON: %v\noutput: %s", err, out)
	}
}

func TestBenchmarkReportCmd_GeneratesMarkdown(t *testing.T) {
	dir := t.TempDir()
	compPath := filepath.Join(dir, "comparison.json")
	outPath := filepath.Join(dir, "report.md")

	compData, _ := json.Marshal(map[string]interface{}{
		"with_ccg": map[string]interface{}{
			"mode":    "with-ccg",
			"run_at":  "2026-01-01T00:00:00Z",
			"results": []interface{}{},
		},
		"matches": []interface{}{},
	})
	_ = os.WriteFile(compPath, compData, 0o644)

	_, err := runBenchmarkCmd(t, "report", "--comparison", compPath, "--out", outPath)
	if err != nil {
		t.Fatalf("benchmark report: %v", err)
	}
	if _, err := os.Stat(outPath); os.IsNotExist(err) {
		t.Error("report.md was not created")
	}
}

func TestBenchmarkRunCmd_RequiresCWD(t *testing.T) {
	dir := t.TempDir()
	corpusPath := filepath.Join(dir, "queries.yaml")
	_ = os.WriteFile(corpusPath, []byte("queries:\n  - id: q1\n    description: test\n"), 0o644)
	// Run without --cwd should fail
	_, err := runBenchmarkCmd(t, "run", "--corpus", corpusPath)
	if err == nil {
		t.Error("expected error when --cwd is missing")
	}
}
