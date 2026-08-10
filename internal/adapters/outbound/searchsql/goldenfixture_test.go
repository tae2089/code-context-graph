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
	outIntent := map[string]goldenIntentAnswer{}
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
		answer, err := captureIntentAnswer(ctx, reader, q.Query)
		if err != nil {
			t.Fatalf("%q: %v", q.Query, err)
		}
		outIntent[q.Query] = answer
		t.Logf("%-30q -> %2d candidates, %2d intent hits", q.Query, len(captured), len(answer.Hits))
	}

	writeGoldenJSON(t, dir+"candidates.json", out)
	writeGoldenJSON(t, dir+"intent_candidates.json", outIntent)
	t.Log("candidates.json and intent_candidates.json rewritten; re-run the rank golden report and review every change")
}

// captureIntentAnswer runs the production intent query for one golden query,
// with the same over-fetch the search service asks for, and keeps the whole
// answer: hits, scored terms with their reason counts, and the corpus size.
// The terms matter as much as the hits — membership is gated on them.
func captureIntentAnswer(ctx context.Context, reader *Reader, query string) (goldenIntentAnswer, error) {
	result, err := reader.QueryIntent(ctx, query, rank.FetchLimit(goldenLimit))
	if err != nil {
		return goldenIntentAnswer{}, err
	}
	answer := goldenIntentAnswer{Corpus: result.Corpus}
	for _, term := range result.Terms {
		answer.Terms = append(answer.Terms, goldenIntentTerm{Text: term.Text, InReasons: term.InReasons})
	}
	for _, h := range result.Hits {
		answer.Hits = append(answer.Hits, goldenIntentHit{
			ID:            h.Node.ID,
			Name:          h.Node.Name,
			QualifiedName: h.Node.QualifiedName,
			Kind:          string(h.Node.Kind),
			FilePath:      h.Node.FilePath,
			Intent:        h.Node.Intent(),
			Reason:        h.Node.RecordedReason(),
			Terms:         h.Terms,
		})
	}
	return answer, nil
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
	existingIntent := map[string]goldenIntentAnswer{}
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

	added := 0
	for _, q := range set.Queries {
		_, haveNamed := existing[q.Query]
		_, haveIntent := existingIntent[q.Query]
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
		if !haveIntent {
			answer, err := captureIntentAnswer(ctx, reader, q.Query)
			if err != nil {
				t.Fatalf("%q: %v", q.Query, err)
			}
			existingIntent[q.Query] = answer
		}
		added++
		t.Logf("added %-40q -> %2d candidates, %2d intent hits", q.Query, len(existing[q.Query]), len(existingIntent[q.Query].Hits))
	}
	if added == 0 {
		t.Log("every query already has captured candidates; nothing written")
		return
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

// goldenIntentAnswer mirrors the intent-candidate record the rank golden set
// reads back: the ranked hits, every scored term with its reason count, and the
// corpus size. The terms are captured because membership is gated on them.
type goldenIntentAnswer struct {
	Corpus int                `json:"corpus,omitempty"`
	Terms  []goldenIntentTerm `json:"terms,omitempty"`
	Hits   []goldenIntentHit  `json:"hits,omitempty"`
}

// goldenIntentTerm is one scored term of the question and how many recorded
// reasons in the whole index hold it.
type goldenIntentTerm struct {
	Text      string `json:"text"`
	InReasons int    `json:"in_reasons"`
}

// goldenIntentHit is one node the intent index answered the query with, the
// recorded reason it matched, and the query terms the scorer counted in it.
type goldenIntentHit struct {
	ID            uint     `json:"id"`
	Name          string   `json:"name"`
	QualifiedName string   `json:"qualified_name"`
	Kind          string   `json:"kind"`
	FilePath      string   `json:"file_path"`
	Intent        string   `json:"intent,omitempty"`
	Reason        string   `json:"reason,omitempty"`
	Terms         []string `json:"terms,omitempty"`
}
