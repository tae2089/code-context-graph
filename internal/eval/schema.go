package eval

type EvalNode struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line,omitempty"`
}

func (n EvalNode) Key() string {
	return n.Kind + ":" + n.Name + "@" + n.File
}

type EvalEdge struct {
	Kind string `json:"kind"`
	From string `json:"from"`
	To   string `json:"to"`
}

func (e EvalEdge) Key() string {
	return e.Kind + ":" + e.From + "->" + e.To
}

type GoldenCorpus struct {
	Language string     `json:"language"`
	File     string     `json:"file"`
	Nodes    []EvalNode `json:"nodes"`
	Edges    []EvalEdge `json:"edges"`
}

type QueryCase struct {
	Query    string   `json:"query"`
	Relevant []string `json:"relevant"`
	K        int      `json:"k,omitempty"`
}

type QueryCorpus struct {
	CorpusDir string      `json:"corpus_dir"`
	Queries   []QueryCase `json:"queries"`
}

type LanguageReport struct {
	Language    string                `json:"language"`
	NodeMetrics ClassificationMetrics `json:"node_metrics"`
	EdgeMetrics ClassificationMetrics `json:"edge_metrics"`
	Files       int                   `json:"files"`
}

type SearchReport struct {
	QueriesTotal int     `json:"queries_total"`
	AvgPAt1      float64 `json:"avg_p_at_1"`
	AvgPAt3      float64 `json:"avg_p_at_3"`
	AvgPAt5      float64 `json:"avg_p_at_5"`
	AvgRecallAt5 float64 `json:"avg_recall_at_5"`
	AvgMRR       float64 `json:"avg_mrr"`
	AvgNDCGAt5   float64 `json:"avg_ndcg_at_5"`
}

type Report struct {
	Suite     string           `json:"suite"`
	Languages []LanguageReport `json:"languages,omitempty"`
	Search    *SearchReport    `json:"search,omitempty"`
}
