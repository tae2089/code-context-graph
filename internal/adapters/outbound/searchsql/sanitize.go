// @index Query sanitizers for backend-specific full-text search syntax.
package searchsql

import (
	"strings"
	"unicode"

	"github.com/tae2089/code-context-graph/internal/app/search/identtoken"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// sanitizeRawTokens splits raw search input into identifier-like terms,
// preserving original case so camelCase boundaries survive for sub-token splitting.
// @intent expose original-case query terms; lowercasing happens per consumer.
// @domainRule only letter, digit, and underscore sequences survive tokenization.
func sanitizeRawTokens(query string) []string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
	})
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		if field != "" {
			tokens = append(tokens, field)
		}
	}
	return tokens
}

// sanitizeTokens extracts lowercase identifier-like terms from raw search input.
// @intent normalize user queries into backend-safe tokens before they are embedded into FTS syntax.
// @domainRule only letter, digit, and underscore sequences survive tokenization.
func sanitizeTokens(query string) []string {
	raw := sanitizeRawTokens(query)
	tokens := make([]string, 0, len(raw))
	for _, field := range raw {
		tokens = append(tokens, strings.ToLower(field))
	}
	return tokens
}

// SanitizeFTS5 converts raw user input into a safe FTS5 prefix query. A
// camelCase term also matches its sub-tokens, so `getUser` matches either the
// whole token or (`get` AND `user`), mirroring the sub-tokens indexed at build time.
// @intent build SQLite FTS queries that preserve prefix matching without exposing parser-breaking characters.
// @domainRule empty or fully stripped input returns an empty query string.
func SanitizeFTS5(query string) string {
	return buildPrefixQuery(query, `"`+"%s"+`"*`, " AND ", " OR ", " ")
}

// SanitizePostgresTSQuery converts raw user input into a safe prefix tsquery,
// mirroring SanitizeFTS5 including camelCase sub-token expansion.
// @intent translate free-form user input into a PostgreSQL tsquery that mirrors the SQLite prefix search behavior.
// @domainRule empty or fully stripped input returns an empty query string.
func SanitizePostgresTSQuery(query string) string {
	return buildPrefixQuery(query, "%s:*", " & ", " | ", " & ")
}

// buildPrefixQuery renders sanitized query terms into a backend's prefix syntax.
// Each raw term becomes one prefix; a camelCase term expands to
// "(whole OR (sub1 AND sub2 ...))" so it matches either the full token or the
// indexed sub-tokens. `termFmt` formats one lowercased prefix term; `and`/`or`
// are the backend's operators; `sep` joins the top-level term groups.
// @intent share one injection-safe prefix expansion policy across SQLite FTS5 and PostgreSQL tsquery syntax.
// @domainRule camelCase input matches either its whole token or the conjunction of its identifier sub-tokens.
func buildPrefixQuery(query, termFmt, and, or, sep string) string {
	raw := dropFunctionWords(sanitizeRawTokens(query))
	if len(raw) == 0 {
		return ""
	}
	prefix := func(tok string) string { return strings.Replace(termFmt, "%s", tok, 1) }
	groups := make([]string, 0, len(raw))
	for _, field := range raw {
		whole := prefix(strings.ToLower(field))
		subs := identtoken.Split(field)
		if len(subs) <= 1 {
			groups = append(groups, whole)
			continue
		}
		subParts := make([]string, 0, len(subs))
		for _, st := range subs {
			subParts = append(subParts, prefix(st))
		}
		groups = append(groups, "("+whole+or+"("+strings.Join(subParts, and)+"))")
	}
	return strings.Join(groups, sep)
}

// functionWords are English words that carry no meaning in a corpus of code
// identifiers and one-line annotations. Because the terms of a query are joined
// by an implicit AND, one of these in the query makes every result depend on a
// word the searcher did not mean — which is why every question-shaped query
// tested against a real graph returned nothing at all.
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

// dropFunctionWords removes meaningless words from a query's terms, and gives
// up when that would leave nothing.
// @intent stop one unremarkable English word from making every other term in the query conditional on it.
// @domainRule a query made only of function words keeps every term, so it stays answerable.
func dropFunctionWords(tokens []string) []string {
	kept := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if functionWords[strings.ToLower(tok)] {
			continue
		}
		kept = append(kept, tok)
	}
	if len(kept) == 0 {
		return tokens
	}
	return kept
}

// extractExactNameToken returns the single sanitized token eligible for exact-name promotion.
// @intent treat only single-identifier queries as eligible for exact-name promotion.
// @domainRule multi-token queries never produce an exact-name promotion target.
func extractExactNameToken(query string) string {
	tokens := sanitizeTokens(query)
	if len(tokens) != 1 {
		return ""
	}
	return tokens[0]
}

// promoteExactNameMatch moves an exact node-name match to the front of result ordering when present.
// @intent move an exact symbol-name hit to the front of search results to improve precision.
// @mutates nodes slice ordering in place when an exact-name match is promoted.
func promoteExactNameMatch(nodes []graph.Node, query string) []graph.Node {
	target := extractExactNameToken(query)
	if target == "" || len(nodes) < 2 {
		return nodes
	}
	raw := strings.TrimSpace(query)
	if raw != "" {
		for i, node := range nodes {
			if node.Name != raw {
				continue
			}
			if i == 0 {
				return nodes
			}
			promoted := nodes[i]
			copy(nodes[1:i+1], nodes[0:i])
			nodes[0] = promoted
			return nodes
		}
	}
	for i, node := range nodes {
		if strings.ToLower(node.Name) != target {
			continue
		}
		if i == 0 {
			return nodes
		}
		promoted := nodes[i]
		copy(nodes[1:i+1], nodes[0:i])
		nodes[0] = promoted
		return nodes
	}
	return nodes
}
