// @index Query sanitizers for backend-specific full-text search syntax.
package searchsql

import (
	"strings"
	"unicode"

	"github.com/tae2089/code-context-graph/internal/app/search/identtoken"
	"github.com/tae2089/code-context-graph/internal/app/search/intentrank"
	"github.com/tae2089/code-context-graph/internal/app/search/queryterm"
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
	return buildPrefixQuery(query, alwaysPrefix(`"`+"%s"+`"*`), " AND ", " OR ", " ")
}

// SanitizeIntentFTS5 converts a question into an any-term FTS5 prefix query for
// the intent index.
//
// SanitizeFTS5 requires every term because the searcher there typed identifiers,
// and an identifier a caller half-remembers is still worth demanding in full.
// An intent question is the opposite: it is a sentence aimed at another sentence,
// and no recorded reason will contain all of "why do we verify the signature on a
// push". Requiring every term would answer almost nothing.
//
// Any-term is only safe here because this index holds nothing but recorded
// reasons. The same widening was measured on the shared index and rejected: there
// a common word could match an identifier, a path segment, or a language alias,
// so widening pulled in fifty unrelated nodes. With the names removed, a term can
// only match prose somebody wrote on purpose, and bm25 discounts the words that
// appear in many of those.
// @intent let a sentence-shaped question match a sentence-shaped reason.
// @domainRule any-term matching is confined to the intent index, which holds no identifier text.
func SanitizeIntentFTS5(query string) string {
	return buildPrefixQuery(query, intentTerm(`"`+"%s"+`"*`, `"`+"%s"+`"`), " AND ", " OR ", " OR ")
}

// SanitizePostgresTSQuery converts raw user input into a safe prefix tsquery,
// mirroring SanitizeFTS5 including camelCase sub-token expansion.
// @intent translate free-form user input into a PostgreSQL tsquery that mirrors the SQLite prefix search behavior.
// @domainRule empty or fully stripped input returns an empty query string.
func SanitizePostgresTSQuery(query string) string {
	return buildPrefixQuery(query, alwaysPrefix("%s:*"), " & ", " | ", " & ")
}

// SanitizePostgresIntentTSQuery is the PostgreSQL twin of SanitizeIntentFTS5:
// any term may match, because no recorded reason contains every word of a
// question. See SanitizeIntentFTS5 for why that widening is confined to the
// intent index.
// @intent let a sentence-shaped question match a sentence-shaped reason on PostgreSQL.
// @domainRule any-term matching is confined to the intent index, which holds no identifier text.
func SanitizePostgresIntentTSQuery(query string) string {
	return buildPrefixQuery(query, intentTerm("%s:*", "%s"), " & ", " | ", " | ")
}

// alwaysPrefix renders every term as a prefix, whatever it looks like.
// @intent keep prefix expansion the default for the shared search index.
func alwaysPrefix(termFmt string) func(string) string {
	return func(tok string) string { return strings.Replace(termFmt, "%s", tok, 1) }
}

// intentTerm renders one term of an intent question, choosing prefix or exact
// matching by intentrank.MatchesByPrefix.
//
// The rule lives with the scorer rather than here because both have to apply the
// same one: the index decides what a term matches, the scorer decides what that
// match is worth, and a term matched one way and scored the other would order
// the answer by evidence from a query that never ran.
// @intent keep a short question word from reaching an identifier spelled inside a recorded reason.
// @domainRule only the intent index narrows a term to an exact match.
func intentTerm(prefixFmt, exactFmt string) func(string) string {
	return func(tok string) string {
		if intentrank.MatchesByPrefix(tok) {
			return strings.Replace(prefixFmt, "%s", tok, 1)
		}
		return strings.Replace(exactFmt, "%s", tok, 1)
	}
}

// buildPrefixQuery renders sanitized query terms into a backend's search syntax.
// A camelCase term expands to "(whole OR (sub1 AND sub2 ...))" so it matches
// either the full token or the indexed sub-tokens. `term` renders one lowercased
// term into the backend's syntax and decides whether it matches by prefix;
// `and`/`or` are the backend's operators; `sep` joins the top-level term groups.
// @intent share one injection-safe term expansion policy across SQLite FTS5 and PostgreSQL tsquery syntax.
// @domainRule camelCase input matches either its whole token or the conjunction of its identifier sub-tokens.
func buildPrefixQuery(query string, term func(string) string, and, or, sep string) string {
	raw := queryterm.DropFunctionWords(sanitizeRawTokens(query))
	if len(raw) == 0 {
		return ""
	}
	groups := make([]string, 0, len(raw))
	for _, field := range raw {
		whole := term(strings.ToLower(field))
		subs := identtoken.Split(field)
		if len(subs) <= 1 {
			groups = append(groups, whole)
			continue
		}
		subParts := make([]string, 0, len(subs))
		for _, st := range subs {
			subParts = append(subParts, term(st))
		}
		groups = append(groups, "("+whole+or+"("+strings.Join(subParts, and)+"))")
	}
	return strings.Join(groups, sep)
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
