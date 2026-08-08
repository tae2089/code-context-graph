// Package rank reranks FTS-ranked code-search candidates using
// dependency-free structural signals (name subsequence similarity, path
// proximity), so both the CLI `search` command and the MCP `search` tool share
// one ranking.
package rank

import (
	"cmp"
	"math"
	"sort"
	"strings"
	"unicode"

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

// Candidate-pool sizing. Callers over-fetch a wider pool than the requested
// limit so reranking has more than `limit` rows to reorder before bounding.
const (
	fetchFactor = 5
	fetchFloor  = 50
	fetchCap    = 500
)

// Name-similarity tuning.
//
// The primary scorer requires the query to appear in the name as an ordered
// subsequence, the way an editor fuzzy-finder matches. Names that do not
// contain it score exactly 0, so unrelated identifiers never compete with real
// hits — no similarity threshold has to be guessed. Where each query rune lands
// decides how much it is worth: the start of the name is worth most, the start
// of a sub-word next, a run continuing the previous match next, and a rune
// reached only after skipping others least.
//
// A sub-word starts after a separator (payment_processor, payment-processor),
// at an uppercase rune following a lowercase one (paymentProcessor, and every
// interior boundary of PaymentProcessor), and at the uppercase rune that closes
// an uppercase run (the S of HTTPServer). That last rule is the one an
// upper-after-lower test alone misses, and it is the rule identtoken.Split
// already applies when indexing, so scoring and indexing agree on word starts.
//
// On top of that, every rune skipped *between* two matched runes costs
// gapPenaltyPerRune. Without it a name that merely happens to contain the query
// scattered across it scores close to one that spells it out: "conn" separated
// only canonicalName from connectionPool by 0.03. Runes skipped *before* the
// first match are free, so a query matching the middle of a name (getUserById
// for "user") is not punished for the prefix it did not ask for.
//
// Subsequence matching cannot see a typo that reorders or substitutes runes
// ("reciept" for "receipt"), and it deliberately does not try. Both callers of
// this package — the CLI `search` command and the MCP `search` tool — are driven
// by an agent copying identifiers out of code it has already read, so the query
// is either spelled correctly or names something that does not exist. Edit
// tolerance would only turn the second case into a confident wrong answer.
const (
	bonusNameStart     = 1.0
	bonusWordStart     = 0.8
	bonusConsecutive   = 0.7
	bonusScattered     = 0.3
	gapPenaltyPerRune  = 0.2
	tailPenaltyPerRune = 0.06
)

// noAlignment marks an alignment the scorer cannot reach. It has to lose every
// max() it takes part in, and stay lost after a bonus is added to it, so -Inf is
// the only safe choice: any finite sentinel could be climbed back out of.
var noAlignment = math.Inf(-1)

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
	// A single ranked list: array position is the retrieval rank.
	retrievalRank := make([]int, len(nodes))
	for i := range nodes {
		retrievalRank[i] = i
	}
	return rerankWithRanks(query, nodes, retrievalRank, limit)
}

// rerankWithRanks fuses the caller-supplied retrieval rank of each node with the
// structural signal. Splitting the rank out of the slice position is what lets
// federated search fuse several ranked lists: retrievalRank[i] is node i's rank
// *within its own source list*, not its position in the merged slice.
//
// @requires len(retrievalRank) == len(nodes); each entry is a 0-based rank.
// @intent keep one fusion implementation for both single-list and multi-list retrieval.
func rerankWithRanks(query string, nodes []graph.Node, retrievalRank []int, limit int) []graph.Node {
	if strings.TrimSpace(query) == "" || len(nodes) == 0 {
		return applyLimit(nodes, limit)
	}
	qTokens := tokenize(query)
	if len(qTokens) == 0 {
		return applyLimit(nodes, limit)
	}

	// Name evidence orders the candidates; path proximity only separates names
	// that score the same. Adding a weighted path score to the name score
	// instead would put two incomparable scales on one number: nameSim is
	// continuous, while pathScore is the share of query tokens appearing as
	// path segments and takes only len(qTokens)+1 distinct values — for a
	// one-word query, exactly 0 or 1. Any weight small enough to stop a path
	// hit burying a real name match is still large enough to bury a fainter
	// one, because nameSim has no positive lower bound: at weight 0.05, "adir"
	// against a 74-rune identifier scored 0.0096 and lost. Ranking name first
	// removes that crossover instead of relocating it.
	nameScores := make([]float64, len(nodes))
	pathScores := make([]float64, len(nodes))
	for i := range nodes {
		nameScores[i] = nameSim(qTokens, nodes[i])
		pathScores[i] = pathScore(qTokens, nodes[i])
	}
	structRank := rankBy(len(nodes), func(a, b int) int {
		if by := cmp.Compare(nameScores[b], nameScores[a]); by != 0 {
			return by // any name evidence outranks none, whatever the path says
		}
		return cmp.Compare(pathScores[b], pathScores[a])
	})

	final := make([]float64, len(nodes))
	for i := range nodes {
		final[i] = 1.0/(rrfK+float64(retrievalRank[i])) + rrfStructWeight/(rrfK+float64(structRank[i]))
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

// RerankGroups fuses several independently ranked candidate lists — one per
// namespace in federated search — into a single ordering.
//
// Concatenating the lists and calling Rerank would be wrong: Rerank reads a
// node's array position as its retrieval rank, so the second list's top hit
// would be charged the first list's length. With a 50-row pool that alone costs
// it more than the whole structural signal can repay, and every extra namespace
// makes it worse. Here each node keeps the rank it held inside its own list.
//
// @requires each group is that source's rank-ordered candidate slice.
// @ensures a node's fused score does not depend on which group it came from or
// on the order the groups were supplied; empty groups contribute nothing.
// @intent make federated results comparable across namespaces instead of favouring whichever namespace was queried first.
func RerankGroups(query string, groups [][]graph.Node, limit int) []graph.Node {
	total := 0
	for _, g := range groups {
		total += len(g)
	}
	merged := make([]graph.Node, 0, total)
	retrievalRank := make([]int, 0, total)
	for _, g := range groups {
		for i := range g {
			merged = append(merged, g[i])
			retrievalRank = append(retrievalRank, i)
		}
	}
	return rerankWithRanks(query, merged, retrievalRank, limit)
}

// applyLimit bounds the result slice, treating a non-positive limit as unbounded.
// @intent apply the caller's result bound after candidate reranking.
func applyLimit(nodes []graph.Node, limit int) []graph.Node {
	if limit > 0 && len(nodes) > limit {
		return nodes[:limit]
	}
	return nodes
}

// nameSim scores the query against the node name and the last segment of its
// qualified name. For each target it takes the stronger of:
//   - token-level: the average match of every query token (so "user" or "id"
//     matches getUserById, and a multi-word query needs most of its words), and
//   - joined-whole: the run-together query vs the whole name, which is how a
//     multi-word query reaches the identifier that spells it without separators
//     ("search document" against SearchDocument).
//
// The scale is ordinal, not absolute: scores compare candidates *within one
// query* and nothing anchors them to 1.0. An exact match earns the most its
// query can earn, but how much that is depends on query length and on the
// target's shape — a word-start bonus outweighs a consecutive one, so
// CrossRef outscores crossref for "crossref". The old doc promised 1.0 for an
// exact match, which held only because Jaro-Winkler overwrote the subsequence
// score with its own 1.0. Nothing reads an absolute threshold, so the ordering
// is what has to be right.
//
// @ensures an exact identifier match scores higher than any longer identifier
// containing it; an identifier that does not contain the query as an ordered
// subsequence scores 0.
// @intent score query tokens against simple and qualified node identifiers.
func nameSim(qTokens []string, node graph.Node) float64 {
	joined := strings.Join(qTokens, "")
	targets := []string{node.Name, lastSegment(node.QualifiedName, '.')}
	best := 0.0
	for _, target := range targets {
		if target == "" {
			continue
		}
		sum := 0.0
		for _, tok := range qTokens {
			sum += subsequenceScore(tok, target)
		}
		best = max(best, sum/float64(len(qTokens)))
		if len(qTokens) > 1 { // for one token the joined query is that token
			best = max(best, subsequenceScore(joined, target))
		}
	}
	return best
}

// subsequenceScore rewards each matched rune by where it landed, then divides by
// query length so the result is comparable across queries, and by a tail penalty
// so a longer surrounding identifier scores lower.
//
// It scores the *best* alignment, not the first one found scanning left to
// right. Greedy scanning decides containment correctly but scores it wrong: it
// binds the query's opening rune to the earliest target rune that matches, so
// "processor" anchored to the p of paymentProcessor and then paid a seven-rune
// gap crossing to the word it actually names. That cost real orderings —
// "server" scored startNewServer 0.24 against serviceProvider 0.34, ranking a
// scattered match above the name that spells the word out. Taking the best
// alignment moves that pair to 0.48 against 0.34.
//
// @ensures returns 0 when the query is not an ordered subsequence of the target.
// @intent rank identifiers that contain the query by how prominently they contain it.
func subsequenceScore(query, target string) float64 {
	q := []rune(strings.ToLower(query))
	t := []rune(target)
	if len(q) == 0 || len(t) == 0 || len(q) > len(t) {
		return 0
	}
	lower := []rune(strings.ToLower(target))

	// best[j] holds the score of the best alignment whose last matched query
	// rune sits at target position j; unreachable positions stay at noAlignment.
	// Rolling two rows keeps this O(len(q)*len(t)) time and O(len(t)) space.
	rows := make([]float64, 2*len(t))
	prev, cur := rows[:len(t)], rows[len(t):]
	for qi := range q {
		// carry is the best prev[k] + gapPenaltyPerRune*k over every k that
		// leaves at least one skipped rune before the current position. Folding
		// the penalty into the carry is what keeps the inner loop linear: the
		// gap cost of jumping k -> j splits into a term that depends only on k
		// and one that depends only on j.
		carry := noAlignment
		for tj := range t {
			if tj >= 2 {
				carry = max(carry, prev[tj-2]+gapPenaltyPerRune*float64(tj-2))
			}
			if lower[tj] != q[qi] {
				cur[tj] = noAlignment
				continue
			}
			if qi == 0 {
				// Runes skipped before the first match are free, so a query
				// matching the middle of a name pays nothing for the prefix.
				cur[tj] = matchBonus(t, tj, false)
				continue
			}
			consecutive := noAlignment
			if tj > 0 {
				consecutive = prev[tj-1] + matchBonus(t, tj, true)
			}
			gapped := carry - gapPenaltyPerRune*float64(tj-1) + matchBonus(t, tj, false)
			cur[tj] = max(consecutive, gapped)
		}
		prev, cur = cur, prev
	}

	score := noAlignment
	for tj := range t {
		score = max(score, prev[tj])
	}
	if math.IsInf(score, -1) {
		return 0 // the query is not an ordered subsequence of the target
	}

	score = max(score, 0)
	tail := float64(len(t) - len(q))
	return score / float64(len(q)) / (1.0 + tailPenaltyPerRune*tail)
}

// matchBonus weighs one matched rune by its position in the identifier.
// @intent make a match at a word boundary count for more than one reached by skipping runes.
func matchBonus(target []rune, i int, consecutive bool) float64 {
	switch {
	case i == 0:
		return bonusNameStart
	case unicode.IsUpper(target[i]) && !unicode.IsUpper(target[i-1]):
		return bonusWordStart
	case isIdentSep(target[i-1]):
		return bonusWordStart
	case isAcronymTail(target, i):
		return bonusWordStart
	case consecutive:
		return bonusConsecutive
	default:
		return bonusScattered
	}
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
	return r == '/' || isIdentSep(r)
}

// isAcronymTail reports whether target[i] opens the word that follows an
// uppercase run, the way the S does in HTTPServer. An upper-after-lower test
// cannot see that boundary, because the rune before it is uppercase too. This
// is the same rule identtoken.Split uses to index HTTPServer as http + server,
// so scoring and indexing agree on where the word starts.
// @intent keep acronym-prefixed identifiers scoring like their mixed-case spelling.
func isAcronymTail(target []rune, i int) bool {
	return unicode.IsUpper(target[i]) && unicode.IsUpper(target[i-1]) &&
		i+1 < len(target) && unicode.IsLower(target[i+1])
}

// @intent recognize separators that delimit words inside a single identifier.
func isIdentSep(r rune) bool {
	switch r {
	case '.', '_', '-':
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

// rankBy returns each index's rank when n items are ordered by compare, sharing
// one rank across a group compare calls equal (standard competition ranking:
// 0,1,1,3). compare follows the cmp.Compare convention: negative when a ranks
// ahead of b, zero when they are indistinguishable.
//
// Splitting a tie by array position would smuggle position back into the
// structural signal, and in federated search array position is namespace order —
// a namespace queried later would lose to identical evidence queried first.
//
// @requires compare is a total order: consistent, and equal only for items that
// should share a rank.
// @ensures indistinguishable items receive equal ranks; the rank after a tie
// group of size n skips n-1 values, so rank values stay comparable to a plain
// ordinal ranking.
// @intent convert a structural ordering to deterministic ordinal ranks for reciprocal-rank fusion.
func rankBy(n int, compare func(a, b int) int) []int {
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return compare(order[a], order[b]) < 0
	})
	rank := make([]int, n)
	tieStart := 0
	for pos, idx := range order {
		if pos > 0 && compare(idx, order[pos-1]) != 0 {
			tieStart = pos
		}
		rank[idx] = tieStart
	}
	return rank
}
