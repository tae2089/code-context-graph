package benchmark_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tae2089/code-context-graph/internal/benchmark"
)

// sampleJSONL returns lines representing a minimal Claude Code session JSONL.
// It includes START/END markers inside tool_result content.
func writeSampleJSONL(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	var content string
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseJSONL_ValidSession(t *testing.T) {
	path := writeSampleJSONL(t, []string{
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"world"}],"usage":{"input_tokens":10,"output_tokens":5}}}`,
	})
	msgs, err := benchmark.ParseJSONL(path)
	if err != nil {
		t.Fatalf("ParseJSONL: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("got %d messages, want 2", len(msgs))
	}
}

func TestParseJSONL_EmptyFile(t *testing.T) {
	path := writeSampleJSONL(t, nil)
	msgs, err := benchmark.ParseJSONL(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages, got %d", len(msgs))
	}
}

func TestParseJSONL_InvalidLine(t *testing.T) {
	path := writeSampleJSONL(t, []string{
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}`,
		`NOT VALID JSON {{{`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}`,
	})
	msgs, err := benchmark.ParseJSONL(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 {
		t.Errorf("expected 2 valid messages (invalid skipped), got %d", len(msgs))
	}
}

// markerLine produces a tool_result line containing a benchmark marker.
func markerLine(markerText string) string {
	return `{"type":"tool_result","tool_use_id":"x","content":"` + markerText + `"}`
}

func TestExtractQuerySegments_TwoMarkers(t *testing.T) {
	path := writeSampleJSONL(t, []string{
		markerLine(`===BENCHMARK_QUERY_START id=q1===`),
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"answer1"}]}}`,
		markerLine(`===BENCHMARK_QUERY_END id=q1===`),
		markerLine(`===BENCHMARK_QUERY_START id=q2===`),
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"answer2"}]}}`,
		markerLine(`===BENCHMARK_QUERY_END id=q2===`),
	})
	msgs, _ := benchmark.ParseJSONL(path)
	segs, err := benchmark.ExtractQuerySegments(msgs)
	if err != nil {
		t.Fatalf("ExtractQuerySegments: %v", err)
	}
	if len(segs) != 2 {
		t.Errorf("expected 2 segments, got %d", len(segs))
	}
	if segs[0].QueryID != "q1" {
		t.Errorf("seg[0].QueryID: got %q, want q1", segs[0].QueryID)
	}
	if segs[1].QueryID != "q2" {
		t.Errorf("seg[1].QueryID: got %q, want q2", segs[1].QueryID)
	}
}

func TestExtractQuerySegments_NoMarkers(t *testing.T) {
	path := writeSampleJSONL(t, []string{
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`,
	})
	msgs, _ := benchmark.ParseJSONL(path)
	segs, err := benchmark.ExtractQuerySegments(msgs)
	if err != nil {
		t.Fatalf("ExtractQuerySegments: %v", err)
	}
	if len(segs) != 0 {
		t.Errorf("expected 0 segments, got %d", len(segs))
	}
}

func TestExtractQuerySegments_UnclosedMarker(t *testing.T) {
	path := writeSampleJSONL(t, []string{
		markerLine(`===BENCHMARK_QUERY_START id=q1===`),
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"partial answer"}]}}`,
	})
	msgs, _ := benchmark.ParseJSONL(path)
	segs, err := benchmark.ExtractQuerySegments(msgs)
	if err != nil {
		t.Fatalf("ExtractQuerySegments: %v", err)
	}
	if len(segs) != 1 {
		t.Errorf("expected 1 open segment, got %d", len(segs))
	}
	if segs[0].QueryID != "q1" {
		t.Errorf("seg.QueryID: got %q, want q1", segs[0].QueryID)
	}
}

func TestExtractToolCalls_FromSegment(t *testing.T) {
	path := writeSampleJSONL(t, []string{
		markerLine(`===BENCHMARK_QUERY_START id=q1===`),
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"mcp__ccg__search","input":{"query":"retry"}}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"internal/foo.go"}}]}}`,
		markerLine(`===BENCHMARK_QUERY_END id=q1===`),
	})
	msgs, _ := benchmark.ParseJSONL(path)
	segs, _ := benchmark.ExtractQuerySegments(msgs)
	if len(segs) == 0 {
		t.Fatal("no segments")
	}
	calls := benchmark.ExtractToolCalls(segs[0])
	if len(calls) != 2 {
		t.Errorf("expected 2 tool calls, got %d", len(calls))
	}
	if calls[0].Tool != "mcp__ccg__search" {
		t.Errorf("calls[0].Tool: got %q", calls[0].Tool)
	}
}

func TestExtractFilesRead_FromSegment(t *testing.T) {
	path := writeSampleJSONL(t, []string{
		markerLine(`===BENCHMARK_QUERY_START id=q1===`),
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"internal/webhook/handler.go"}}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Glob","input":{"pattern":"internal/**/*.go"}}]}}`,
		markerLine(`===BENCHMARK_QUERY_END id=q1===`),
	})
	msgs, _ := benchmark.ParseJSONL(path)
	segs, _ := benchmark.ExtractQuerySegments(msgs)
	files := benchmark.ExtractFilesRead(segs[0])
	if len(files) != 1 {
		t.Errorf("expected 1 file (only Read, not Glob), got %d: %v", len(files), files)
	}
	if files[0] != "internal/webhook/handler.go" {
		t.Errorf("files[0]: got %q", files[0])
	}
}

func TestExtractTokens_FromSegment(t *testing.T) {
	path := writeSampleJSONL(t, []string{
		markerLine(`===BENCHMARK_QUERY_START id=q1===`),
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"first"}],"usage":{"input_tokens":100,"output_tokens":20}}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"second"}],"usage":{"input_tokens":50,"output_tokens":10}}}`,
		markerLine(`===BENCHMARK_QUERY_END id=q1===`),
	})
	msgs, _ := benchmark.ParseJSONL(path)
	segs, _ := benchmark.ExtractQuerySegments(msgs)
	in, out := benchmark.ExtractTokens(segs[0])
	if in != 150 {
		t.Errorf("InputTokens: got %d, want 150", in)
	}
	if out != 30 {
		t.Errorf("OutputTokens: got %d, want 30", out)
	}
}

func TestExtractAnswer_FromSegment(t *testing.T) {
	path := writeSampleJSONL(t, []string{
		markerLine(`===BENCHMARK_QUERY_START id=q1===`),
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"first message"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"final answer"}]}}`,
		markerLine(`===BENCHMARK_QUERY_END id=q1===`),
	})
	msgs, _ := benchmark.ParseJSONL(path)
	segs, _ := benchmark.ExtractQuerySegments(msgs)
	answer := benchmark.ExtractAnswer(segs[0])
	if answer != "final answer" {
		t.Errorf("answer: got %q, want %q", answer, "final answer")
	}
}
