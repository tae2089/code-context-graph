package search

import (
	"strings"
	"unicode"
)

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
