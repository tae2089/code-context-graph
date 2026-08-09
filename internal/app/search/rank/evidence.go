package rank

import (
	"strings"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// Structural holds the two scores Rerank orders candidates by, for one
// candidate, so a caller can say *why* a result is in the list.
//
// The numbers are the same ones the ranker uses, computed the same way, and
// they carry the same caveat: they compare candidates within one query and
// nothing anchors them to 1.0. Read them as "is there any evidence, and which
// kind", not as a confidence.
// @intent let a caller explain a search result using the ranker's own signals instead of re-deriving them.
type Structural struct {
	Name float64
	Path float64
}

// Any reports whether the query left any structural trace on this node.
// @intent give callers one question to ask before deciding a candidate is unexplainable.
func (s Structural) Any() bool { return s.Name > 0 || s.Path > 0 }

// Signals scores one node against a query the way Rerank does.
// @requires the caller passes the same raw query string the search ran with.
// @ensures a blank query, or one with no usable tokens, scores zero on both signals.
// @intent expose the ranker's per-candidate evidence to the code that builds a result list.
func Signals(query string, node graph.Node) Structural {
	if strings.TrimSpace(query) == "" {
		return Structural{}
	}
	qTokens := tokenize(query)
	if len(qTokens) == 0 {
		return Structural{}
	}
	return Structural{Name: nameSim(qTokens, node), Path: pathScore(qTokens, node)}
}
