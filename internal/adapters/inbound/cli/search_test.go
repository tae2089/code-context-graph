package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/viper"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/adapters/outbound/graphgorm"
	search "github.com/tae2089/code-context-graph/internal/adapters/outbound/searchsql"
	"github.com/tae2089/code-context-graph/internal/app/search/evidence"
	intentapp "github.com/tae2089/code-context-graph/internal/app/search/intent"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

type spySearchBackend struct {
	queryFn func(ctx context.Context, query string, limit int) ([]graph.Node, error)
}

func (s *spySearchBackend) Query(ctx context.Context, query string, limit int) ([]graph.Node, error) {
	if s.queryFn != nil {
		return s.queryFn(ctx, query, limit)
	}
	return nil, nil
}

func (s *spySearchBackend) QueryIntent(ctx context.Context, query string, limit int) (intentapp.Result, error) {
	return intentapp.Result{}, nil
}

var searchTestDBSeq atomic.Int64

func setupSearchTest(t *testing.T) (*Deps, *bytes.Buffer, *bytes.Buffer, *gorm.DB) {
	t.Helper()
	deps, stdout, stderr := newTestDeps()

	// A shared-cache named memory DB, because the search service queries both
	// indexes concurrently: a plain :memory: DSN gives every pool connection
	// its own empty database, and the second query finds no tables.
	dsn := fmt.Sprintf("file:clisearchtest%d?mode=memory&cache=shared", searchTestDBSeq.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}

	st := graphgorm.New(db)
	if err := st.AutoMigrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&graph.SearchDocument{}); err != nil {
		t.Fatal(err)
	}

	sb := search.NewSQLiteBackend()
	if err := sb.Migrate(db); err != nil {
		if errors.Is(err, search.ErrFTS5NotAvailable) {
			t.Skip("fts5 module not available, skipping test")
		}
		t.Fatal(err)
	}

	deps.Store = st
	deps.SearchReader = search.NewReader(db, sb)

	return deps, stdout, stderr, db
}

func seedSearchData(t *testing.T, db *gorm.DB) {
	t.Helper()
	ctx := context.Background()

	nodes := []graph.Node{
		{Name: "Hello", QualifiedName: "pkg.Hello", Kind: graph.NodeKindFunction, FilePath: "hello.go", StartLine: 3, EndLine: 5, Language: "go"},
		{Name: "World", QualifiedName: "pkg.World", Kind: graph.NodeKindFunction, FilePath: "world.go", StartLine: 1, EndLine: 3, Language: "go"},
		{Name: "Foo", QualifiedName: "pkg.Foo", Kind: graph.NodeKindFunction, FilePath: "foo.go", StartLine: 1, EndLine: 2, Language: "go"},
	}
	if err := db.WithContext(ctx).Create(&nodes).Error; err != nil {
		t.Fatal(err)
	}

	docs := []graph.SearchDocument{
		{Namespace: nodes[0].Namespace, NodeID: nodes[0].ID, Content: "Hello function says hello", Language: "go"},
		{Namespace: nodes[1].Namespace, NodeID: nodes[1].ID, Content: "World function says world", Language: "go"},
		{Namespace: nodes[2].Namespace, NodeID: nodes[2].ID, Content: "Foo function does foo stuff", Language: "go"},
	}
	if err := db.WithContext(ctx).Create(&docs).Error; err != nil {
		t.Fatal(err)
	}

	sb := search.NewSQLiteBackend()
	if err := sb.Rebuild(ctx, db); err != nil {
		t.Fatal(err)
	}
}

func TestSearchCommand_FindsResults(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedSearchData(t, db)

	stdout.Reset()

	err := executeCmd(deps, stdout, stderr, "search", "Hello")
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "pkg.Hello") {
		t.Fatalf("expected pkg.Hello in output, got: %s", out)
	}
}

// --json answers with the same wire contract the MCP search tool speaks, so an
// agent can consume either surface with one parser.
func TestSearchCommand_JSONSpeaksTheMCPContract(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedSearchData(t, db)

	stdout.Reset()
	if err := executeCmd(deps, stdout, stderr, "search", "--json", "Hello"); err != nil {
		t.Fatalf("search: %v", err)
	}

	var payload struct {
		Files []struct {
			FilePath string `json:"file_path"`
			HitCount int    `json:"hit_count"`
			Hits     []struct {
				QualifiedName string   `json:"qualified_name"`
				Matched       []string `json:"matched"`
			} `json:"hits"`
		} `json:"files"`
		FileCount int `json:"file_count"`
		Limits    struct {
			Files     int `json:"files"`
			HitBudget int `json:"hit_budget"`
		} `json:"limits"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v output=%s", err, stdout.String())
	}
	if payload.FileCount != 1 || len(payload.Files) != 1 {
		t.Fatalf("file_count = %d files = %#v, want the one answering file", payload.FileCount, payload.Files)
	}
	file := payload.Files[0]
	if file.FilePath != "hello.go" || file.HitCount != 1 {
		t.Fatalf("files[0] = %#v, want hello.go with 1 hit", file)
	}
	if file.Hits[0].QualifiedName != "pkg.Hello" {
		t.Errorf("hits[0].qualified_name = %q", file.Hits[0].QualifiedName)
	}
	if len(file.Hits[0].Matched) == 0 {
		t.Errorf("hits[0].matched is empty; the contract explains every hit")
	}
	if payload.Limits.Files != 10 || payload.Limits.HitBudget != evidence.PageHitBudget {
		t.Errorf("limits = %#v", payload.Limits)
	}
}

// A truncated --json answer carries the same next actions MCP emits, phrased as
// a repeatable search call.
func TestSearchCommand_JSONNamesTheNextPage(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedSearchData(t, db)

	stdout.Reset()
	if err := executeCmd(deps, stdout, stderr, "search", "--json", "--limit", "1", "--include-weak", "function"); err != nil {
		t.Fatalf("search: %v", err)
	}

	var payload struct {
		Truncated bool `json:"truncated"`
		Next      []struct {
			Tool string         `json:"tool"`
			Args map[string]any `json:"args"`
		} `json:"next"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v output=%s", err, stdout.String())
	}
	if !payload.Truncated {
		t.Fatalf("truncated = false with three files behind --limit 1: %s", stdout.String())
	}
	if len(payload.Next) == 0 || payload.Next[0].Tool != "search" {
		t.Fatalf("next = %#v, want a search call for the withheld files", payload.Next)
	}
	if offset, ok := payload.Next[0].Args["offset"].(float64); !ok || offset != 1 {
		t.Errorf("next[0].args.offset = %#v, want 1", payload.Next[0].Args["offset"])
	}
}

// An empty --json answer is still one JSON document with its reason inside,
// never the human "No results" lines.
func TestSearchCommand_JSONExplainsAnEmptyAnswer(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedSearchData(t, db)

	stdout.Reset()
	if err := executeCmd(deps, stdout, stderr, "search", "--json", "zzzznotfound"); err != nil {
		t.Fatalf("search: %v", err)
	}

	var payload struct {
		FileCount int    `json:"file_count"`
		Note      string `json:"note"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v output=%s", err, stdout.String())
	}
	if payload.FileCount != 0 {
		t.Fatalf("file_count = %d, want 0", payload.FileCount)
	}
	if payload.Note == "" {
		t.Errorf("note is empty; an empty answer must say which kind of empty it is")
	}
}

func TestSearchCommand_NoResults(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedSearchData(t, db)

	stdout.Reset()

	err := executeCmd(deps, stdout, stderr, "search", "zzzznotfound")
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "No results") {
		t.Fatalf("expected 'No results', got: %s", out)
	}
}

// --limit counts files, and the footer says how to read the rest.
func TestSearchCommand_LimitFlagCountsFiles(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedSearchData(t, db)

	stdout.Reset()

	// "function" appears in every seeded document but in no name, path, or
	// @intent, so all three hits are weak. This test is about --limit, so it
	// asks for the weak candidates instead of losing them to the evidence cut.
	err := executeCmd(deps, stdout, stderr, "search", "--limit", "1", "--include-weak", "function")
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	out := stdout.String()
	var results int
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.HasPrefix(line, " ") {
			results++
		}
	}
	if results != 1 {
		t.Fatalf("expected the single file's 1 hit with --limit 1, got %d: %s", results, out)
	}
	if !strings.Contains(out, "--offset 1") {
		t.Errorf("nothing told the reader how to reach the other files: %s", out)
	}
}

// seedCrowdedFile indexes one file holding dense declarations, then quiet
// files of one each. The crowded file alone fills a first page's candidate
// pool, so the page ends at the pool's edge with no further file left to count.
func seedCrowdedFile(t *testing.T, db *gorm.DB, dense, quiet int) {
	t.Helper()
	ctx := context.Background()

	nodes := make([]graph.Node, 0, dense+quiet)
	for i := range dense {
		nodes = append(nodes, graph.Node{
			Name: fmt.Sprintf("syncQueueStep%03d", i), QualifiedName: fmt.Sprintf("pkg.SyncQueue.step%03d", i),
			Kind: graph.NodeKindFunction, FilePath: "crowded.go", StartLine: i*10 + 1, EndLine: i*10 + 5, Language: "go",
		})
	}
	for i := range quiet {
		nodes = append(nodes, graph.Node{
			Name: fmt.Sprintf("syncQueueStep%03d", dense+i), QualifiedName: fmt.Sprintf("pkg.SyncQueue.quiet%03d", i),
			Kind: graph.NodeKindFunction, FilePath: fmt.Sprintf("quiet%02d.go", i), StartLine: 1, EndLine: 5, Language: "go",
		})
	}
	if err := db.WithContext(ctx).Create(&nodes).Error; err != nil {
		t.Fatal(err)
	}

	docs := make([]graph.SearchDocument, 0, len(nodes))
	for _, n := range nodes {
		docs = append(docs, graph.SearchDocument{Namespace: n.Namespace, NodeID: n.ID, Content: "syncqueue worker step", Language: "go"})
	}
	if err := db.WithContext(ctx).Create(&docs).Error; err != nil {
		t.Fatal(err)
	}
	if err := search.NewSQLiteBackend().Rebuild(ctx, db); err != nil {
		t.Fatal(err)
	}
}

// One file's hits can fill the whole candidate pool, and then the page reaches
// every file the pool held. The reader still has files waiting, so the printed
// answer has to say so rather than just ending.
func TestSearchCommand_SaysWhenThePageEndedAtThePoolsEdge(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedCrowdedFile(t, db, evidence.PageHitBudget, 10)

	stdout.Reset()
	if err := executeCmd(deps, stdout, stderr, "search", "--limit", "10", "syncqueue"); err != nil {
		t.Fatalf("search: %v", err)
	}

	out := stdout.String()
	if strings.Contains(out, "quiet00.go") {
		t.Fatalf("the crowded file was meant to fill the page on its own: %s", out)
	}
	if !strings.Contains(out, "--offset 1") {
		t.Errorf("the answer ended without telling the reader the quiet files are still waiting: %s", out)
	}
}

// A second page starts at a new file; no file is ever split across pages.
func TestSearchCommand_OffsetMovesToTheNextFile(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedSearchData(t, db)

	stdout.Reset()
	if err := executeCmd(deps, stdout, stderr, "search", "--limit", "1", "--include-weak", "function"); err != nil {
		t.Fatalf("search: %v", err)
	}
	first := stdout.String()

	stdout.Reset()
	if err := executeCmd(deps, stdout, stderr, "search", "--limit", "1", "--offset", "1", "--include-weak", "function"); err != nil {
		t.Fatalf("search: %v", err)
	}
	second := stdout.String()

	if second == first {
		t.Fatalf("--offset 1 returned the same page: %s", second)
	}
	if strings.Contains(second, "No results") {
		t.Fatalf("--offset 1 fell off the end: %s", second)
	}
}

func TestSearchCommand_PathFilter_IncludesMatch(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)

	ctx := context.Background()
	// Both names carry the query word, so the path filter is the only thing
	// that can separate them — the evidence cut leaves both standing.
	nodes := []graph.Node{
		{Name: "HandleLogin", QualifiedName: "internal/auth/login.go::HandleLogin", Kind: graph.NodeKindFunction, FilePath: "internal/auth/login.go", StartLine: 1, EndLine: 5, Language: "go"},
		{Name: "HandlePay", QualifiedName: "internal/payment/pay.go::HandlePay", Kind: graph.NodeKindFunction, FilePath: "internal/payment/pay.go", StartLine: 1, EndLine: 5, Language: "go"},
	}
	db.WithContext(ctx).Create(&nodes)

	docs := []graph.SearchDocument{
		{Namespace: nodes[0].Namespace, NodeID: nodes[0].ID, Content: "handle user login", Language: "go"},
		{Namespace: nodes[1].Namespace, NodeID: nodes[1].ID, Content: "handle payment", Language: "go"},
	}
	db.WithContext(ctx).Create(&docs)
	search.NewSQLiteBackend().Rebuild(ctx, db)

	if err := executeCmd(deps, stdout, stderr, "search", "--path", "internal/auth", "handle"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "HandleLogin") {
		t.Errorf("expected HandleLogin in output, got:\n%s", out)
	}
	if strings.Contains(out, "HandlePay\t") {
		t.Errorf("HandlePay should be excluded by --path filter, got:\n%s", out)
	}
}

func TestSearchCommand_PathFilter_NoMatch(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedSearchData(t, db)

	if err := executeCmd(deps, stdout, stderr, "search", "--path", "internal/nonexistent", "Hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(stdout.String(), "No results") {
		t.Errorf("expected 'No results' for unmatched path, got:\n%s", stdout.String())
	}
}

func TestSearchCommand_PathFilter_RespectsPathBoundary(t *testing.T) {
	deps, stdout, stderr := newTestDeps()
	deps.SearchReader = &spySearchBackend{queryFn: func(ctx context.Context, query string, queryLimit int) ([]graph.Node, error) {
		return []graph.Node{
			{QualifiedName: "internal/api/handler.go::Handle", Kind: graph.NodeKindFunction, FilePath: "internal/api/handler.go", StartLine: 1},
			{QualifiedName: "internal/api2/handler.go::Handle2", Kind: graph.NodeKindFunction, FilePath: "internal/api2/handler.go", StartLine: 1},
		}, nil
	}}

	if err := executeCmd(deps, stdout, stderr, "search", "--path", "internal/api", "handle"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "internal/api/handler.go::Handle") {
		t.Fatalf("expected internal/api result, got:\n%s", out)
	}
	if strings.Contains(out, "internal/api2/handler.go::Handle2") {
		t.Fatalf("did not expect sibling path match, got:\n%s", out)
	}
}

// seedEvidenceData plants one node the query can be justified against and one
// it cannot, so the evidence cut has something to keep and something to drop.
func seedEvidenceData(t *testing.T, db *gorm.DB) {
	t.Helper()
	ctx := context.Background()

	nodes := []graph.Node{
		{Name: "SyncQueue", QualifiedName: "reposync.SyncQueue", Kind: graph.NodeKindType, FilePath: "internal/app/reposync/queue.go", StartLine: 12, EndLine: 40, Language: "go"},
		{Name: "helper", QualifiedName: "misc.helper", Kind: graph.NodeKindFunction, FilePath: "internal/misc/misc.go", StartLine: 1, EndLine: 4, Language: "go"},
	}
	if err := db.WithContext(ctx).Create(&nodes).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.WithContext(ctx).Create(&graph.Annotation{
		NodeID: nodes[0].ID,
		Tags:   []graph.DocTag{{Kind: graph.TagIntent, Value: "hand webhook pushes to a bounded worker pool"}},
	}).Error; err != nil {
		t.Fatal(err)
	}

	docs := []graph.SearchDocument{
		{Namespace: nodes[0].Namespace, NodeID: nodes[0].ID, Content: "SyncQueue hand webhook pushes to a bounded worker pool", Language: "go"},
		{Namespace: nodes[1].Namespace, NodeID: nodes[1].ID, Content: "helper mentions webhook only in passing", Language: "go"},
	}
	if err := db.WithContext(ctx).Create(&docs).Error; err != nil {
		t.Fatal(err)
	}
	if err := search.NewSQLiteBackend().Rebuild(ctx, db); err != nil {
		t.Fatal(err)
	}
}

// The whole point of the change: a reader should be able to see why a result is
// in the list without opening the file.
func TestSearchCommand_PrintsIntentUnderTheResult(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedEvidenceData(t, db)
	stdout.Reset()

	if err := executeCmd(deps, stdout, stderr, "search", "webhook"); err != nil {
		t.Fatalf("search: %v", err)
	}

	out := stdout.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected a result line and an evidence line, got:\n%s", out)
	}
	if strings.HasPrefix(lines[0], " ") {
		t.Errorf("the result line must stay unindented so scripts can still read it: %q", lines[0])
	}
	if !strings.Contains(lines[0], "internal/app/reposync/queue.go:12") {
		t.Errorf("result line lost its path:line, got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], " ") {
		t.Errorf("the evidence line must be indented so `grep -v '^ '` still yields plain results: %q", lines[1])
	}
	if !strings.Contains(lines[1], "bounded worker pool") {
		t.Errorf("evidence line missing the @intent text, got %q", lines[1])
	}
	if !strings.Contains(lines[1], "[intent]") {
		t.Errorf("evidence line missing the matched-signal labels, got %q", lines[1])
	}
}

// A hit only the intent index found prints its recorded reason on the evidence
// line, even when the node has no @intent of its own — otherwise the reader
// sees a bare result line with nothing saying why it answered the question.
func TestSearchCommand_PrintsTheRecordedReasonForAnIntentHit(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	ctx := context.Background()

	node := graph.Node{Name: "admitRepo", QualifiedName: "webhook.admitRepo", Kind: graph.NodeKindFunction, FilePath: "internal/webhook/admission.go", StartLine: 5, EndLine: 20, Language: "go"}
	if err := db.WithContext(ctx).Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	// Only a @domainRule: node.Intent() stays empty, so the fallback to the
	// recorded reason is the only thing that can fill the evidence line.
	if err := db.WithContext(ctx).Create(&graph.Annotation{
		NodeID: node.ID,
		Tags:   []graph.DocTag{{Kind: graph.TagDomainRule, Value: "only pushes from allowed repositories may trigger a build"}},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.WithContext(ctx).Create(&graph.SearchDocument{
		Namespace: node.Namespace, NodeID: node.ID,
		Content: "admitRepo checks repository allowlist", Language: "go",
		IntentContent: "only pushes from allowed repositories may trigger a build",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := search.NewSQLiteBackend().Rebuild(ctx, db); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()

	if err := executeCmd(deps, stdout, stderr, "search", "which push may trigger a build"); err != nil {
		t.Fatalf("search: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "webhook.admitRepo") {
		t.Fatalf("the recorded reason did not answer the question:\n%s", out)
	}
	if !strings.Contains(out, "    only pushes from allowed repositories may trigger a build") {
		t.Errorf("evidence line missing the recorded reason:\n%s", out)
	}
	// Fuzzy name scoring may add its own label, so only the intent label is pinned.
	if !strings.Contains(out, "intent]") {
		t.Errorf("evidence line missing the matched-signal label:\n%s", out)
	}
}

func TestSearchCommand_HidesCandidatesWithNoEvidence(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedEvidenceData(t, db)
	stdout.Reset()

	if err := executeCmd(deps, stdout, stderr, "search", "webhook"); err != nil {
		t.Fatalf("search: %v", err)
	}

	out := stdout.String()
	if strings.Contains(out, "misc.helper") {
		t.Errorf("a candidate with no name, path, or intent match was shown:\n%s", out)
	}
	if !strings.Contains(out, "--include-weak") {
		t.Errorf("the hidden candidate was never mentioned, so the reader cannot ask for it:\n%s", out)
	}
}

func TestSearchCommand_IncludeWeakShowsTheHiddenCandidates(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedEvidenceData(t, db)
	stdout.Reset()

	if err := executeCmd(deps, stdout, stderr, "search", "--include-weak", "webhook"); err != nil {
		t.Fatalf("search: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "misc.helper") {
		t.Errorf("--include-weak did not bring the weak candidate back:\n%s", out)
	}
	if strings.Index(out, "reposync.SyncQueue") > strings.Index(out, "misc.helper") {
		t.Errorf("weak candidates must come last:\n%s", out)
	}
}

// An empty answer has to say which kind of empty it is, or the caller cannot
// tell "rephrase the query" from "ask again including the weak candidates".
func TestSearchCommand_ExplainsAnEmptyResult(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedEvidenceData(t, db)
	stdout.Reset()

	if err := executeCmd(deps, stdout, stderr, "search", "zzzznotfound"); err != nil {
		t.Fatalf("search: %v", err)
	}

	out := stdout.String()
	if !strings.Contains(out, "No results") {
		t.Fatalf("expected 'No results', got:\n%s", out)
	}
	if len(strings.Split(strings.TrimRight(out, "\n"), "\n")) < 2 {
		t.Errorf("an empty result printed no explanation:\n%s", out)
	}
}

func TestSearchCommand_NamespaceIsolation(t *testing.T) {
	_, _, _, db := setupSearchTest(t)
	ctxA := requestctx.WithNamespace(context.Background(), "ns-a")
	ctxB := requestctx.WithNamespace(context.Background(), "ns-b")

	nodeA := graph.Node{Namespace: "ns-a", Name: "SearchA", QualifiedName: "pkg.SearchA", Kind: graph.NodeKindFunction, FilePath: "a.go", StartLine: 1, EndLine: 2, Language: "go"}
	nodeB := graph.Node{Namespace: "ns-b", Name: "SearchB", QualifiedName: "pkg.SearchB", Kind: graph.NodeKindFunction, FilePath: "b.go", StartLine: 1, EndLine: 2, Language: "go"}
	if err := db.Create(&nodeA).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&nodeB).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: "ns-a", NodeID: nodeA.ID, Content: "sharedterm alpha", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&graph.SearchDocument{Namespace: "ns-b", NodeID: nodeB.ID, Content: "sharedterm beta", Language: "go"}).Error; err != nil {
		t.Fatal(err)
	}
	sb := search.NewSQLiteBackend()
	if err := sb.Rebuild(ctxA, db); err != nil {
		t.Fatal(err)
	}
	if err := sb.Rebuild(ctxB, db); err != nil {
		t.Fatal(err)
	}

	resultsA, err := sb.Query(ctxA, db, "sharedterm", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(resultsA) != 1 || resultsA[0].Namespace != "ns-a" {
		t.Fatalf("expected only ns-a result, got %#v", resultsA)
	}

	resultsB, err := sb.Query(ctxB, db, "sharedterm", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(resultsB) != 1 || resultsB[0].Namespace != "ns-b" {
		t.Fatalf("expected only ns-b result, got %#v", resultsB)
	}
}

func TestSearchCommand_SpecialCharactersDoNotError(t *testing.T) {
	deps, stdout, stderr, db := setupSearchTest(t)
	seedSearchData(t, db)

	for _, query := range []string{"func(x)", "foo:bar", "hello-world", "\"unterminated"} {
		stdout.Reset()
		stderr.Reset()
		if err := executeCmd(deps, stdout, stderr, "search", query); err != nil {
			t.Fatalf("search %q returned error: %v", query, err)
		}
	}
}

func TestSearchCommand_RejectsNonPositiveLimit(t *testing.T) {
	for _, limit := range []string{"0", "-5"} {
		deps, stdout, stderr, _ := setupSearchTest(t)
		called := false
		deps.SearchReader = &spySearchBackend{queryFn: func(ctx context.Context, query string, queryLimit int) ([]graph.Node, error) {
			called = true
			return nil, nil
		}}

		err := executeCmd(deps, stdout, stderr, "search", "--limit", limit, "hello")
		if err == nil || !strings.Contains(err.Error(), "limit must be > 0") {
			t.Fatalf("expected limit validation error for %s, got %v", limit, err)
		}
		if called {
			t.Fatalf("search backend should not be called for invalid limit %s", limit)
		}
	}
}

func TestSearchCommand_NamespaceFromConfig(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	deps, stdout, stderr, _ := setupSearchTest(t)

	var gotNS string
	deps.SearchReader = &spySearchBackend{queryFn: func(ctx context.Context, query string, queryLimit int) ([]graph.Node, error) {
		gotNS = requestctx.FromContext(ctx)
		return nil, nil
	}}

	// Config selects namespace=backend; no --namespace flag is passed.
	viper.Set("namespace", "backend")

	if err := executeCmd(deps, stdout, stderr, "search", "hello"); err != nil {
		t.Fatalf("search: %v", err)
	}
	if gotNS != "backend" {
		t.Fatalf("expected config namespace 'backend', got %q", gotNS)
	}
}

func TestSearchCommand_FlagOverridesConfigNamespace(t *testing.T) {
	viper.Reset()
	defer viper.Reset()

	deps, stdout, stderr, _ := setupSearchTest(t)

	var gotNS string
	deps.SearchReader = &spySearchBackend{queryFn: func(ctx context.Context, query string, queryLimit int) ([]graph.Node, error) {
		gotNS = requestctx.FromContext(ctx)
		return nil, nil
	}}

	// Explicit --namespace must win over the config value.
	viper.Set("namespace", "backend")

	if err := executeCmd(deps, stdout, stderr, "--namespace", "frontend", "search", "hello"); err != nil {
		t.Fatalf("search: %v", err)
	}
	if gotNS != "frontend" {
		t.Fatalf("expected flag namespace 'frontend' to override config, got %q", gotNS)
	}
}

func TestSearchCommand_UsesCommandContext(t *testing.T) {
	deps, stdout, stderr, _ := setupSearchTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	deps.SearchReader = &spySearchBackend{queryFn: func(ctx context.Context, query string, queryLimit int) ([]graph.Node, error) {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("expected canceled command context, got %v", ctx.Err())
		}
		return nil, ctx.Err()
	}}

	err := executeCmdWithContext(ctx, deps, stdout, stderr, "search", "hello")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}
