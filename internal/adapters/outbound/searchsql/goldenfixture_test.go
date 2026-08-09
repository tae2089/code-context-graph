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

	"github.com/tae2089/code-context-graph/internal/app/search/rank"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
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

	// Read-only, and immutable so SQLite never writes a WAL sidecar next to a
	// graph the test does not own.
	db, err := gorm.Open(sqlite.Open("file:"+goldenGraphPath+"?immutable=1&mode=ro"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open %s: %v", goldenGraphPath, err)
	}
	backend := &SQLiteBackend{}
	ctx := requestctx.WithNamespace(context.Background(), set.Corpus.Namespace)

	out := map[string][]goldenCandidate{}
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
		t.Logf("%-30q -> %2d candidates", q.Query, len(captured))
	}

	blob, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goldenDir+"candidates.json", append(blob, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Log("candidates.json rewritten; re-run the rank golden report and review every change")
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
	existing := map[string][]goldenCandidate{}
	blob, err := os.ReadFile(goldenDir + "candidates.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(blob, &existing); err != nil {
		t.Fatal(err)
	}

	db, err := gorm.Open(sqlite.Open("file:"+goldenGraphPath+"?immutable=1&mode=ro"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open %s: %v", goldenGraphPath, err)
	}
	backend := &SQLiteBackend{}
	ctx := requestctx.WithNamespace(context.Background(), set.Corpus.Namespace)

	added := 0
	for _, q := range set.Queries {
		if _, ok := existing[q.Query]; ok {
			continue
		}
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
		added++
		t.Logf("added %-40q -> %2d candidates", q.Query, len(captured))
	}
	if added == 0 {
		t.Log("every query already has captured candidates; nothing written")
		return
	}
	out, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goldenDir+"candidates.json", append(out, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("candidates.json: %d queries added, existing entries untouched", added)
}

var (
	captureGolden  = flag.Bool("capture-golden", false, "rewrite the ranking golden candidate fixture from a local graph")
	captureMissing = flag.Bool("capture-missing", false, "capture only golden queries missing from the fixture and merge them in")
)

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
