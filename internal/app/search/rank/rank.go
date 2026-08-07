// Package rank reranks FTS-ranked code-search candidates using
// dependency-free structural signals (name fuzzy similarity, path proximity),
// so both the CLI `search` command and the MCP `search` tool share one ranking.
package rank

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

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
//
// pathSignalWeight has to sit below the weakest name score worth ranking, or a
// node that merely shares a directory buries a real name match. Abbreviated and
// very long identifiers score low — measured, "cfg" against ccgConfigFileGlobals
// is 0.066 and against loadConfig is 0.188 — so the weight is set under those.
// This is a calibration, not a bound: nameSim has no positive lower limit, so a
// weak enough real match can still lose. Tests pin the measured cases.
const (
	nameSignalWeight = 1.0
	pathSignalWeight = 0.05
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
// Subsequence matching cannot see a typo that reorders runes ("reciept" for
// "receipt"), so Jaro-Winkler runs alongside it and contributes only above
// jwTypoFloor. Measured on unrelated identifiers Jaro-Winkler peaks around
// 0.78, so the floor admits typos without admitting noise.
const (
	bonusNameStart     = 1.0
	bonusWordStart     = 0.8
	bonusConsecutive   = 0.7
	bonusScattered     = 0.3
	gapPenaltyPerRune  = 0.2
	tailPenaltyPerRune = 0.06

	jwTypoFloor   = 0.90
	jwPrefixMax   = 4
	jwPrefixScale = 0.1
)

// jwMinLengthRatio is the shortest length ratio that can still reach
// jwTypoFloor, so anything below it can skip Jaro-Winkler entirely.
//
// With s and L the shorter and longer lengths and r = s/L, at most s runes can
// match and at best none of them are transposed, so
//
//	jaro <= (s/s + s/L + 1) / 3 = (2 + r) / 3
//
// The Winkler prefix boost adds at most jwPrefixMax*jwPrefixScale of the
// remaining headroom, giving jw <= 0.4 + 0.6*jaro = 0.8 + 0.2*r. That reaches
// jwTypoFloor only when r >= 0.5, so the bound is exact and skipping below it
// cannot change a score.
const jwMinLengthRatio = 0.5

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

	structScores := make([]float64, len(nodes))
	for i := range nodes {
		structScores[i] = structScore(qTokens, nodes[i])
	}
	structRank := rankDesc(structScores)

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

// structScore is a weighted sum of the name and path signals, with name
// dominant so an exact-name match outranks a mere same-file (path) match.
// @intent combine identifier and path evidence while keeping identifier similarity dominant.
func structScore(qTokens []string, node graph.Node) float64 {
	return nameSignalWeight*nameSim(qTokens, node) + pathSignalWeight*pathScore(qTokens, node)
}

// nameSim scores fuzzy similarity of the query against the node name and the
// last segment of its qualified name. For each target it takes the stronger of:
//   - token-level: the average match of every query token (so "user" or "id"
//     matches getUserById, and a multi-word query needs most of its words), and
//   - joined-whole: the run-together query vs the whole name (so a typo like
//     "getUsrById" still matches getUserById).
//
// @ensures an exact identifier match scores 1.0; a partial match scores below
// that, decreasing as the surrounding identifier grows; an identifier that does
// not contain the query scores 0.
// @intent score query tokens against simple and qualified node identifiers with typo tolerance.
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
			sum += fuzzySim(tok, target)
		}
		best = max(best, sum/float64(len(qTokens)))
		if len(qTokens) > 1 { // for one token the joined query is that token
			best = max(best, fuzzySim(joined, target))
		}
	}
	return best
}

// fuzzySim scores one query token against one identifier, combining ordered
// subsequence matching with a typo-only Jaro-Winkler contribution.
// @ensures identical strings score 1.0; a target not containing the query as an
// ordered subsequence scores 0 unless it is within typo distance.
// @intent give the name signal a floor of zero for unrelated identifiers while still tolerating typos.
func fuzzySim(query, target string) float64 {
	best := subsequenceScore(query, target)
	if !canReachTypoFloor(query, target) {
		return best
	}
	if jw := jaroWinkler(strings.ToLower(query), strings.ToLower(target)); jw >= jwTypoFloor {
		best = max(best, jw)
	}
	return best
}

// canReachTypoFloor reports whether two strings are close enough in length for
// Jaro-Winkler to possibly reach jwTypoFloor. Counting runes without building
// slices keeps the check cheaper than the call it guards.
// @intent avoid scoring Jaro-Winkler for the many candidates it cannot rate highly anyway.
func canReachTypoFloor(a, b string) bool {
	shorter, longer := utf8.RuneCountInString(a), utf8.RuneCountInString(b)
	if shorter > longer {
		shorter, longer = longer, shorter
	}
	if longer == 0 {
		return false
	}
	return float64(shorter)/float64(longer) >= jwMinLengthRatio
}

// subsequenceScore matches the query greedily left to right and rewards each
// matched rune by where it landed, then divides by query length so the result is
// comparable across queries, and by a tail penalty so a longer surrounding
// identifier scores lower. Greedy matching decides containment exactly but may
// pick a lower-scoring alignment than the best one; the full dynamic-programming
// alignment editor fuzzy-finders use costs more than the ordering gains here.
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

	score, qi, skipped := 0.0, 0, 0
	consecutive := false
	for ti := range t {
		if qi >= len(q) {
			break
		}
		if lower[ti] != q[qi] {
			consecutive = false
			if qi > 0 {
				skipped++ // only gaps *between* matches count
			}
			continue
		}
		score += matchBonus(t, ti, consecutive)
		qi++
		consecutive = true
	}
	if qi < len(q) {
		return 0
	}

	score = max(score-gapPenaltyPerRune*float64(skipped), 0)
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

// jaroWinkler is the Jaro similarity with the standard Winkler boost for a
// shared prefix. It is used only to catch typos that reorder or substitute
// runes, which ordered subsequence matching cannot see.
// @ensures result is in [0,1]; identical strings score 1.0.
// @intent measure near-identity between short identifiers independently of rune order.
func jaroWinkler(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}
	if a == b {
		return 1
	}

	window := max(max(len(ra), len(rb))/2-1, 0)
	matchedA, matchedB := make([]bool, len(ra)), make([]bool, len(rb))
	matches := 0
	for i := range ra {
		for j := max(i-window, 0); j <= min(i+window, len(rb)-1); j++ {
			if matchedB[j] || ra[i] != rb[j] {
				continue
			}
			matchedA[i], matchedB[j] = true, true
			matches++
			break
		}
	}
	if matches == 0 {
		return 0
	}

	// Transpositions: matched runes that pair up out of order.
	transpositions, k := 0, 0
	for i := range ra {
		if !matchedA[i] {
			continue
		}
		for !matchedB[k] {
			k++
		}
		if ra[i] != rb[k] {
			transpositions++
		}
		k++
	}

	m := float64(matches)
	jaro := (m/float64(len(ra)) + m/float64(len(rb)) + (m-float64(transpositions)/2)/m) / 3

	prefix := 0
	for prefix < jwPrefixMax && prefix < len(ra) && prefix < len(rb) && ra[prefix] == rb[prefix] {
		prefix++
	}
	return jaro + float64(prefix)*jwPrefixScale*(1-jaro)
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

// rankDesc returns each index's rank when scores are ordered descending, sharing
// one rank across an equal-score group (standard competition ranking: 0,1,1,3).
//
// Splitting a tie by array position would smuggle position back into the
// structural signal, and in federated search array position is namespace order —
// a namespace queried later would lose to identical evidence queried first.
//
// @ensures equal scores receive equal ranks; the rank after a tie group of size n
// skips n-1 values, so rank values stay comparable to a plain ordinal ranking.
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
	tieStart := 0
	for pos, idx := range order {
		if pos > 0 && scores[idx] != scores[order[pos-1]] {
			tieStart = pos
		}
		rank[idx] = tieStart
	}
	return rank
}
