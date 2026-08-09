// @index BM25 scoring of recorded reasons against a plain-language question, shared by every search backend.
package intentrank

import (
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tae2089/code-context-graph/internal/app/search/identtoken"
	"github.com/tae2089/code-context-graph/internal/app/search/queryterm"
)

// Doc is one candidate the index admitted: a node and the recorded reason that
// was indexed for it.
// @intent carry the exact indexed text into scoring so the score is computed over what was matched.
type Doc struct {
	NodeID  uint
	Content string
}

// Match is one document the question reached, and the terms of the question
// written in it.
// @intent say what earned a document its place, not only that it earned one.
type Match struct {
	NodeID uint
	Terms  []string
}

// Term is one term of the question and how many recorded reasons in the whole
// index hold it.
//
// A term nobody wrote down is reported with a count of zero rather than left
// out, because that is the reader's answer to why the question came back thin.
// @intent let a reader weigh a match by how common the word that earned it is.
type Term struct {
	Text      string
	InReasons int
}

// Result is a ranked answer plus the evidence for it.
// @intent hand back what matched alongside what ranked, so a weak answer can be recognised as one.
type Result struct {
	Matches []Match
	Terms   []Term
	Corpus  int
}

// Rank scores candidate reasons against a question and returns the answer best
// first, at most limit documents of it, with the evidence that produced it.
//
// This runs in Go rather than in the database because the two databases do not
// agree. SQLite's FTS5 orders by bm25, which discounts a word that appears in
// many recorded reasons; PostgreSQL's ts_rank reads one document at a time and
// never learns that a word is common. Same index, same question, different
// answer — and the deployed server runs PostgreSQL while the golden set was
// measured on SQLite. Scoring here gives both backends one answer to be judged
// by, and leaves the databases doing what they both do well: finding candidates.
//
// corpusSize is how many documents the whole index holds, which is what makes a
// word "common". Pass 0 and only the candidates are counted, which overstates
// how rare every term is.
//
// The evidence comes back rather than a confidence score or a cutoff. A cutoff
// would be a number fitted to whichever codebase it was measured on; the term
// counts are recounted against whatever corpus is in front of them, so they mean
// the same thing in a repository nobody has ever measured. "Twenty files, all of
// them matched on `code` alone, and `code` is written in 812 of 1751 recorded
// reasons" is something the reader can act on. A score of 0.31 is not.
//
// @requires docs must be every document the index matched, not a truncated page.
// @return returns matches in answer order, dropping any document no term of the question reaches, and every question term with its corpus count either way.
func Rank(question string, docs []Doc, corpusSize, limit int) Result {
	groups := parseGroups(question)
	if len(groups) == 0 || len(docs) == 0 || limit <= 0 {
		return Result{}
	}

	lengths := make([]int, len(docs))
	freq := make([][]int, len(docs))
	docsWithTerm := make([]int, len(groups))
	totalLength := 0
	for i, doc := range docs {
		tokens := tokenize(doc.Content)
		lengths[i] = len(tokens)
		totalLength += len(tokens)
		freq[i] = make([]int, len(groups))
		for g, group := range groups {
			count := group.count(tokens)
			freq[i][g] = count
			if count > 0 {
				docsWithTerm[g]++
			}
		}
	}
	if totalLength == 0 {
		return Result{}
	}
	averageLength := float64(totalLength) / float64(len(docs))

	// Every document holding any query term is a candidate, because an intent
	// question matches on any term. So counting the candidates counts the whole
	// corpus for these terms exactly — no separate statistics table is needed.
	total := max(corpusSize, len(docs))
	weight := make([]float64, len(groups))
	terms := make([]Term, len(groups))
	for g, seen := range docsWithTerm {
		weight[g] = inverseDocumentFrequency(total, seen)
		terms[g] = Term{Text: groups[g].whole, InReasons: seen}
	}

	type scored struct {
		nodeID uint
		score  float64
		terms  []string
	}
	results := make([]scored, 0, len(docs))
	for i, doc := range docs {
		score := 0.0
		var matched []string
		for g := range groups {
			count := freq[i][g]
			if count == 0 {
				continue
			}
			score += weight[g] * saturate(float64(count), float64(lengths[i]), averageLength)
			matched = append(matched, groups[g].whole)
		}
		if score <= 0 {
			continue
		}
		results = append(results, scored{nodeID: doc.NodeID, score: score, terms: matched})
	}

	// The node id is not a preference, it is the promise that asking for one more
	// row extends the answer instead of reshuffling it. Reasons are one sentence
	// long, so exact ties are common rather than rare.
	sort.SliceStable(results, func(a, b int) bool {
		if results[a].score != results[b].score {
			return results[a].score > results[b].score
		}
		return results[a].nodeID < results[b].nodeID
	})

	matches := make([]Match, 0, min(limit, len(results)))
	for _, result := range results {
		if len(matches) >= limit {
			break
		}
		matches = append(matches, Match{NodeID: result.nodeID, Terms: result.terms})
	}
	return Result{Matches: matches, Terms: terms, Corpus: total}
}

// inverseDocumentFrequency is how much one term is worth: a word written in most
// recorded reasons says almost nothing about which one answers the question, and
// a word written in one says almost everything.
// @intent give a rare term more weight than a common one, which is the whole point of scoring here.
func inverseDocumentFrequency(total, seen int) float64 {
	return math.Log(1 + (float64(total)-float64(seen)+0.5)/(float64(seen)+0.5))
}

// saturate is BM25's term-frequency component: the second mention of a word is
// worth much less than the first, and a long reason gets less credit than a
// short one for saying the same thing.
// @intent stop a long or repetitive reason from outranking a short exact one.
func saturate(count, length, averageLength float64) float64 {
	normalized := k1 * (1 - b + b*length/averageLength)
	return count * (k1 + 1) / (count + normalized)
}

// k1 and b are BM25's standard settings. b controls how hard a long reason is
// penalised; it is worth something here because a node carrying both an @intent
// and a @domainRule is indexed as one longer document than a node carrying only
// an @intent.
const (
	k1 = 1.2
	b  = 0.75
)

// group is one term of the question, plus the sub-tokens a camelCase term splits
// into. It matches a document either as the whole word or as all of its parts,
// mirroring what the query sanitizers ask the index for.
type group struct {
	whole string
	subs  []string
}

// parseGroups turns a question into the terms worth scoring, dropping the words
// that carry no signal.
//
// It reuses the same tokenizer and the same function-word list the query
// sanitizers use. Scoring a different set of terms than the index matched would
// mean ranking one query by another query's evidence.
// @intent score the same terms the index was asked to match.
func parseGroups(question string) []group {
	raw := queryterm.DropFunctionWords(tokenizeKeepingCase(question))
	groups := make([]group, 0, len(raw))
	for _, field := range raw {
		subs := identtoken.Split(field)
		if len(subs) <= 1 {
			subs = nil
		}
		groups = append(groups, group{whole: strings.ToLower(field), subs: subs})
	}
	return groups
}

// count reports how many times this term appears in a document.
// @intent measure one term's presence the same way the index matched it.
func (g group) count(tokens []string) int {
	if whole := countTerm(g.whole, tokens); whole > 0 {
		return whole
	}
	if len(g.subs) == 0 {
		return 0
	}
	// A camelCase term matches its parts only when every part is present, which
	// is what `(whole OR (sub1 AND sub2))` asks the index for. The rarest part
	// bounds how many times the combination can be said to appear.
	fewest := 0
	for i, sub := range g.subs {
		count := countTerm(sub, tokens)
		if count == 0 {
			return 0
		}
		if i == 0 || count < fewest {
			fewest = count
		}
	}
	return fewest
}

// countTerm counts a term's occurrences under the same prefix rule the index used.
// @intent keep the scorer and the index agreeing on what counts as a match.
func countTerm(term string, tokens []string) int {
	if term == "" {
		return 0
	}
	prefix := MatchesByPrefix(term)
	count := 0
	for _, token := range tokens {
		if token == term || (prefix && strings.HasPrefix(token, term)) {
			count++
		}
	}
	return count
}

// MatchesByPrefix reports whether a term of an intent question is long enough to
// prefix-match safely.
//
// A short Latin term is the one that misfires. `get*` reaches only 3 of 1702
// recorded reasons, so it is not a common word any frequency cut would catch —
// but all three matches are the identifiers getAnnotation, getImpactRadius, and
// getAffectedFlows written inside prose, not the word "get" being used.
//
// Length alone is not the rule, because a word boundary is not written the same
// way everywhere. English separates words with a space, so the indexed token
// already is the whole word and a prefix only buys inflections. Korean glues the
// particle onto the noun, so "네임스페이스가" is one token and a prefix is the
// only way a question about "네임스페이스" can reach it. Anything outside ASCII
// keeps prefix matching for that reason.
//
// It is exported because the query sanitizers and this scorer both have to apply
// it: if the index matched a term one way and the score is computed the other,
// the answer is ordered by evidence from a query that never ran.
// @intent measure a term against the misfire that motivated the rule, not against a raw length.
// @domainRule non-ASCII terms always match by prefix, whatever their length.
func MatchesByPrefix(term string) bool {
	for _, r := range term {
		if r > unicode.MaxASCII {
			return true
		}
	}
	return utf8.RuneCountInString(term) >= minPrefixRunes
}

// minPrefixRunes is the shortest ASCII term still allowed to prefix-match in the
// intent index.
// @intent name the boundary the golden set was measured at.
const minPrefixRunes = 4

// tokenizeKeepingCase splits text into identifier-like terms with their original
// case, so camelCase boundaries survive for sub-token splitting.
// @intent expose original-case terms; lowercasing happens per consumer.
// @domainRule only letter, digit, and underscore sequences survive tokenization.
func tokenizeKeepingCase(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
	})
}

// tokenize splits an indexed reason into the lowercase tokens it is scored over.
// @intent read a document the same way the query is read.
func tokenize(text string) []string {
	fields := tokenizeKeepingCase(text)
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		tokens = append(tokens, strings.ToLower(field))
	}
	return tokens
}
