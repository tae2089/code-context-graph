// Package identtoken is the one place search text turns into tokens: Fields
// cuts free text into identifier-like terms, and Split cuts one identifier
// into its sub-tokens. It is a leaf utility shared by search indexing,
// query sanitizing, and both scorers, because two copies of a tokenizer is
// how a query and the index it searches stop reading text the same way.
package identtoken

import (
	"strings"
	"unicode"
)

// Fields splits free text into identifier-like terms with their original case,
// so camelCase boundaries survive for sub-token splitting by Split.
// @intent expose original-case terms; lowercasing happens per consumer.
// @domainRule only letter, digit, and underscore sequences survive tokenization.
func Fields(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_')
	})
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// FieldsLower splits free text into lowercase identifier-like terms, which is
// how both indexed documents and the queries aimed at them are read.
// @intent read a document the same way the query is read.
func FieldsLower(text string) []string {
	fields := Fields(text)
	if len(fields) == 0 {
		return nil
	}
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		tokens = append(tokens, strings.ToLower(field))
	}
	return tokens
}

// Split breaks an identifier into lowercased sub-tokens on separators, camelCase
// boundaries, and letter/digit transitions ("getUserById" -> get, user, by, id;
// "HTTPServer" -> http, server; "parseHTML5" -> parse, html, 5).
// @intent normalize source identifiers into stable search-index tokens without language-specific dependencies.
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

// @intent keep identifier tokenization limited to Unicode letters and digits.
func isAlnum(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
