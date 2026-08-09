package searchsql

import (
	"encoding/json"
	"flag"
	"os"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// TestCaptureIntentCorpus freezes the annotated corpus that the intent golden
// set replays.
//
// The ranking fixture freezes the *result* of a query, because the thing under
// test there is rank.Rerank, which runs after retrieval. Intent has no reranker:
// the order comes straight out of the FTS index, so freezing the query output
// would freeze the only thing being measured. This captures the corpus instead,
// and the harness rebuilds a real index from it and asks the real question.
//
// It is skipped unless -capture-intent is passed and it needs a built graph at
// the repository root, which the test cannot create:
//
//	go test -tags fts5 ./internal/adapters/outbound/searchsql/ \
//	  -run TestCaptureIntentCorpus -capture-intent -count=1
func TestCaptureIntentCorpus(t *testing.T) {
	if !*captureIntent {
		t.Skip("pass -capture-intent to rewrite the intent corpus fixture")
	}
	db, err := gorm.Open(sqlite.Open("file:"+goldenGraphPath+"?immutable=1&mode=ro"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open %s: %v", goldenGraphPath, err)
	}

	// search_documents decides membership, not nodes: it is already the set of
	// kinds the index can reach. Taking nodes instead would put packages into
	// the corpus, and coverage would then report a denominator production never
	// counts.
	var docs []graph.SearchDocument
	if err := db.Where("namespace = ?", intentCorpusNamespace).Order("node_id").Find(&docs).Error; err != nil {
		t.Fatalf("read search documents: %v", err)
	}
	if len(docs) == 0 {
		t.Fatalf("namespace %q has no search documents in %s", intentCorpusNamespace, goldenGraphPath)
	}
	ids := make([]uint, len(docs))
	for i, d := range docs {
		ids[i] = d.NodeID
	}

	var nodes []graph.Node
	if err := db.Where("id IN ?", ids).Preload("Annotation.Tags").Order("id").Find(&nodes).Error; err != nil {
		t.Fatalf("read nodes: %v", err)
	}

	outDegree := intentEdgeCounts(t, db, "from_node_id")
	inDegree := intentEdgeCounts(t, db, "to_node_id")

	corpus := intentCorpus{Namespace: intentCorpusNamespace, Nodes: make([]intentCorpusNode, 0, len(nodes))}
	withReason := 0
	for _, n := range nodes {
		record := intentCorpusNode{
			ID:            n.ID,
			Name:          n.Name,
			QualifiedName: n.QualifiedName,
			Kind:          string(n.Kind),
			FilePath:      n.FilePath,
			StartLine:     n.StartLine,
			Language:      n.Language,
			OutEdges:      outDegree[n.ID],
			InEdges:       inDegree[n.ID],
		}
		if n.Annotation != nil {
			for _, tag := range n.Annotation.Tags {
				if tag.Kind != graph.TagIntent && tag.Kind != graph.TagDomainRule {
					continue // only these two reach the intent index
				}
				record.Tags = append(record.Tags, intentCorpusTag{Kind: string(tag.Kind), Value: tag.Value, Ordinal: tag.Ordinal})
			}
		}
		if len(record.Tags) > 0 {
			withReason++
		}
		corpus.Nodes = append(corpus.Nodes, record)
	}

	blob, err := json.MarshalIndent(corpus, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(intentDir+"intent_corpus.json", append(blob, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("intent_corpus.json rewritten: %d declarations, %d with a recorded reason", len(corpus.Nodes), withReason)
}

// intentEdgeCounts counts edges per node on one end, so the harness can tell an
// entry point that can be walked from a dead end.
func intentEdgeCounts(t *testing.T, db *gorm.DB, column string) map[uint]int {
	t.Helper()
	var rows []struct {
		NodeID uint
		Total  int
	}
	if err := db.Model(&graph.Edge{}).
		Select(column+" AS node_id, count(*) AS total").
		Where("namespace = ?", intentCorpusNamespace).
		Group(column).
		Scan(&rows).Error; err != nil {
		t.Fatalf("count edges by %s: %v", column, err)
	}
	counts := make(map[uint]int, len(rows))
	for _, r := range rows {
		counts[r.NodeID] = r.Total
	}
	return counts
}

var captureIntent = flag.Bool("capture-intent", false, "rewrite the intent corpus fixture from a local graph")

const (
	intentDir = "testdata/"
	// intentCorpusNamespace is the namespace `ccg build .` writes at the
	// repository root, which is the only graph this corpus is captured from.
	intentCorpusNamespace = "ccg"
)

// intentCorpus is the frozen annotated snapshot the intent golden set rebuilds
// an index from.
type intentCorpus struct {
	Namespace string             `json:"namespace"`
	Nodes     []intentCorpusNode `json:"nodes"`
}

// intentCorpusNode carries everything needed to reseed one declaration: the
// identity an answer reports, the tags the index is built from, and the edge
// counts that say whether a caller could walk anywhere from it.
type intentCorpusNode struct {
	ID            uint              `json:"id"`
	Name          string            `json:"name"`
	QualifiedName string            `json:"qualified_name"`
	Kind          string            `json:"kind"`
	FilePath      string            `json:"file_path"`
	StartLine     int               `json:"start_line"`
	Language      string            `json:"language,omitempty"`
	OutEdges      int               `json:"out_edges"`
	InEdges       int               `json:"in_edges"`
	Tags          []intentCorpusTag `json:"tags,omitempty"`
}

type intentCorpusTag struct {
	Kind    string `json:"kind"`
	Value   string `json:"value"`
	Ordinal int    `json:"ordinal"`
}
