package mcp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// seedIntentNodes puts one node with a recorded reason and one without into the
// graph, then rebuilds the derived search documents and both indexes.
func seedIntentNodes(t *testing.T, deps *Deps) (reasoned graph.Node) {
	t.Helper()
	db := testDBFor(deps)
	reasoned = graph.Node{QualifiedName: "webhook.handle", Kind: graph.NodeKindFunction, Name: "handle", FilePath: "webhook/handle.go", StartLine: 12, Language: "go"}
	namesake := graph.Node{QualifiedName: "util.signatureFormatter", Kind: graph.NodeKindFunction, Name: "signatureFormatter", FilePath: "util/format.go", StartLine: 3, Language: "go"}
	for _, node := range []*graph.Node{&reasoned, &namesake} {
		if err := db.Create(node).Error; err != nil {
			t.Fatalf("seed node %s: %v", node.Name, err)
		}
	}
	annotation := graph.Annotation{NodeID: reasoned.ID, Summary: "handle processes an incoming push."}
	if err := db.Create(&annotation).Error; err != nil {
		t.Fatalf("seed annotation: %v", err)
	}
	tag := graph.DocTag{AnnotationID: annotation.ID, Kind: graph.TagIntent, Value: "verify the signature so a push from anywhere else is rejected"}
	if err := db.Create(&tag).Error; err != nil {
		t.Fatalf("seed tag: %v", err)
	}
	if _, err := deps.Build.Maintenance.RefreshDocuments(t.Context()); err != nil {
		t.Fatalf("RefreshDocuments: %v", err)
	}
	if err := deps.Build.Maintenance.RebuildIndex(t.Context()); err != nil {
		t.Fatalf("RebuildIndex: %v", err)
	}
	return reasoned
}

func callFindByIntent(t *testing.T, deps *Deps, args map[string]any) string {
	t.Helper()
	h := &handlers{deps: deps}
	request := mcp.CallToolRequest{}
	request.Params.Arguments = args
	result, err := h.findByIntent(t.Context(), request)
	if err != nil {
		t.Fatalf("findByIntent: %v", err)
	}
	return resultTextOf(t, result)
}

// The whole point of the tool: a question in plain words comes back with a file
// to open and a node id to walk the graph from. If the answer had only a file,
// the caller would have to search again to get anywhere.
func TestFindByIntent_AnswersAQuestionWithFilesAndNodeIDs(t *testing.T) {
	deps := setupTestDeps(t)
	reasoned := seedIntentNodes(t, deps)

	text := callFindByIntent(t, deps, map[string]any{"question": "why do we verify the signature on a push"})

	var answer struct {
		Files []struct {
			FilePath string `json:"file_path"`
			Entries  []struct {
				NodeID uint   `json:"node_id"`
				Reason string `json:"reason"`
			} `json:"entries"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(text), &answer); err != nil {
		t.Fatalf("unmarshal %q: %v", text, err)
	}
	if len(answer.Files) != 1 {
		t.Fatalf("got %d files, want 1: %s", len(answer.Files), text)
	}
	if answer.Files[0].FilePath != "webhook/handle.go" {
		t.Errorf("file = %q, want webhook/handle.go", answer.Files[0].FilePath)
	}
	if len(answer.Files[0].Entries) != 1 || answer.Files[0].Entries[0].NodeID != reasoned.ID {
		t.Fatalf("entries = %+v, want one entry for node %d", answer.Files[0].Entries, reasoned.ID)
	}
	if !strings.Contains(answer.Files[0].Entries[0].Reason, "rejected") {
		t.Errorf("reason = %q, want the recorded @intent", answer.Files[0].Entries[0].Reason)
	}
}

// An answer nobody can weigh is not much of an answer. The wire payload carries
// which words of the question matched each declaration, and how many recorded
// reasons in this codebase hold each of those words, so a caller can see that a
// long file list rests on one word everybody writes. It is evidence rather than
// a score on purpose: a score would have to be calibrated on some codebase, and
// these counts are recounted against whichever one is being searched.
func TestFindByIntent_ReportsWhatMatchedAndHowCommonItIs(t *testing.T) {
	deps := setupTestDeps(t)
	seedIntentNodes(t, deps)

	text := callFindByIntent(t, deps, map[string]any{"question": "why do we verify the signature on a push"})

	var answer struct {
		Files []struct {
			Entries []struct {
				MatchedTerms []string `json:"matched_terms"`
			} `json:"entries"`
		} `json:"files"`
		Terms []struct {
			Text      string `json:"text"`
			InReasons int    `json:"in_reasons"`
		} `json:"terms"`
		ReasonsSearched int `json:"reasons_searched"`
	}
	if err := json.Unmarshal([]byte(text), &answer); err != nil {
		t.Fatalf("unmarshal %q: %v", text, err)
	}
	if len(answer.Files) != 1 || len(answer.Files[0].Entries) != 1 {
		t.Fatalf("want one entry to carry evidence: %s", text)
	}
	matched := answer.Files[0].Entries[0].MatchedTerms
	if !slices.Contains(matched, "signature") || !slices.Contains(matched, "verify") {
		t.Errorf("matched terms = %v, want the words the recorded reason actually holds", matched)
	}
	byTerm := map[string]int{}
	for _, term := range answer.Terms {
		byTerm[term.Text] = term.InReasons
	}
	if byTerm["signature"] != 1 {
		t.Errorf("signature is reported in %d reasons, want 1: %s", byTerm["signature"], text)
	}
	if _, ok := byTerm["scheduler"]; ok {
		t.Errorf("a word the question never used is reported: %s", text)
	}
	if answer.ReasonsSearched != 1 {
		t.Errorf("reasons searched = %d, want the 1 recorded reason in this fixture", answer.ReasonsSearched)
	}
}

// An empty answer must say how much of the code had a reason recorded at all,
// otherwise the caller cannot tell a real "no" from an unannotated codebase.
func TestFindByIntent_EmptyAnswerCarriesCoverage(t *testing.T) {
	deps := setupTestDeps(t)
	seedIntentNodes(t, deps)

	text := callFindByIntent(t, deps, map[string]any{"question": "how does the scheduler pick a leader"})

	var answer struct {
		Files    []any `json:"files"`
		Coverage struct {
			NodesWithReason int `json:"nodes_with_reason"`
			NodesTotal      int `json:"nodes_total"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal([]byte(text), &answer); err != nil {
		t.Fatalf("unmarshal %q: %v", text, err)
	}
	if len(answer.Files) != 0 {
		t.Fatalf("got %d files, want none: %s", len(answer.Files), text)
	}
	if answer.Coverage.NodesTotal != 2 || answer.Coverage.NodesWithReason != 1 {
		t.Errorf("coverage = %+v, want 1/2", answer.Coverage)
	}
}

func TestFindByIntent_RejectsAnEmptyQuestion(t *testing.T) {
	deps := setupTestDeps(t)
	h := &handlers{deps: deps}
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"question": "   "}

	result, err := h.findByIntent(t.Context(), request)
	if err != nil {
		t.Fatalf("findByIntent: %v", err)
	}
	if !result.IsError {
		t.Fatal("an empty question was accepted")
	}
}

func TestFindByIntent_ReportsWhenNotConfigured(t *testing.T) {
	deps := setupTestDeps(t)
	deps.Graph.Intent = nil
	h := &handlers{deps: deps}
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"question": "why do we verify the signature"}

	result, err := h.findByIntent(t.Context(), request)
	if err != nil {
		t.Fatalf("findByIntent: %v", err)
	}
	if !result.IsError {
		t.Fatal("an unconfigured intent service answered anyway")
	}
}
