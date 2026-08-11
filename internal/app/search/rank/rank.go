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
	"unicode/utf8"

	"github.com/tae2089/code-context-graph/internal/app/search/identtoken"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
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

// PoolWidth is how many of the backend's rows the page at offset is cut from, for
// a caller asking for limit files a page.
//
// It is FetchLimit of the whole span the page needs — the offset as well as the
// limit, because no fetch carries a skip — rounded up to a whole number of
// blocks. A block is FetchLimit(limit): the pool one page would need on its own.
//
// Rounding up is what makes paging repeatable. The pool is ordered a block at a
// time (see the search service), so a pool that stops in the middle of a block
// holds a block the backend has more rows for. The next, wider page fills that
// block in and reorders it, and the files the earlier page cut out of the
// half-filled block come back on the later one. Rounding up costs at most one
// block of rows the answer never shows.
//
// The cap is applied in whole blocks too, so the widest pool is as many blocks as
// fit in fetchCap rows — never a full pool with a part of a block on the end.
//
// @requires limit is the same on every page of one walk; a walk that changes it re-cuts the blocks and starts a different answer.
// @ensures the result is a whole number of FetchLimit(limit) blocks, and never narrows as offset grows.
// @intent size a paging caller's pool so the page it already delivered cannot be reshuffled by the next one.
func PoolWidth(offset, limit int) int {
	block := FetchLimit(limit)
	wanted := FetchLimit(max(offset, 0) + limit)
	blocks := min((wanted+block-1)/block, fetchCap/block)
	return max(blocks, 1) * block
}

// Rerank orders FTS candidates by structural evidence — identifier-name
// similarity first, file-path proximity to break its ties — and falls back to
// the backend's own rank only where structure cannot separate two candidates.
//
// The backend rank used to decide part of the order, fused in via Reciprocal
// Rank Fusion. It was measured against the golden set and removed: fusing the
// two scored worse than either input used alone (MRR 0.720 fused, 0.793 by
// structure alone, 21 versus 25 first-place hits over 33 queries). The reason
// is that the pool is a whole FetchLimit wide — up to 500 rows — and a position
// inside it reflects term frequency and document length, which say little about
// which candidate a person meant. Fusion let a 40-place gap in that ordering
// overturn a first-place structural match, which is how an exact name match
// ended up tenth.
//
// @requires nodes is the backend's rank-ordered candidate slice (index == FTS rank).
// @ensures deterministic output; empty query or empty nodes returns the input
// bounded by limit, preserving FTS order.
// @intent order candidates by identifier-name and file-path evidence, using backend rank only as a deterministic tie-break.
func Rerank(query string, nodes []graph.Node, limit int) []graph.Node {
	// A single ranked list: array position is the retrieval rank.
	retrievalRank := make([]int, len(nodes))
	for i := range nodes {
		retrievalRank[i] = i
	}
	return rerankWithRanks(query, nodes, retrievalRank, limit)
}

// rerankWithRanks orders by structural evidence and breaks the remaining ties
// with the caller-supplied retrieval rank. Splitting that rank out of the slice
// position is what lets federated search merge several ranked lists:
// retrievalRank[i] is node i's rank *within its own source list*, not its
// position in the merged slice, so a tie is broken the same way whichever list
// a node arrived from.
//
// @requires len(retrievalRank) == len(nodes); each entry is a 0-based rank.
// @intent keep one ordering implementation for both single-list and multi-list retrieval.
func rerankWithRanks(query string, nodes []graph.Node, retrievalRank []int, limit int) []graph.Node {
	if strings.TrimSpace(query) == "" || len(nodes) == 0 {
		return applyLimit(nodes, limit)
	}
	qTokens := newQueryTokens(query)
	if qTokens.empty() {
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

	// Structural evidence alone decides the order. Candidates it cannot tell
	// apart are ordered by who they are — file path, then qualified name —
	// rather than by the backend's rank, because SQLite's bm25 and PostgreSQL's
	// ts_rank stand equal candidates differently and the same query must give
	// the same answer on both. The retrieval rank remains only for candidates
	// with the same identity, where it keeps the output deterministic.
	order := make([]int, len(nodes))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		x, y := order[a], order[b]
		if structRank[x] != structRank[y] {
			return structRank[x] < structRank[y]
		}
		if by := compareIdentity(nodes[x], nodes[y]); by != 0 {
			return by < 0
		}
		return retrievalRank[x] < retrievalRank[y]
	})

	out := make([]graph.Node, len(nodes))
	for pos, idx := range order {
		out[pos] = nodes[idx]
	}
	return applyLimit(out, limit)
}

// RerankGroups merges several independently ranked candidate lists — one per
// namespace in federated search — into a single ordering.
//
// Concatenating the lists and calling Rerank would be wrong: Rerank reads a
// node's array position as its retrieval rank, so the second list's top hit
// would be charged the first list's length. That rank now only breaks
// structural ties, but a tie is exactly where a namespace's own results should
// not be penalised for being queried second. Here each node keeps the rank it
// held inside its own list.
//
// @requires each group is that source's rank-ordered candidate slice.
// @ensures a node's position does not depend on which group it came from or on
// the order the groups were supplied; empty groups contribute nothing.
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

// compareIdentity orders two structurally tied candidates by who they are.
//
// The key itself lives in the domain, as graph.CompareIdentity, because the
// intent scorer has to break its ties the same way. Two layers of one search
// disagreeing about who comes first is how an answer starts depending on which
// layer produced it.
// @intent break structural ties by node identity so the order never depends on which backend retrieved the pool.
func compareIdentity(a, b graph.Node) int {
	return graph.CompareIdentity(a.Identity(), b.Identity())
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
func nameSim(q queryTokens, node graph.Node) float64 {
	best := scoreTargets(q, node.Name, lastSegment(node.QualifiedName, '.'))
	if receiver := receiverSegment(node.QualifiedName); receiver != "" {
		best = max(best, receiverWeight*scoreTargets(q, receiver))
	}
	return best
}

// receiverWeight discounts the score a method earns from the type it hangs on,
// so that evidence can never equal evidence from the node's own name.
//
// Without a discount every method of SyncQueue ties the SyncQueue type itself on
// the query "syncqueue", and a type with fourteen methods buries itself: the
// reader who typed the type name gets the type somewhere inside a list of its
// own internals. Halving keeps the whole family in the list — which is the
// point, since a method is where the type actually does anything — while
// keeping anything that spells the query in its own name above all of it.
const receiverWeight = 0.5

// receiverSegment returns the type a method hangs on, or "" when the qualified
// name does not describe a method.
//
// It requires three segments, because that is what separates package.Type.Method
// from package.Function. Reading the second-to-last segment unconditionally would
// hand every top-level function its package name as a name target, and then
// typing a package name would score every function in it as a name match. Path
// proximity already measures package closeness, on its own scale.
//
// @ensures a qualified name with fewer than three dot-separated segments has no receiver.
// @intent let a method be found by the type it belongs to, without turning a package name into an identifier match.
func receiverSegment(qualifiedName string) string {
	segments := strings.Split(qualifiedName, ".")
	if len(segments) < 3 {
		return ""
	}
	return segments[len(segments)-2]
}

// scoreTargets returns the best score any of the given identifiers earns for the
// query, using the token-level and joined-whole readings described on nameSim.
//
// The token-level reading is only offered when a sub-token worth more than a
// shared character earned part of it — see queryTokens.meaningfulPart. Averaging
// it unconditionally let one stray rune stand as a candidate's whole evidence.
//
// The joined reading needs no such guard. If the run-together query is an
// ordered subsequence of the target then so is every sub-token it was built
// from, the long ones included, so a joined match can never rest on a stray rune
// alone.
// @intent score one query against several spellings of the same node.
func scoreTargets(q queryTokens, targets ...string) float64 {
	if len(q.parts) == 0 {
		return 0
	}
	joined := strings.Join(q.parts, "")
	best := 0.0
	for _, target := range targets {
		if target == "" {
			continue
		}
		sum := 0.0
		justified := false
		for _, tok := range q.parts {
			hit := subsequenceScore(tok, target)
			sum += hit
			justified = justified || (hit > 0 && q.meaningfulPart(tok))
		}
		if justified {
			best = max(best, sum/float64(len(q.parts)))
		}
		if len(q.parts) > 1 { // for one part the joined query is that part
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

// pathScore is the fraction of the query's whole tokens that appear as
// file-path segments.
//
// It reads the whole tokens, not their sub-tokens, because a segment match is an
// exact one: a query for syncQueue is answered by internal/syncqueue/, which the
// sub-tokens sync and queue would each miss.
// @intent use matching path segments as a bounded secondary relevance signal.
func pathScore(q queryTokens, node graph.Node) float64 {
	if len(q.whole) == 0 {
		return 0
	}
	segs := map[string]struct{}{}
	for _, seg := range strings.FieldsFunc(strings.ToLower(node.FilePath), isPathSep) {
		segs[seg] = struct{}{}
	}
	if len(segs) == 0 {
		return 0
	}
	hits := 0
	for _, tok := range q.whole {
		if _, ok := segs[tok]; ok {
			hits++
		}
	}
	return float64(hits) / float64(len(q.whole))
}

// queryTokens holds the two readings of a query the scorers need, because they
// ask different questions of it.
//
// nameSim compares runes inside an identifier, so it wants the query cut as
// small as the identifier was cut when it was indexed. pathScore compares a
// token against a whole path segment, so it wants the token the searcher typed:
// internal/syncqueue/ holds the segment syncqueue, and nothing in it is spelled
// sync.
//
// One flat list cannot serve both. Keeping the two apart is also what protects
// the joined reading in scoreTargets, which runs the tokens back together with
// no separator between them: the parts of a token rejoin to that token, while
// the parts plus the token they came from rejoin to it written twice —
// getUserId would join to useriduserid, which matches nothing.
type queryTokens struct {
	// parts is the query cut into sub-tokens, for scoring against identifiers.
	parts []string
	// whole is the query cut into whole tokens, for matching path segments.
	whole []string
}

// newQueryTokens reads one query both ways.
//
// parts comes from identtoken.Split, the same cut the index, the full-text query
// and the evidence cut are built from. Reading the query as whole tokens here
// instead was how a camelCase query lost the node full-text search had already
// found for it: the index matched getUserId as get, user and id, while scoring
// asked whether the nine-rune run getuserid appeared inside a seven-rune
// identifier and answered no. The node was retrieved and then dropped for
// failing a test the index never applied.
//
// @ensures parts cuts the query exactly as identifiers are cut when indexed.
// @intent read a query once and hand each scorer the cut it can use.
func newQueryTokens(query string) queryTokens {
	return queryTokens{parts: identtoken.Split(query), whole: tokenize(query)}
}

// empty reports whether the query left nothing to score with.
// @intent give callers one question to ask before scoring a candidate.
func (q queryTokens) empty() bool { return len(q.parts) == 0 && len(q.whole) == 0 }

// meaningfulPart reports whether this sub-token is worth showing a candidate for
// on its own.
//
// A sub-token one rune long is not. Cutting the query the way the index cuts an
// identifier is what lets ExecuteC reach Execute, but it also leaves a trailing
// capital standing as a sub-token of its own, and one rune is a rune most
// identifiers hold somewhere. Cobra's tmplFunc shares the c of ExecuteC and
// nothing else; that scored 0.1056, and any score above zero is all the evidence
// cut asks for, so the query answered with eight declarations instead of three.
//
// One rune is not a threshold picked for these numbers. It is the point below
// which there is nothing to pick: a sub-token has to be at least one rune to
// exist, so this withholds the shortest possible piece and nothing else. A
// length that could be raised — two runes, three — would be fitted to one
// codebase's identifiers, which testdata/README.md forbids.
//
// The exception is a query with nothing longer in it. Someone typing c means the
// identifier c, and that rune is the whole of what they asked rather than the
// leftover of a larger word, so it still has to match. Withholding it there
// would make short names unfindable, a worse fault than the one this fixes.
//
// Two runes was measured before being left alone. The worry is that id, cut out
// of getUserById, is an ordered subsequence of Field and of plenty else. Across
// the four frozen corpora, of the 370 candidates a multi-part query kept on name
// evidence, the number carried by a two-rune piece alone is zero — counting a
// candidate the joined reading cannot reach and whose longest matching piece is
// exactly two runes. Forty-six of those queries do hold a two-rune piece, though
// only gorm's OnConflict is an identifier rather than an English question, and
// its on carried nothing by itself. A rune almost every identifier holds and a
// pair that has to appear in order are not the same risk.
//
// Two things that measurement does not cover, either of which could change the
// answer: a pool is the top ten rows full-text search returned, so a coincidence
// it never retrieved is invisible here, and no corpus holds a query shaped like
// getUserById — the very example the worry is written around. Closing the
// question properly means adding such a query to a corpus and recapturing its
// candidates, which is a larger change than the one it would justify today.
//
// @ensures a query whose sub-tokens are all one rune keeps every one of them.
// @intent stop a single shared character from standing as a candidate's only evidence.
func (q queryTokens) meaningfulPart(part string) bool {
	if utf8.RuneCountInString(part) > 1 {
		return true
	}
	for _, other := range q.parts {
		if utf8.RuneCountInString(other) > 1 {
			return false
		}
	}
	return true
}

// tokenize lowercases and splits input into alphanumeric tokens. Underscore is a
// separator, so user_id becomes two tokens — which is how the identifier index
// splits it, and how a path segment spelled user_id is read.
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
// @intent convert a structural ordering to deterministic ordinal ranks so equally-scored candidates share one rank and fall through to the retrieval tie-break.
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
