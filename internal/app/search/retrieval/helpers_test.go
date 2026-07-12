package retrieval_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/tae2089/code-context-graph/internal/app/search/retrieval"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

func TestMatchedTerms_basic(t *testing.T) {
	terms := retrieval.MatchedTerms("Hello World")
	if len(terms) != 4 {
		t.Fatalf("expected 4 terms including identifier aliases, got %d: %v", len(terms), terms)
	}
	if terms[0] != "hello" || terms[1] != "world" {
		t.Errorf("unexpected terms: %v", terms)
	}
}

func TestMatchedTerms_deduplication(t *testing.T) {
	terms := retrieval.MatchedTerms("foo foo bar")
	if len(terms) != 4 {
		t.Fatalf("expected 4 unique terms including identifier aliases, got %d: %v", len(terms), terms)
	}
}

func TestMatchedTerms_punctuationStripped(t *testing.T) {
	terms := retrieval.MatchedTerms(`"hello", (world)!`)
	for _, term := range terms {
		if strings.Contains(term, "_") {
			continue
		}
		for _, ch := range `"'()[]{}.,:;!?` {
			if len(term) > 0 && (rune(term[0]) == ch || rune(term[len(term)-1]) == ch) {
				t.Errorf("term %q still has punctuation", term)
			}
		}
	}
}

func TestMatchedTerms_identifierAliases(t *testing.T) {
	terms := retrieval.MatchedTerms("retrieve docs matched fields")
	for _, want := range []string{"retrieve_docs", "retrievedocs", "matched_fields", "matchedfields"} {
		if !slices.Contains(terms, want) {
			t.Fatalf("terms missing %q: %v", want, terms)
		}
	}
}

func TestMatchedTerms_referenceAliases(t *testing.T) {
	terms := retrieval.MatchedTerms("see annotation cross namespace reference resolve")
	for _, want := range []string{"cross_namespace", "crossnamespace", "ref", "refs"} {
		if !slices.Contains(terms, want) {
			t.Fatalf("terms missing %q: %v", want, terms)
		}
	}
	for _, notWant := range []string{"annotations", "namespaces", "resolved", "resolver", "resolution"} {
		if slices.Contains(terms, notWant) {
			t.Fatalf("terms should not include broad morphology alias %q: %v", notWant, terms)
		}
	}
}

func TestTextContainsAnyTerm_collapsesIdentifierSeparators(t *testing.T) {
	terms := retrieval.MatchedTerms("cross namespace")
	if !retrieval.TextContainsAnyTerm("cross-namespace annotation links", terms) {
		t.Fatalf("expected cross namespace query to match hyphenated evidence; terms=%v", terms)
	}
}

func TestMatchedTerms_empty(t *testing.T) {
	terms := retrieval.MatchedTerms("")
	if len(terms) != 0 {
		t.Errorf("expected empty, got %v", terms)
	}
}

func TestDBCandidateLimit_floor(t *testing.T) {
	got := retrieval.DBCandidateLimit(1)
	if got < 50 {
		t.Errorf("expected >= 50 (floor), got %d", got)
	}
}

func TestDBCandidateLimit_cap(t *testing.T) {
	got := retrieval.DBCandidateLimit(1000)
	if got > 500 {
		t.Errorf("expected <= 500 (cap), got %d", got)
	}
}

func TestDBCandidateLimit_normal(t *testing.T) {
	got := retrieval.DBCandidateLimit(10)
	if got != 100 {
		t.Errorf("expected 100 (10*10), got %d", got)
	}
}

func TestGroupCandidatesByFile_order(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Kind: graph.NodeKindFunction, FilePath: "a/b.go", Name: "Foo"},
		{ID: 2, Kind: graph.NodeKindFunction, FilePath: "c/d.go", Name: "Bar"},
		{ID: 3, Kind: graph.NodeKindFunction, FilePath: "a/b.go", Name: "Baz"},
	}
	groups, nodeIDs := retrieval.GroupCandidatesByFile(nodes, 10)
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].FilePath != "a/b.go" {
		t.Errorf("first group should be a/b.go, got %s", groups[0].FilePath)
	}
	if len(groups[0].Nodes) != 2 {
		t.Errorf("first group should have 2 nodes, got %d", len(groups[0].Nodes))
	}
	if len(nodeIDs) != 3 {
		t.Errorf("expected 3 nodeIDs, got %d", len(nodeIDs))
	}
}

func TestGroupCandidatesByFile_limit(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Kind: graph.NodeKindFunction, FilePath: "a.go", Name: "A"},
		{ID: 2, Kind: graph.NodeKindFunction, FilePath: "b.go", Name: "B"},
		{ID: 3, Kind: graph.NodeKindFunction, FilePath: "c.go", Name: "C"},
	}
	groups, _ := retrieval.GroupCandidatesByFile(nodes, 2)
	if len(groups) != 2 {
		t.Errorf("expected limit of 2 groups, got %d", len(groups))
	}
}

func TestGroupCandidatesByFile_emptyFilePath(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Kind: graph.NodeKindFunction, FilePath: "", Name: "NoPath"},
		{ID: 2, Kind: graph.NodeKindFunction, FilePath: "a.go", Name: "A"},
	}
	groups, _ := retrieval.GroupCandidatesByFile(nodes, 10)
	if len(groups) != 1 {
		t.Errorf("expected 1 group (empty path skipped), got %d", len(groups))
	}
}

func TestGroupCandidatesByFile_skipsPackageNodes(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Kind: graph.NodeKindPackage, FilePath: "internal/mcp", Name: "mcp"},
		{ID: 2, Kind: graph.NodeKindFunction, FilePath: "internal/mcp/handler.go", Name: "Handler"},
	}
	groups, nodeIDs := retrieval.GroupCandidatesByFile(nodes, 10)
	if len(groups) != 1 {
		t.Fatalf("expected one retrievable group, got %d", len(groups))
	}
	if groups[0].FilePath != "internal/mcp/handler.go" {
		t.Fatalf("unexpected group path %q", groups[0].FilePath)
	}
	if len(nodeIDs) != 1 || nodeIDs[0] != 2 {
		t.Fatalf("expected only function node ID, got %v", nodeIDs)
	}
}

func TestDBSummary_annotationSummaryFirst(t *testing.T) {
	ann := &graph.Annotation{NodeID: 1, Summary: "my summary"}
	nodes := []graph.Node{{ID: 1, Name: "Foo"}}
	annotations := map[uint]*graph.Annotation{1: ann}
	got := retrieval.DBSummary(nodes, annotations)
	if got != "my summary" {
		t.Errorf("expected 'my summary', got %q", got)
	}
}

func TestDBSummary_fallbackToTagValue(t *testing.T) {
	ann := &graph.Annotation{
		NodeID:  1,
		Summary: "",
		Tags:    []graph.DocTag{{Kind: graph.TagIntent, Value: "tag value"}},
	}
	nodes := []graph.Node{{ID: 1, Name: "Foo"}}
	annotations := map[uint]*graph.Annotation{1: ann}
	got := retrieval.DBSummary(nodes, annotations)
	if got != "tag value" {
		t.Errorf("expected 'tag value', got %q", got)
	}
}

func TestDBSummary_noAnnotation(t *testing.T) {
	nodes := []graph.Node{{ID: 1, Name: "Foo"}}
	annotations := map[uint]*graph.Annotation{}
	got := retrieval.DBSummary(nodes, annotations)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestDBPath_basic(t *testing.T) {
	path := retrieval.DBPath("internal/mcp/handler.go")
	if len(path) < 2 {
		t.Fatalf("expected at least 2 segments, got %v", path)
	}
	if path[0] != "docs" {
		t.Errorf("first segment should be 'docs', got %q", path[0])
	}
	if path[len(path)-1] != "handler.go" {
		t.Errorf("last segment should be 'handler.go', got %q", path[len(path)-1])
	}
}

func TestDBPath_noEmptySegments(t *testing.T) {
	path := retrieval.DBPath("a//b.go")
	for _, seg := range path {
		if seg == "" {
			t.Errorf("path contains empty segment: %v", path)
		}
	}
}

func TestBuildDBResult_fieldsDefault(t *testing.T) {
	group := retrieval.DBFileGroup{
		FilePath: "pkg/foo.go",
		Nodes:    []graph.Node{{ID: 1, Kind: graph.NodeKindFunction, Name: "Foo", FilePath: "pkg/foo.go", QualifiedName: "pkg.Foo"}},
	}
	annotations := map[uint]*graph.Annotation{}
	terms := []string{"unmatched"}
	result := retrieval.BuildDBResult(group, annotations, terms, 0)
	if result.ID != "file:pkg/foo.go" {
		t.Errorf("unexpected ID: %s", result.ID)
	}
	if result.Kind != "file" {
		t.Errorf("unexpected Kind: %s", result.Kind)
	}
	if len(result.MatchedFields) == 0 || result.MatchedFields[0] != "search" {
		t.Errorf("expected default field 'search', got %v", result.MatchedFields)
	}
}

func TestBuildDBResult_scoreUsesStructuredEvidence(t *testing.T) {
	group := retrieval.DBFileGroup{
		FilePath: "a.go",
		Nodes:    []graph.Node{{ID: 1, Kind: graph.NodeKindFunction, Name: "Auth", FilePath: "a.go", QualifiedName: "pkg.Auth"}},
	}
	result := retrieval.BuildDBResult(group, nil, []string{"auth"}, 0)
	if result.Score != 26 {
		t.Errorf("score = %d, want 26 (12 label + 4 qualified_name + 10 distinct term bonus)", result.Score)
	}
}

func TestBuildDBResult_scoreDoesNotExposeResponseOrderTieBreaker(t *testing.T) {
	first := retrieval.DBFileGroup{
		FilePath: "a.go",
		Nodes:    []graph.Node{{ID: 1, Kind: graph.NodeKindFunction, Name: "A", FilePath: "a.go"}},
	}
	second := retrieval.DBFileGroup{
		FilePath: "b.go",
		Nodes: []graph.Node{
			{ID: 2, Kind: graph.NodeKindFunction, Name: "B", FilePath: "b.go"},
			{ID: 3, Kind: graph.NodeKindFunction, Name: "C", FilePath: "b.go"},
		},
	}
	firstResult := retrieval.BuildDBResult(first, nil, nil, 0)
	secondResult := retrieval.BuildDBResult(second, nil, nil, 1)
	if firstResult.Score != secondResult.Score {
		t.Fatalf("score should expose relevance only, got first=%d second=%d", firstResult.Score, secondResult.Score)
	}
}

func TestDBMatches_deduplication(t *testing.T) {
	nodes := []graph.Node{
		{ID: 1, Kind: graph.NodeKindFunction, Name: "Foo", FilePath: "a.go", QualifiedName: "pkg.Foo"},
		{ID: 1, Kind: graph.NodeKindFunction, Name: "Foo", FilePath: "a.go", QualifiedName: "pkg.Foo"},
	}
	matches := retrieval.DBMatches(nodes, nil)
	if len(matches) != 1 {
		t.Errorf("expected 1 deduplicated match, got %d", len(matches))
	}
}

func TestDBMatches_empty(t *testing.T) {
	matches := retrieval.DBMatches(nil, nil)
	if matches != nil {
		t.Errorf("expected nil for empty input, got %v", matches)
	}
}

func TestDBMatches_annotationSummaryPreferred(t *testing.T) {
	ann := &graph.Annotation{NodeID: 1, Summary: "annotated summary"}
	nodes := []graph.Node{{ID: 1, Kind: graph.NodeKindFunction, Name: "Foo", FilePath: "a.go", QualifiedName: "pkg.Foo", Annotation: ann}}
	matches := retrieval.DBMatches(nodes, nil)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Summary != "annotated summary" {
		t.Errorf("expected annotation summary, got %q", matches[0].Summary)
	}
}

func TestDBMatches_batchAnnotationSummaryPreferred(t *testing.T) {
	nodes := []graph.Node{{ID: 1, Kind: graph.NodeKindFunction, Name: "Foo", FilePath: "a.go", QualifiedName: "pkg.Foo"}}
	annotations := map[uint]*graph.Annotation{1: {NodeID: 1, Summary: "batch summary"}}
	matches := retrieval.DBMatches(nodes, annotations)
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].Summary != "batch summary" {
		t.Fatalf("expected batch annotation summary, got %q", matches[0].Summary)
	}
}

func TestTextContainsAnyTerm_match(t *testing.T) {
	if !retrieval.TextContainsAnyTerm("Hello World", []string{"world"}) {
		t.Error("expected match for 'world' in 'Hello World'")
	}
}

func TestTextContainsAnyTerm_noMatch(t *testing.T) {
	if retrieval.TextContainsAnyTerm("Hello World", []string{"foo"}) {
		t.Error("expected no match for 'foo' in 'Hello World'")
	}
}

func TestTextContainsAnyTerm_emptyText(t *testing.T) {
	if retrieval.TextContainsAnyTerm("", []string{"foo"}) {
		t.Error("expected no match for empty text")
	}
}

func TestNodeMatchesTerms_nameMatch(t *testing.T) {
	node := graph.Node{Name: "AuthHandler", QualifiedName: "pkg.AuthHandler", FilePath: "auth.go"}
	if !retrieval.NodeMatchesTerms(node, []string{"auth"}) {
		t.Error("expected node to match term 'auth'")
	}
}

func TestNodeMatchesTerms_annotationMatch(t *testing.T) {
	ann := &graph.Annotation{Summary: "handles payment processing"}
	node := graph.Node{Name: "Foo", QualifiedName: "pkg.Foo", FilePath: "foo.go", Annotation: ann}
	if !retrieval.NodeMatchesTerms(node, []string{"payment"}) {
		t.Error("expected node to match via annotation summary")
	}
}

func TestNodeMatchesTerms_noMatch(t *testing.T) {
	node := graph.Node{Name: "Foo", QualifiedName: "pkg.Foo", FilePath: "foo.go"}
	if retrieval.NodeMatchesTerms(node, []string{"unrelated"}) {
		t.Error("expected no match")
	}
}
