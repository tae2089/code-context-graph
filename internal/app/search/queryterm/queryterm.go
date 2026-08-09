// Package queryterm holds the query-side vocabulary rules that both ways of
// searching this graph have to agree on: the full-text query built for the
// database, and the term list the fallback scan matches nodes against. A word
// dropped by one and kept by the other makes the two tools disagree about what
// the searcher asked for.
package queryterm

import "strings"

// functionWords are English words that carry no meaning in a corpus of code
// identifiers and one-line annotations. They hurt both searches, in opposite
// ways. In the full-text query the terms are joined by an implicit AND, so one
// of these makes every result depend on a word the searcher did not mean —
// which is why every question-shaped query tested against a real graph returned
// nothing at all. In the fallback scan the terms are ORed and each match adds
// score, so `the` alone lifts most of the corpus onto the page.
//
// Words that are also common code terms are deliberately absent: get, set,
// list, new, run, call, has, all, add, use, type, error. Removing those would
// silently break searches for the identifiers that spell them.
//
// This is a fixed list rather than a rarity measurement on purpose. Rarity
// picks out the wrong words here: `how` appears in 8 of 1338 stored summaries
// and `what` in none, so a rarity score treats both as highly meaningful and
// weights the query towards them.
var functionWords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "to": true, "in": true,
	"on": true, "at": true, "by": true, "for": true, "with": true, "from": true,
	"as": true, "is": true, "are": true, "was": true, "were": true, "be": true,
	"do": true, "does": true, "did": true, "how": true, "what": true,
	"where": true, "when": true, "why": true, "who": true, "which": true,
	"i": true, "my": true, "me": true, "it": true, "its": true, "this": true,
	"that": true, "and": true, "or": true, "can": true, "could": true,
	"would": true, "should": true, "will": true, "shall": true, "there": true,
}

// IsFunctionWord reports whether a term is one of the meaningless words.
// @intent let a caller judge one term without copying the list.
func IsFunctionWord(term string) bool {
	return functionWords[strings.ToLower(term)]
}

// DropFunctionWords removes meaningless words from a query's terms, and gives
// up when that would leave nothing.
// @intent stop one unremarkable English word from deciding which results a query returns.
// @domainRule a query made only of function words keeps every term, so it stays answerable.
func DropFunctionWords(tokens []string) []string {
	kept := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if IsFunctionWord(tok) {
			continue
		}
		kept = append(kept, tok)
	}
	if len(kept) == 0 {
		return tokens
	}
	return kept
}
