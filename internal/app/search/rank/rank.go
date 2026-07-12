// Package rank reranks FTS-ranked code-search candidates using
// dependency-free structural signals (name fuzzy similarity, path proximity),
// so both the CLI `search` command and the MCP `search` tool share one ranking.
package rank

import (
	"sort"
	"strings"
	"unicode"

	"github.com/tae2089/code-context-graph/internal/app/search/identtoken"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// Reciprocal Rank Fusion constants.
// rrfK dampens how sharply rank position affects the fused score (60 is the
// conventional value). rrfStructWeight (>1) lets the structural signal override
// a small FTS-rank gap; at weight 1 RRF is symmetric and a two-item rank swap
// always ties, leaving FTS order untouched.
const (
	rrfK            = 60.0
	rrfStructWeight = 2.0
)

// Structural signal weights. Name match is more discriminating than path
// proximity: every node in one file shares the path, so an unweighted max lets
// a path hit saturate the score and bury the exact-name match. Weighting name
// above path keeps name the dominant signal with path as a nudge.
const (
	nameSignalWeight = 1.0
	pathSignalWeight = 0.25
)

// Candidate-pool sizing. Callers over-fetch a wider pool than the requested
// limit so reranking has more than `limit` rows to reorder before bounding.
const (
	fetchFactor = 5
	fetchFloor  = 50
	fetchCap    = 500
)

// FetchLimit widens the candidate pool pulled from FTS so structural reranking
// (and any path filtering) has more than the caller's `limit` rows to reorder;
// the final slice is bounded back to `limit` after reranking.
// @intent retain enough backend candidates for structural relevance signals to affect the caller's bounded result.
// @domainRule candidate pools stay between 50 and 500 rows regardless of the requested result limit.
func FetchLimit(limit int) int {
	return min(max(limit*fetchFactor, fetchFloor), fetchCap)
}

// Rerank reorders FTS-ranked search candidates using structural signals fused
// with the backend rank via Reciprocal Rank Fusion.
//
// @requires nodes is the backend's rank-ordered candidate slice (index == FTS rank).
// @ensures deterministic output; empty query or empty nodes returns the input
// bounded by limit, preserving FTS order.
// @intent combine backend relevance with identifier-name and file-path similarity without losing deterministic FTS tie order.
func Rerank(query string, nodes []graph.Node, limit int) []graph.Node {
	if strings.TrimSpace(query) == "" || len(nodes) == 0 {
		return applyLimit(nodes, limit)
	}
	qTokens := tokenize(query)
	if len(qTokens) == 0 {
		return applyLimit(nodes, limit)
	}

	structScores := make([]float64, len(nodes))
	for i := range nodes {
		structScores[i] = structScore(qTokens, nodes[i])
	}
	structRank := rankDesc(structScores)

	final := make([]float64, len(nodes))
	for i := range nodes {
		final[i] = 1.0/(rrfK+float64(i)) + rrfStructWeight/(rrfK+float64(structRank[i]))
	}

	order := make([]int, len(nodes))
	for i := range order {
		order[i] = i
	}
	// Stable sort by fused score descending; equal scores keep the original
	// FTS order, so ranking stays deterministic.
	sort.SliceStable(order, func(a, b int) bool {
		return final[order[a]] > final[order[b]]
	})

	out := make([]graph.Node, len(nodes))
	for pos, idx := range order {
		out[pos] = nodes[idx]
	}
	return applyLimit(out, limit)
}

// applyLimit bounds the result slice, treating a non-positive limit as unbounded.
// @intent apply the caller's result bound after candidate reranking.
func applyLimit(nodes []graph.Node, limit int) []graph.Node {
	if limit > 0 && len(nodes) > limit {
		return nodes[:limit]
	}
	return nodes
}

// structScore is a weighted sum of the name and path signals, with name
// dominant so an exact-name match outranks a mere same-file (path) match.
// @intent combine identifier and path evidence while keeping identifier similarity dominant.
func structScore(qTokens []string, node graph.Node) float64 {
	return nameSignalWeight*nameSim(qTokens, node) + pathSignalWeight*pathScore(qTokens, node)
}

// nameSim scores fuzzy similarity of the query against the node name and the
// last segment of its qualified name. For each target it takes the stronger of:
//   - token-level: every query token's best match against the whole name or any
//     of its identifier sub-tokens (so "user" or "id" matches getUserById), and
//   - joined-whole: the run-together query vs the whole name (so a typo like
//     "getUsrById" still matches getUserById).
//
// @intent score query tokens against simple and qualified node identifiers with typo tolerance.
func nameSim(qTokens []string, node graph.Node) float64 {
	joined := strings.Join(qTokens, "")
	rawTargets := []string{node.Name, lastSegment(node.QualifiedName, '.')}
	best := 0.0
	for _, raw := range rawTargets {
		if raw == "" {
			continue
		}
		lower := strings.ToLower(raw)
		subs := identtoken.Split(raw) // original case: camelCase boundaries matter
		sum := 0.0
		for _, tok := range qTokens {
			b := normLevSim(tok, lower)
			for _, st := range subs {
				b = max(b, normLevSim(tok, st))
			}
			sum += b
		}
		cand := max(sum/float64(len(qTokens)), normLevSim(joined, lower))
		best = max(best, cand)
	}
	return best
}

// pathScore is the fraction of query tokens that appear as file-path segments.
// @intent use matching path segments as a bounded secondary relevance signal.
func pathScore(qTokens []string, node graph.Node) float64 {
	segs := map[string]struct{}{}
	for _, seg := range strings.FieldsFunc(strings.ToLower(node.FilePath), isPathSep) {
		segs[seg] = struct{}{}
	}
	if len(segs) == 0 {
		return 0
	}
	hits := 0
	for _, tok := range qTokens {
		if _, ok := segs[tok]; ok {
			hits++
		}
	}
	return float64(hits) / float64(len(qTokens))
}

// tokenize lowercases and splits input into alphanumeric tokens.
// @intent normalize free-text search input into comparable Unicode tokens.
func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// @intent recognize separators that delimit meaningful source-path segments.
func isPathSep(r rune) bool {
	switch r {
	case '/', '.', '_', '-':
		return true
	default:
		return false
	}
}

// @intent extract the leaf identifier from a qualified name without allocating intermediate segments.
func lastSegment(s string, sep rune) string {
	if i := strings.LastIndexByte(s, byte(sep)); i >= 0 {
		return s[i+1:]
	}
	return s
}

// rankDesc returns each index's position when scores are ordered descending;
// ties resolve by original index so the ranking is deterministic.
// @intent convert structural scores to deterministic ordinal ranks for reciprocal-rank fusion.
func rankDesc(scores []float64) []int {
	order := make([]int, len(scores))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return scores[order[a]] > scores[order[b]]
	})
	rank := make([]int, len(scores))
	for pos, idx := range order {
		rank[idx] = pos
	}
	return rank
}

// normLevSim is the Levenshtein distance normalized to a [0,1] similarity.
// @intent convert edit distance into a comparable bounded relevance score.
func normLevSim(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	maxLen := max(len(ra), len(rb))
	if maxLen == 0 {
		return 0
	}
	return 1.0 - float64(levenshtein(ra, rb))/float64(maxLen)
}

// levenshtein computes edit distance with a rolling single-row DP.
// @intent compute Unicode-aware edit distance with memory proportional to the second input.
func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr := make([]int, len(b)+1)
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev = curr
	}
	return prev[len(b)]
}
