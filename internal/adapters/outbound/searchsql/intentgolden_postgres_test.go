//go:build fts5 && postgres

package searchsql

import (
	"testing"

	intentapp "github.com/tae2089/code-context-graph/internal/app/search/intent"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
)

// intentPostgresBaseline is PostgreSQL's own record of the golden run.
//
// It is a separate file because the two backends do not rank the same question
// the same way, and a shared baseline would report that difference as a code
// regression on whichever backend was not used to record it.
const intentPostgresBaseline = "intent_baseline_postgres.json"

// runIntentGoldenPostgres replays the frozen corpus against a live PostgreSQL
// database. It skips when no server answers, so a machine without one still
// runs the SQLite half.
//
// Careful: setupPostgresDB drops and recreates the `public` schema of whatever
// TEST_POSTGRES_DSN points at. Point it at a throwaway database.
func runIntentGoldenPostgres(t *testing.T) ([]intentOutcome, intentapp.Coverage, intentCorpus) {
	t.Helper()
	return runIntentGoldenOn(t, setupPostgresDB(t), NewPostgresBackend())
}

// TestQueryIntentPostgres_OrdersTiedReasonsTheSameWayAtEveryLimit is the
// PostgreSQL twin of the SQLite test of the same name. It is the backend that
// actually reshuffled: `ts_rank` scores are coarse enough that nine recorded
// reasons landed on exactly 0.020264236 for one golden question, and changing
// the limit changed which of them came first.
func TestQueryIntentPostgres_OrdersTiedReasonsTheSameWayAtEveryLimit(t *testing.T) {
	db := setupPostgresDB(t)
	seedTiedIntentFixture(t, db, 12)
	backend := NewPostgresBackend()
	ctx := requestctx.WithNamespace(t.Context(), requestctx.DefaultNamespace)
	if _, err := RefreshSearchDocuments(ctx, db); err != nil {
		t.Fatalf("RefreshSearchDocuments: %v", err)
	}
	if err := backend.Rebuild(ctx, db); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}

	reader := NewReader(db, backend)
	shortResult, err := reader.QueryIntent(ctx, "what keeps the queue draining", 4)
	if err != nil {
		t.Fatalf("QueryIntent: %v", err)
	}
	longResult, err := reader.QueryIntent(ctx, "what keeps the queue draining", 12)
	if err != nil {
		t.Fatalf("QueryIntent: %v", err)
	}
	short, long := answeringNodes(shortResult), answeringNodes(longResult)
	if len(short) != 4 || len(long) != 12 {
		t.Fatalf("got %d and %d nodes, want 4 and 12", len(short), len(long))
	}
	for i, node := range short {
		if long[i].ID != node.ID {
			t.Fatalf("row %d is node %d at limit 4 but node %d at limit 12", i, node.ID, long[i].ID)
		}
	}
}

// TestGoldenIntentPostgres_HasNotRegressed is the PostgreSQL twin of
// TestGoldenIntent_HasNotRegressed. Run it with `-tags "fts5,postgres"`.
func TestGoldenIntentPostgres_HasNotRegressed(t *testing.T) {
	outcomes, _, _ := runIntentGoldenPostgres(t)
	checkIntentBaseline(t, outcomes, intentPostgresBaseline)
}

// TestGoldenIntentPostgres_StartsTheReaderInTheSamePlaceAsSQLite is the reason
// scoring moved out of the databases.
//
// It used to be false, and expensively so: PostgreSQL scored 0.603 against
// SQLite's 0.740 on the same corpus, and one question lost its answer entirely,
// because ts_rank never learns that a word is written in most recorded reasons.
// The golden score was measured on a laptop running SQLite and the server runs
// PostgreSQL, so the number being tracked described a system nobody used.
//
// Only what the caller acts on is compared. Entry counts are allowed to differ
// because the two indexes tokenize prose slightly differently and so admit
// slightly different candidates — measured at one entry apart on two of sixteen
// questions. Which file the reader is sent to first is not allowed to differ at
// all.
func TestGoldenIntentPostgres_StartsTheReaderInTheSamePlaceAsSQLite(t *testing.T) {
	outcomes, _, _ := runIntentGoldenPostgres(t)

	var sqlite []intentOutcome
	readIntentJSON(t, intentDir+"intent_baseline.json", &sqlite)
	want := make(map[string]intentOutcome, len(sqlite))
	for _, outcome := range sqlite {
		want[outcome.Question] = outcome
	}

	for _, got := range outcomes {
		other, ok := want[got.Question]
		if !ok {
			t.Errorf("%q is missing from the SQLite baseline", got.Question)
			continue
		}
		if got.Rank != other.Rank {
			t.Errorf("%q: first acceptable file is %d on PostgreSQL and %d on SQLite", got.Question, got.Rank, other.Rank)
		}
		if got.Answered != other.Answered {
			t.Errorf("%q: answered is %v on PostgreSQL and %v on SQLite", got.Question, got.Answered, other.Answered)
		}
		if got.Files != other.Files {
			t.Errorf("%q: %d files on PostgreSQL and %d on SQLite", got.Question, got.Files, other.Files)
		}
	}
}

// TestGoldenIntentPostgres_Report prints the PostgreSQL scoreboard, which should
// now read the same as the SQLite one.
func TestGoldenIntentPostgres_Report(t *testing.T) {
	outcomes, coverage, corpus := runIntentGoldenPostgres(t)
	reportIntentGolden(t, outcomes, coverage, corpus)
}
