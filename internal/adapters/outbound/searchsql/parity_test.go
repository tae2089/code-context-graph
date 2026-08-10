//go:build fts5 && postgres

package searchsql

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"gorm.io/gorm"

	searchapp "github.com/tae2089/code-context-graph/internal/app/search"
	"github.com/tae2089/code-context-graph/internal/app/search/document"
	"github.com/tae2089/code-context-graph/internal/app/search/evidence"
	"github.com/tae2089/code-context-graph/internal/app/search/rank"
	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// TestBackendParity_SearchAnswersAreIdentical seeds the same corpus into a
// SQLite FTS5 database and a PostgreSQL database, replays every golden query
// from every frozen corpus through the full search service on each, and
// requires the two answers to be identical: same files, same order, same
// hits inside each file.
//
// The deployment gap this guards is real: an agent talking to a laptop's
// SQLite server and the same agent talking to a team's PostgreSQL server must
// get the same answer for the same question, or every measurement made on one
// backend silently stops describing the other. The service was built for this
// — both backends retrieve, the shared reranker orders — and this test is
// where that promise is checked instead of assumed.
//
// Skips when PostgreSQL is not reachable, like every other test in this file's
// build scope. Runs under `-tags "fts5,postgres"` only, because it needs both
// drivers in one binary.
func TestBackendParity_SearchAnswersAreIdentical(t *testing.T) {
	corpora := parityCorpora(t)

	lite := setupTestDB(t)
	// The service queries both indexes concurrently, and every pooled
	// connection to a `:memory:` SQLite sees its own empty database. One
	// connection makes the pool behave like the single file production opens.
	liteSQL, err := lite.DB()
	if err != nil {
		t.Fatal(err)
	}
	liteSQL.SetMaxOpenConns(1)
	pg := setupPostgresDB(t)
	liteBackend := NewSQLiteBackend()
	if err := liteBackend.Migrate(lite); err != nil {
		t.Fatal(err)
	}
	pgBackend := NewPostgresBackend()
	if err := pgBackend.Migrate(pg); err != nil {
		t.Fatal(err)
	}

	liteSearch := searchapp.New(NewReader(lite, liteBackend))
	pgSearch := searchapp.New(NewReader(pg, pgBackend))

	for _, corpus := range corpora {
		t.Run(corpus.namespace, func(t *testing.T) {
			ctx := requestctx.WithNamespace(context.Background(), corpus.namespace)
			seedParityCorpus(t, lite, corpus)
			seedParityCorpus(t, pg, corpus)
			if err := liteBackend.Rebuild(ctx, lite); err != nil {
				t.Fatal(err)
			}
			if err := pgBackend.Rebuild(ctx, pg); err != nil {
				t.Fatal(err)
			}

			for _, query := range corpus.queries {
				// Parity is promised only where the candidate pool is complete.
				// When more rows match than the service fetches, each backend
				// keeps its own top slice — ordered by bm25 or ts_rank, which
				// legitimately disagree — so membership itself becomes
				// backend-specific. Probe the pool and skip such queries.
				fetchLimit := rank.FetchLimit(10)
				if parityPoolTruncated(t, ctx, lite, liteBackend, query, fetchLimit) ||
					parityPoolTruncated(t, ctx, pg, pgBackend, query, fetchLimit) {
					t.Logf("%q: candidate pool exceeds %d, membership is backend-specific by design; skipping", query, fetchLimit)
					continue
				}
				liteList, err := liteSearch.Search(ctx, searchapp.Params{Query: query, Limit: 10})
				if err != nil {
					t.Fatalf("%q on sqlite: %v", query, err)
				}
				pgList, err := pgSearch.Search(ctx, searchapp.Params{Query: query, Limit: 10})
				if err != nil {
					t.Fatalf("%q on postgres: %v", query, err)
				}
				liteAnswer := parityProjection(liteList)
				pgAnswer := parityProjection(pgList)
				if !reflect.DeepEqual(liteAnswer, pgAnswer) {
					t.Errorf("%q: the two backends answered differently\n  sqlite:   %s\n  postgres: %s",
						query, renderParity(liteAnswer), renderParity(pgAnswer))
				}
			}
		})
	}
}

// parityPoolTruncated reports whether the backend's candidate pool for the
// query hit the fetch ceiling, meaning rows beyond it were cut by
// backend-specific relevance order.
func parityPoolTruncated(t *testing.T, ctx context.Context, db *gorm.DB, backend Backend, query string, fetchLimit int) bool {
	t.Helper()
	nodes, err := backend.Query(ctx, db, query, fetchLimit)
	if err != nil {
		t.Fatalf("%q pool probe: %v", query, err)
	}
	return len(nodes) >= fetchLimit
}

// parityAnswer is the part of a search answer an agent acts on: which files,
// in which order, holding which hits, and what was withheld. Node IDs are
// deliberately absent — the two databases assign them independently.
type parityAnswer struct {
	Files        []parityFile
	WeakFiltered int
	Overflow     int
}

type parityFile struct {
	Path string
	Hits []string
}

func parityProjection(list evidence.List) parityAnswer {
	answer := parityAnswer{WeakFiltered: list.WeakFiltered, Overflow: list.OverflowFiles}
	for _, file := range list.Files {
		hits := make([]string, 0, len(file.Hits))
		for _, hit := range file.Hits {
			hits = append(hits, hit.Node.QualifiedName)
		}
		answer.Files = append(answer.Files, parityFile{Path: file.FilePath, Hits: hits})
	}
	return answer
}

// parityCorpus is one frozen corpus reconstructed from the golden fixtures:
// the nodes every captured pool mentions, and the queries to replay.
type parityCorpus struct {
	namespace string
	queries   []string
	nodes     []parityNode
}

type parityNode struct {
	name          string
	qualifiedName string
	kind          string
	filePath      string
	intent        string
	reason        string
}

// parityCorpora loads every golden corpus the rank tests replay. The corpus
// for this test is rebuilt from the frozen candidate pools themselves: every
// node any query's pool mentions is seeded, so the two backends index exactly
// the vocabulary the golden set measures — three codebases' worth.
func parityCorpora(t *testing.T) []parityCorpus {
	t.Helper()
	dirs := []string{goldenDir}
	entries, err := os.ReadDir(goldenDir + "corpora")
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				dirs = append(dirs, goldenDir+"corpora/"+e.Name()+"/")
			}
		}
	}
	corpora := make([]parityCorpus, 0, len(dirs))
	for _, dir := range dirs {
		corpora = append(corpora, loadParityCorpus(t, dir))
	}
	return corpora
}

func loadParityCorpus(t *testing.T, dir string) parityCorpus {
	t.Helper()
	var set struct {
		Corpus struct {
			Namespace string `json:"namespace"`
		} `json:"corpus"`
		Queries []struct {
			Query string `json:"query"`
		} `json:"queries"`
	}
	readParityJSON(t, dir+"queries.json", &set)
	named := map[string][]goldenCandidate{}
	readParityJSON(t, dir+"candidates.json", &named)
	intents := map[string]goldenIntentAnswer{}
	readParityJSON(t, dir+"intent_candidates.json", &intents)

	corpus := parityCorpus{namespace: set.Corpus.Namespace}
	seen := map[string]int{}
	add := func(n parityNode) {
		key := n.kind + "|" + n.qualifiedName + "|" + n.filePath
		if i, ok := seen[key]; ok {
			if corpus.nodes[i].intent == "" {
				corpus.nodes[i].intent = n.intent
			}
			if corpus.nodes[i].reason == "" {
				corpus.nodes[i].reason = n.reason
			}
			return
		}
		seen[key] = len(corpus.nodes)
		corpus.nodes = append(corpus.nodes, n)
	}
	for _, q := range set.Queries {
		corpus.queries = append(corpus.queries, q.Query)
		for _, c := range named[q.Query] {
			add(parityNode{name: c.Name, qualifiedName: c.QualifiedName, kind: c.Kind, filePath: c.FilePath, intent: c.Intent})
		}
		for _, h := range intents[q.Query].Hits {
			add(parityNode{name: h.Name, qualifiedName: h.QualifiedName, kind: h.Kind, filePath: h.FilePath, intent: h.Intent, reason: h.Reason})
		}
	}
	return corpus
}

func readParityJSON(t *testing.T, path string, into any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// seedParityCorpus writes the corpus into one database the way the production
// writer would: the node, its annotation tags, and a search document whose
// content and intent content come from the same document builders the indexer
// uses. Seeding through the builders is what makes the comparison honest —
// both backends index byte-identical text, so any divergence left is the
// backend's own.
func seedParityCorpus(t *testing.T, db *gorm.DB, corpus parityCorpus) {
	t.Helper()
	for _, n := range corpus.nodes {
		node := graph.Node{
			Namespace:     corpus.namespace,
			Name:          n.name,
			QualifiedName: n.qualifiedName,
			Kind:          graph.NodeKind(n.kind),
			FilePath:      n.filePath,
			StartLine:     1,
			EndLine:       2,
			Language:      "go",
		}
		var tags []graph.DocTag
		if n.intent != "" {
			tags = append(tags, graph.DocTag{Kind: graph.TagIntent, Value: n.intent})
		}
		if n.reason != "" && n.reason != n.intent {
			tags = append(tags, graph.DocTag{Kind: graph.TagDomainRule, Value: n.reason})
		}
		if len(tags) > 0 {
			node.Annotation = &graph.Annotation{Tags: tags}
		}
		if err := db.Create(&node).Error; err != nil {
			t.Fatalf("seed %s: %v", n.qualifiedName, err)
		}
		annotations := map[uint]*graph.Annotation{}
		if node.Annotation != nil {
			annotations[node.ID] = node.Annotation
		}
		doc := graph.SearchDocument{
			Namespace:     corpus.namespace,
			NodeID:        node.ID,
			Content:       document.BuildContent(node, annotations),
			IntentContent: document.BuildIntentContent(node, annotations),
			Language:      "go",
		}
		if err := db.Create(&doc).Error; err != nil {
			t.Fatalf("seed doc %s: %v", n.qualifiedName, err)
		}
	}
}

func renderParity(a parityAnswer) string {
	var b strings.Builder
	for _, f := range a.Files {
		b.WriteString(f.Path)
		b.WriteString("[")
		b.WriteString(strings.Join(f.Hits, " "))
		b.WriteString("] ")
	}
	fmt.Fprintf(&b, "weak=%d overflow=%d", a.WeakFiltered, a.Overflow)
	return strings.TrimSpace(b.String())
}
