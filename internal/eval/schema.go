// @index Data schemas for parser golden corpora and search evaluation reports.
package eval

// EvalNode is the normalized parser node shape used in golden corpora.
// @intent capture corpus-stable node identity independent of absolute paths.
type EvalNode struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line,omitempty"`
}

// Key derives a stable string key for node-level evaluation comparisons.
// @intent provide a deterministic identifier for set-based parser metrics.
func (n EvalNode) Key() string {
	return n.Kind + ":" + n.Name + "@" + n.File
}

// EvalEdge is the normalized parser edge shape used in golden corpora.
// @intent capture corpus-stable edge identity for parser comparisons.
type EvalEdge struct {
	Kind string `json:"kind"`
	From string `json:"from"`
	To   string `json:"to"`
}

// Key derives a stable string key for edge-level evaluation comparisons.
// @intent provide a deterministic identifier for set-based parser metrics.
func (e EvalEdge) Key() string {
	return e.Kind + ":" + e.From + "->" + e.To
}

// GoldenCorpus stores one parser snapshot for a source file in a language corpus.
// @intent persist expected parser output for regression comparison.
type GoldenCorpus struct {
	Language string     `json:"language"`
	File     string     `json:"file"`
	Nodes    []EvalNode `json:"nodes"`
	Edges    []EvalEdge `json:"edges"`
}

// QueryCase defines one search evaluation query and its relevant expected results.
// @intent describe a single ranked-retrieval test case for search evaluation.
type QueryCase struct {
	Query    string   `json:"query"`
	Relevant []string `json:"relevant"`
	K        int      `json:"k,omitempty"`
}

// QueryCorpus groups search evaluation cases loaded from one corpus directory.
// @intent represent the full search evaluation suite as one loadable artifact.
type QueryCorpus struct {
	CorpusDir string      `json:"corpus_dir"`
	Queries   []QueryCase `json:"queries"`
}

// LanguageReport summarizes parser accuracy metrics for one language corpus.
// @intent expose per-language node and edge metrics in the eval report.
type LanguageReport struct {
	Language    string                `json:"language"`
	NodeMetrics ClassificationMetrics `json:"node_metrics"`
	EdgeMetrics ClassificationMetrics `json:"edge_metrics"`
	Files       int                   `json:"files"`
}

// SearchReport holds aggregate ranking metrics across the search evaluation corpus.
// @intent surface average P@K, recall, MRR, and nDCG over all search queries.
type SearchReport struct {
	QueriesTotal int     `json:"queries_total"`
	AvgPAt1      float64 `json:"avg_p_at_1"`
	AvgPAt3      float64 `json:"avg_p_at_3"`
	AvgPAt5      float64 `json:"avg_p_at_5"`
	AvgRecallAt5 float64 `json:"avg_recall_at_5"`
	AvgMRR       float64 `json:"avg_mrr"`
	AvgNDCGAt5   float64 `json:"avg_ndcg_at_5"`
}

// Report bundles parser and search evaluation output into one CLI-facing payload.
// @intent represent the complete eval result returned to CLI and JSON consumers.
type Report struct {
	Suite     string           `json:"suite"`
	Languages []LanguageReport `json:"languages,omitempty"`
	Search    *SearchReport    `json:"search,omitempty"`
}
