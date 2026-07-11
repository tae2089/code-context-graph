// Package identtoken splits source identifiers into lowercased sub-tokens on
// separators, camelCase boundaries, and letter/digit transitions. It is a leaf
// utility shared by search indexing (content generation) and search reranking.
package identtoken

import (
	"strings"
	"unicode"
)

// Split breaks an identifier into lowercased sub-tokens on separators, camelCase
// boundaries, and letter/digit transitions ("getUserById" -> get, user, by, id;
// "HTTPServer" -> http, server; "parseHTML5" -> parse, html, 5).
func Split(s string) []string {
	runes := []rune(s)
	var tokens []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			tokens = append(tokens, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for i, r := range runes {
		if !isAlnum(r) {
			flush()
			continue
		}
		if len(cur) > 0 {
			prev := cur[len(cur)-1]
			switch {
			case unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)):
				flush() // lower/digit -> Upper: new word
			case unicode.IsUpper(r) && unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1]):
				flush() // acronym tail begins a new word (HTTPServer -> http, server)
			case unicode.IsDigit(r) != unicode.IsDigit(prev):
				flush() // letter/digit transition
			}
		}
		cur = append(cur, r)
	}
	flush()
	return tokens
}

func isAlnum(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
