// @index Query sanitizers for backend-specific full-text search syntax.
package search

import (
	"strings"
	"unicode"

	"github.com/tae2089/code-context-graph/internal/identtoken"
	"github.com/tae2089/code-context-graph/internal/model"
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
func buildPrefixQuery(query, termFmt, and, or, sep string) string {
	raw := sanitizeRawTokens(query)
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
func promoteExactNameMatch(nodes []model.Node, query string) []model.Node {
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
