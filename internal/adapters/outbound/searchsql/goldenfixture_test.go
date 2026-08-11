package searchsql

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/app/search/rank"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// TestCaptureGoldenCandidates refreshes the frozen candidate lists that the
// ranking golden set replays.
//
// It lives here, not next to the golden set, because only this package can run
// the real full-text query. Capturing through the production path is the point:
// the fixture then holds exactly what the ranker is handed in production,
// including the prefix expansion SanitizeFTS5 applies and the exact-name
// promotion that runs after the MATCH.
//
// It is skipped unless -capture-golden is passed, and it needs a built graph at
// the repository root that the test cannot create. Whoever recaptures owns
// re-reading the affected judgments, because a recapture can hide a retrieval
// regression by baking it into the fixture.
//
//	go test -tags fts5 ./internal/adapters/outbound/searchsql/ \
//	  -run TestCaptureGoldenCandidates -capture-golden -count=1
func TestCaptureGoldenCandidates(t *testing.T) {
	if !*captureGolden {
		t.Skip("pass -capture-golden to rewrite the ranking golden fixture")
	}
	dir, graphPath := captureTarget(t)
	var set struct {
		Corpus struct {
			Namespace string `json:"namespace"`
		} `json:"corpus"`
		Queries []struct {
			Query string `json:"query"`
		} `json:"queries"`
	}
	raw, err := os.ReadFile(dir + "queries.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}

	// Read-only, and immutable so SQLite never writes a WAL sidecar next to a
	// graph the test does not own.
	db, err := gorm.Open(sqlite.Open("file:"+graphPath+"?immutable=1&mode=ro"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open %s: %v", graphPath, err)
	}
	backend := &SQLiteBackend{}
	reader := NewReader(db, backend)
	ctx := requestctx.WithNamespace(context.Background(), set.Corpus.Namespace)

	out := map[string][]goldenCandidate{}
	capturer, err := newGoldenIntentCapturer(ctx, reader)
	if err != nil {
		t.Fatal(err)
	}
	outIntent := goldenIntentFixture{
		Corpus: capturer.corpus, Nodes: map[uint]goldenIntentNode{},
		Documents: map[uint]goldenIntentDocument{}, Queries: map[string][]uint{},
	}
	for _, q := range set.Queries {
		nodes, err := backend.Query(ctx, db, q.Query, rank.FetchLimit(goldenLimit))
		if err != nil {
			t.Fatalf("%q: %v", q.Query, err)
		}
		captured := make([]goldenCandidate, 0, len(nodes))
		for _, n := range nodes {
			captured = append(captured, goldenCandidate{
				ID:            n.ID,
				Name:          n.Name,
				QualifiedName: n.QualifiedName,
				Kind:          string(n.Kind),
				FilePath:      n.FilePath,
				Intent:        n.Intent(),
			})
		}
		out[q.Query] = captured
		matched, err := capturer.capture(ctx, q.Query, &outIntent)
		if err != nil {
			t.Fatalf("%q: %v", q.Query, err)
		}
		t.Logf("%-30q -> %2d candidates, %2d matched intent reasons", q.Query, len(captured), matched)
	}

	if err := validateGoldenIntentFixture(outIntent); err != nil {
		t.Fatal(err)
	}
	writeGoldenJSON(t, dir+"candidates.json", out)
	writeGoldenJSON(t, dir+"intent_candidates.json", outIntent)
	t.Log("candidates.json and intent_candidates.json rewritten; re-run the rank golden report and review every change")
}

type goldenIntentCapturer struct {
	reader    *Reader
	corpus    int
	reasonIDs map[string][]uint
}

// newGoldenIntentCapturer indexes the corpus's persisted reason IDs once. Query
// captures can then store compact references while replay still receives every
// exact document MatchIntent returned.
func newGoldenIntentCapturer(ctx context.Context, reader *Reader) (*goldenIntentCapturer, error) {
	var reasons []graph.SearchReason
	if err := reader.db.WithContext(ctx).
		Where("namespace = ?", requestctx.FromContext(ctx)).
		Order("id").Find(&reasons).Error; err != nil {
		return nil, err
	}
	c := &goldenIntentCapturer{reader: reader, corpus: len(reasons), reasonIDs: make(map[string][]uint, len(reasons))}
	for _, reason := range reasons {
		key := intentReasonKey(reason.NodeID, reason.Content)
		c.reasonIDs[key] = append(c.reasonIDs[key], reason.ID)
	}
	return c, nil
}

func intentReasonKey(nodeID uint, content string) string {
	return strconv.FormatUint(uint64(nodeID), 10) + "\x00" + content
}

func (c *goldenIntentCapturer) capture(ctx context.Context, query string, fixture *goldenIntentFixture) (int, error) {
	docs, err := c.reader.backend.MatchIntent(ctx, c.reader.db, query, maxIntentCandidates)
	if err != nil {
		return 0, err
	}
	refs := make([]uint, 0, len(docs))
	used := make(map[string]int)
	nodeIDs := make([]uint, 0, len(docs))
	seenNode := make(map[uint]bool)
	for _, doc := range docs {
		key := intentReasonKey(doc.NodeID, doc.Content)
		at := used[key]
		ids := c.reasonIDs[key]
		if at >= len(ids) {
			return 0, fmt.Errorf("matched intent reason for node %d is absent from search_reasons", doc.NodeID)
		}
		ref := ids[at]
		used[key] = at + 1
		refs = append(refs, ref)
		document := goldenIntentDocument{NodeID: doc.NodeID, Content: doc.Content}
		if existing, ok := fixture.Documents[ref]; ok && existing != document {
			return 0, fmt.Errorf("intent document id %d identifies two different reasons", ref)
		}
		fixture.Documents[ref] = document
		if !seenNode[doc.NodeID] {
			seenNode[doc.NodeID] = true
			nodeIDs = append(nodeIDs, doc.NodeID)
		}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i] < refs[j] })
	fixture.Queries[query] = refs
	if len(nodeIDs) == 0 {
		return 0, nil
	}
	nodes, err := loadNodesInOrder(ctx, c.reader.db, nodeIDs)
	if err != nil {
		return 0, err
	}
	byID := make(map[uint]graph.Node, len(nodes))
	for _, node := range nodes {
		byID[node.ID] = node
	}
	for _, doc := range docs {
		node, ok := byID[doc.NodeID]
		if !ok {
			return 0, fmt.Errorf("matched intent node %d could not be loaded", doc.NodeID)
		}
		captured := goldenIntentNode{
			Name: node.Name, QualifiedName: doc.QualifiedName, Kind: string(doc.Kind),
			FilePath: doc.FilePath, Namespace: doc.Namespace, StartLine: doc.StartLine,
			Intent: node.Intent(), Reason: node.RecordedReason(),
		}
		if existing, ok := fixture.Nodes[doc.NodeID]; ok && existing != captured {
			return 0, fmt.Errorf("intent node id %d identifies two different nodes", doc.NodeID)
		}
		fixture.Nodes[doc.NodeID] = captured
	}
	return len(refs), nil
}

func validateGoldenIntentFixture(fixture goldenIntentFixture) error {
	usedDocuments := make(map[uint]bool, len(fixture.Documents))
	usedNodes := make(map[uint]bool, len(fixture.Nodes))
	for query, refs := range fixture.Queries {
		if !sort.SliceIsSorted(refs, func(i, j int) bool { return refs[i] < refs[j] }) {
			return fmt.Errorf("intent refs for %q are not in canonical id order", query)
		}
		seen := make(map[uint]bool, len(refs))
		for _, ref := range refs {
			if seen[ref] {
				return fmt.Errorf("intent refs for %q repeat document id %d", query, ref)
			}
			seen[ref] = true
			document, ok := fixture.Documents[ref]
			if !ok {
				return fmt.Errorf("intent refs for %q point to missing document id %d", query, ref)
			}
			if _, ok := fixture.Nodes[document.NodeID]; !ok {
				return fmt.Errorf("intent document id %d points to missing node id %d", ref, document.NodeID)
			}
			usedDocuments[ref] = true
			usedNodes[document.NodeID] = true
		}
	}
	for id := range fixture.Documents {
		if !usedDocuments[id] {
			return fmt.Errorf("intent document id %d is unreachable from every query", id)
		}
	}
	for id := range fixture.Nodes {
		if !usedNodes[id] {
			return fmt.Errorf("intent node id %d is unreachable from every query", id)
		}
	}
	return nil
}

func (c *goldenIntentCapturer) validateExisting(ctx context.Context, fixture goldenIntentFixture) error {
	if err := validateGoldenIntentFixture(fixture); err != nil {
		return err
	}
	if fixture.Corpus != c.corpus {
		return fmt.Errorf("intent corpus changed from %d to %d; run the full capture", fixture.Corpus, c.corpus)
	}
	for id, document := range fixture.Documents {
		ids := c.reasonIDs[intentReasonKey(document.NodeID, document.Content)]
		if !slices.Contains(ids, id) {
			return fmt.Errorf("intent document id %d no longer identifies the captured reason; run the full capture", id)
		}
	}
	for id, want := range fixture.Nodes {
		var node graph.Node
		if err := c.reader.db.WithContext(ctx).
			Where("id = ? AND namespace = ?", id, requestctx.FromContext(ctx)).
			Preload("Annotation.Tags").First(&node).Error; err != nil {
			return fmt.Errorf("intent node id %d no longer resolves: %w", id, err)
		}
		got := goldenIntentNode{
			Name: node.Name, QualifiedName: node.QualifiedName, Kind: string(node.Kind),
			FilePath: node.FilePath, Namespace: node.Namespace, StartLine: node.StartLine,
			Intent: node.Intent(), Reason: node.RecordedReason(),
		}
		if got != want {
			return fmt.Errorf("intent node id %d no longer identifies the captured node; run the full capture", id)
		}
	}
	return nil
}

func writeGoldenJSON(t *testing.T, path string, data any) {
	t.Helper()
	blob, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(blob, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCaptureMissingGoldenCandidates captures only the queries candidates.json
// does not already hold, and merges them in.
//
// Adding a query should not force a full recapture. A full recapture rereads
// every existing query against whatever graph is on disk today, which is how a
// retrieval regression gets baked into the fixture and stops being visible.
// This mode cannot do that: it never rewrites an entry that already exists, so
// a new query costs exactly its own candidate list and nothing else moves.
//
//	go test -tags fts5 ./internal/adapters/outbound/searchsql/ \
//	  -run TestCaptureMissingGoldenCandidates -capture-missing -count=1 -v
func TestCaptureMissingGoldenCandidates(t *testing.T) {
	if !*captureMissing {
		t.Skip("pass -capture-missing to add newly written golden queries")
	}
	dir, graphPath := captureTarget(t)
	var set struct {
		Corpus struct {
			Namespace string `json:"namespace"`
		} `json:"corpus"`
		Queries []struct {
			Query string `json:"query"`
		} `json:"queries"`
	}
	raw, err := os.ReadFile(dir + "queries.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	existing := map[string][]goldenCandidate{}
	blob, err := os.ReadFile(dir + "candidates.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(blob, &existing); err != nil {
		t.Fatal(err)
	}
	existingIntent := goldenIntentFixture{}
	if blob, err := os.ReadFile(dir + "intent_candidates.json"); err == nil {
		if err := json.Unmarshal(blob, &existingIntent); err != nil {
			t.Fatal(err)
		}
	}

	db, err := gorm.Open(sqlite.Open("file:"+graphPath+"?immutable=1&mode=ro"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open %s: %v", graphPath, err)
	}
	backend := &SQLiteBackend{}
	reader := NewReader(db, backend)
	ctx := requestctx.WithNamespace(context.Background(), set.Corpus.Namespace)
	capturer, err := newGoldenIntentCapturer(ctx, reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := capturer.validateExisting(ctx, existingIntent); err != nil {
		t.Fatal(err)
	}
	if existingIntent.Nodes == nil {
		existingIntent.Nodes = map[uint]goldenIntentNode{}
	}
	if existingIntent.Documents == nil {
		existingIntent.Documents = map[uint]goldenIntentDocument{}
	}
	if existingIntent.Queries == nil {
		existingIntent.Queries = map[string][]uint{}
	}

	added := 0
	for _, q := range set.Queries {
		_, haveNamed := existing[q.Query]
		_, haveIntent := existingIntent.Queries[q.Query]
		if haveNamed && haveIntent {
			continue
		}
		if !haveNamed {
			nodes, err := backend.Query(ctx, db, q.Query, rank.FetchLimit(goldenLimit))
			if err != nil {
				t.Fatalf("%q: %v", q.Query, err)
			}
			captured := make([]goldenCandidate, 0, len(nodes))
			for _, n := range nodes {
				captured = append(captured, goldenCandidate{
					ID:            n.ID,
					Name:          n.Name,
					QualifiedName: n.QualifiedName,
					Kind:          string(n.Kind),
					FilePath:      n.FilePath,
					Intent:        n.Intent(),
				})
			}
			existing[q.Query] = captured
		}
		matched := len(existingIntent.Queries[q.Query])
		if !haveIntent {
			matched, err = capturer.capture(ctx, q.Query, &existingIntent)
			if err != nil {
				t.Fatalf("%q: %v", q.Query, err)
			}
		}
		added++
		t.Logf("added %-40q -> %2d candidates, %2d matched intent reasons", q.Query, len(existing[q.Query]), matched)
	}
	if added == 0 {
		t.Log("every query already has captured candidates; nothing written")
		return
	}
	if err := validateGoldenIntentFixture(existingIntent); err != nil {
		t.Fatal(err)
	}
	writeGoldenJSON(t, dir+"candidates.json", existing)
	writeGoldenJSON(t, dir+"intent_candidates.json", existingIntent)
	t.Logf("candidates.json and intent_candidates.json: %d queries added, existing entries untouched", added)
}

var (
	captureGolden  = flag.Bool("capture-golden", false, "rewrite the ranking golden candidate fixture from a local graph")
	captureMissing = flag.Bool("capture-missing", false, "capture only golden queries missing from the fixture and merge them in")
	captureCorpus  = flag.String("corpus", "", "capture the named extra corpus under testdata/corpora/ instead of the primary set")
	captureGraph   = flag.String("graph", "", "path to the graph database to capture from (default: the primary corpus's ./ccg.db)")
)

// captureTarget resolves which corpus a capture run rewrites and which graph it
// reads. The primary set keeps its historical defaults; an extra corpus names
// its directory with -corpus and must name its graph with -graph, because an
// external codebase's graph has no conventional location in this repository.
func captureTarget(t *testing.T) (dir, graphPath string) {
	t.Helper()
	dir, graphPath = goldenDir, goldenGraphPath
	if *captureCorpus != "" {
		dir = goldenDir + "corpora/" + *captureCorpus + "/"
		if *captureGraph == "" {
			t.Fatalf("capturing corpus %q needs -graph pointing at its built graph database", *captureCorpus)
		}
	}
	if *captureGraph != "" {
		graphPath = *captureGraph
	}
	return dir, graphPath
}

const (
	goldenDir = "../../../app/search/rank/testdata/"
	// goldenGraphPath is the local graph the fixture is captured from. It is
	// build output, not tracked, so the capture only runs where one exists.
	goldenGraphPath = "../../../../ccg.db"
	// goldenLimit must stay equal to the limit the golden set replays with,
	// or the fixture would hold a differently sized pool than the one scored.
	goldenLimit = 10
)

// goldenCandidate mirrors the fixture record the rank golden set reads back.
//
// Intent is captured because search now shows a node's @intent as evidence and
// treats a word shared with the query as a reason to keep the node. A fixture
// without it would replay a search that has no annotations at all, and would
// score annotation-matched hits as if they had been dropped.
type goldenCandidate struct {
	ID            uint   `json:"id"`
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	FilePath      string `json:"file_path"`
	Intent        string `json:"intent,omitempty"`
}

type goldenIntentFixture struct {
	Corpus    int                           `json:"corpus,omitempty"`
	Nodes     map[uint]goldenIntentNode     `json:"nodes,omitempty"`
	Documents map[uint]goldenIntentDocument `json:"documents,omitempty"`
	Queries   map[string][]uint             `json:"queries"`
}

type goldenIntentDocument struct {
	NodeID  uint   `json:"node_id"`
	Content string `json:"content"`
}

type goldenIntentNode struct {
	Name          string `json:"name"`
	QualifiedName string `json:"qualified_name"`
	Kind          string `json:"kind"`
	FilePath      string `json:"file_path"`
	Namespace     string `json:"namespace,omitempty"`
	StartLine     int    `json:"start_line,omitempty"`
	Intent        string `json:"intent,omitempty"`
	Reason        string `json:"reason,omitempty"`
}
