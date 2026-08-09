package searchsql

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	retrievalapp "github.com/tae2089/code-context-graph/internal/app/search/retrieval"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// TestCaptureDocsGoldenCandidates freezes the two inputs `wiki_search` reads, so
// the golden set can score that tool without opening a database.
//
// `search` needs one fixture — a ranked candidate list. `wiki_search` needs two,
// because it has two sources: the same full-text query, and a whole-namespace
// scan it falls back to when full-text underfills the page. The scan is not a
// detail to skip: every question-shaped query returns nothing from full-text, so
// the scan is the only thing standing between the reader and an empty answer,
// and a fixture without it would score a code path production never takes.
//
// It is skipped unless -capture-docs is passed, and it needs a built graph at
// the repository root that the test cannot create.
//
//	go test -tags fts5 ./internal/adapters/outbound/searchsql/ \
//	  -run TestCaptureDocsGoldenCandidates -capture-docs -count=1 -v
func TestCaptureDocsGoldenCandidates(t *testing.T) {
	if !*captureDocs {
		t.Skip("pass -capture-docs to rewrite the wiki_search golden fixture")
	}
	var set struct {
		Corpus struct {
			Namespace string `json:"namespace"`
		} `json:"corpus"`
		Queries []struct {
			Query string `json:"query"`
		} `json:"queries"`
	}
	raw, err := os.ReadFile(goldenDir + "queries.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}

	db, err := gorm.Open(sqlite.Open("file:"+goldenGraphPath+"?immutable=1&mode=ro"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open %s: %v", goldenGraphPath, err)
	}
	reader := NewReader(db, &SQLiteBackend{})
	ctx := requestctx.WithNamespace(context.Background(), set.Corpus.Namespace)

	// The kind list is derived rather than written out, so widening what
	// retrieval considers retrievable widens this capture with it.
	kinds := make([]graph.NodeKind, 0, len(allNodeKinds))
	for _, kind := range allNodeKinds {
		if retrievalapp.IsRetrievableNodeKind(kind) {
			kinds = append(kinds, kind)
		}
	}
	scanned, err := reader.ScanCandidates(ctx, kinds, docsScanRowCap)
	if err != nil {
		t.Fatal(err)
	}
	if len(scanned) >= docsScanRowCap {
		t.Fatalf("the scan hit its %d-row ceiling, so the fixture would hold a truncated namespace", docsScanRowCap)
	}

	fixture := docsFixture{Pool: make([]docsNode, 0, len(scanned)), FTS: map[string][]uint{}}
	for _, n := range scanned {
		fixture.Pool = append(fixture.Pool, docsNodeOf(n))
	}
	for _, q := range set.Queries {
		nodes, err := reader.Query(ctx, q.Query, retrievalapp.DBCandidateLimit(goldenLimit))
		if err != nil {
			t.Fatalf("%q: %v", q.Query, err)
		}
		ids := make([]uint, 0, len(nodes))
		for _, n := range nodes {
			ids = append(ids, n.ID)
		}
		fixture.FTS[q.Query] = ids
		t.Logf("%-40q -> %3d full-text candidates", q.Query, len(ids))
	}

	blob, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goldenDir+"docs_candidates.json", append(blob, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("docs_candidates.json rewritten: %d pooled nodes, %d queries", len(fixture.Pool), len(fixture.FTS))
}

// docsFixture is what a replay of `wiki_search` needs and nothing more.
//
// FTS stores node IDs rather than whole records because every one of them is
// already in Pool, or is of a kind retrieval drops before looking at it. Storing
// the identity twice would let the two copies disagree.
type docsFixture struct {
	Pool []docsNode        `json:"pool"`
	FTS  map[string][]uint `json:"fts"`
}

// docsNode carries every field the scan matches on and the scorer reads. It is
// wider than the ranking fixture's record because `wiki_search` scores whole
// annotations, not just the one @intent line search shows.
type docsNode struct {
	ID            uint            `json:"id"`
	Name          string          `json:"name"`
	QualifiedName string          `json:"qualified_name"`
	Kind          string          `json:"kind"`
	FilePath      string          `json:"file_path"`
	Annotation    *docsAnnotation `json:"annotation,omitempty"`
}

type docsAnnotation struct {
	Summary string       `json:"summary,omitempty"`
	Context string       `json:"context,omitempty"`
	RawText string       `json:"raw_text,omitempty"`
	Tags    []docsDocTag `json:"tags,omitempty"`
}

type docsDocTag struct {
	Kind  string `json:"kind"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

func docsNodeOf(n graph.Node) docsNode {
	out := docsNode{
		ID:            n.ID,
		Name:          n.Name,
		QualifiedName: n.QualifiedName,
		Kind:          string(n.Kind),
		FilePath:      n.FilePath,
	}
	if n.Annotation == nil {
		return out
	}
	ann := &docsAnnotation{
		Summary: n.Annotation.Summary,
		Context: n.Annotation.Context,
		RawText: n.Annotation.RawText,
	}
	for _, tag := range n.Annotation.Tags {
		ann.Tags = append(ann.Tags, docsDocTag{Kind: string(tag.Kind), Name: tag.Name, Value: tag.Value})
	}
	out.Annotation = ann
	return out
}

var captureDocs = flag.Bool("capture-docs", false, "rewrite the wiki_search golden fixture from a local graph")

// docsScanRowCap must stay equal to retrieval's own scan ceiling, or the fixture
// would hold a different slice of the namespace than production scans.
const docsScanRowCap = 5000

var allNodeKinds = []graph.NodeKind{
	graph.NodeKindFile,
	graph.NodeKindPackage,
	graph.NodeKindClass,
	graph.NodeKindFunction,
	graph.NodeKindType,
	graph.NodeKindTest,
}
