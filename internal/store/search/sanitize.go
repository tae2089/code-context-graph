// @index Query sanitizers for backend-specific full-text search syntax.
package search

import (
	"strings"
	"unicode"
)

// sanitizeTokens extracts lowercase identifier-like terms from raw search input.
// @intent normalize user queries into backend-safe tokens before they are embedded into FTS syntax.
func sanitizeTokens(query string) []string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
	})
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		if field == "" {
			continue
		}
		tokens = append(tokens, strings.ToLower(field))
	}
	return tokens
}

// SanitizeFTS5 converts raw user input into a safe FTS5 prefix query.
// @intent build SQLite FTS queries that preserve prefix matching without exposing parser-breaking characters.
func SanitizeFTS5(query string) string {
	tokens := sanitizeTokens(query)
	if len(tokens) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		parts = append(parts, "\""+tok+"\"*")
	}
	return strings.Join(parts, " ")
}

// SanitizePostgresTSQuery converts raw user input into a safe prefix tsquery.
// @intent translate free-form user input into a PostgreSQL tsquery that mirrors the SQLite prefix search behavior.
func SanitizePostgresTSQuery(query string) string {
	tokens := sanitizeTokens(query)
	if len(tokens) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		parts = append(parts, tok+":*")
	}
	return strings.Join(parts, " & ")
}
