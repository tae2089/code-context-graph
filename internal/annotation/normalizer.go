package annotation

import (
	"strings"
)

// Normalizer strips language-specific comment syntax from raw documentation blocks.
// @intent normalize comment text before annotation parsing across supported languages
type Normalizer struct{}

// NewNormalizer creates a Normalizer.
// @intent provide a reusable comment normalizer for annotation extraction
func NewNormalizer() *Normalizer {
	return &Normalizer{}
}

// Normalize removes comment delimiters and line prefixes from raw comment text.
// @intent turn raw source comments into plain text consumable by the annotation parser
// @requires language matches the source file language for correct delimiter handling
// @ensures returned text has no leading comment markers and no trailing blank lines
// @see annotation.stripBlockDelimiters
// @see annotation.stripLinePrefix
func (n *Normalizer) Normalize(text string, language string) string {
	if text == "" {
		return ""
	}

	text = stripBlockDelimiters(text, language)

	lines := strings.Split(text, "\n")
	var result []string

	for _, line := range lines {
		if language == "go" && isGoDirective(line) {
			continue
		}
		stripped := stripLinePrefix(line, language)
		stripped = strings.TrimRight(stripped, " \t")
		if stripped != "" || len(result) > 0 {
			result = append(result, stripped)
		}
	}

	for len(result) > 0 && result[len(result)-1] == "" {
		result = result[:len(result)-1]
	}

	return strings.Join(result, "\n")
}

// isGoDirective reports whether a line is a Go compiler pragma like `//go:generate`.
// @intent exclude `//go:*` pragma lines from annotation normalization so tag values stay clean
func isGoDirective(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "//go:") {
		return false
	}
	rest := trimmed[len("//go:"):]
	if rest == "" {
		return false
	}
	c := rest[0]
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

// stripBlockDelimiters removes outer block-comment wrappers for the given language.
// @intent keep only the inner documentation payload from block-style comments
func stripBlockDelimiters(text string, language string) string {
	switch language {
	case "go", "java", "cpp", "c", "csharp", "typescript", "javascript", "kotlin", "swift", "scala", "php":
		if stripped, ok := strings.CutPrefix(text, "/**"); ok {
			text = stripped
			text = strings.TrimSuffix(strings.TrimSpace(text), "*/")
		} else if stripped, ok := strings.CutPrefix(text, "/*"); ok {
			text = stripped
			text = strings.TrimSuffix(strings.TrimSpace(text), "*/")
		}
	case "python":
		if stripped, ok := stripPythonDocstringDelimiters(text); ok {
			return stripped
		}
	}
	return text
}

func stripPythonDocstringDelimiters(text string) (string, bool) {
	for _, quote := range []string{"\"\"\"", "'''"} {
		if stripped, ok := stripPythonQuotedString(text, quote); ok {
			return stripped, true
		}
	}
	return "", false
}

func stripPythonQuotedString(text string, quote string) (string, bool) {
	lower := strings.ToLower(text)
	idx := strings.Index(lower, quote)
	if idx < 0 {
		return "", false
	}

	prefix := lower[:idx]
	if !isSupportedPythonDocstringPrefix(prefix) {
		return "", false
	}
	if !strings.HasSuffix(lower, quote) {
		return "", false
	}

	return text[idx+len(quote) : len(text)-len(quote)], true
}

func isSupportedPythonDocstringPrefix(prefix string) bool {
	if prefix == "" {
		return true
	}
	return prefix == "r" || prefix == "u"
}

// stripLinePrefix removes one line-level comment marker from a comment line.
// @intent normalize individual documentation lines across language comment syntaxes
func stripLinePrefix(line string, language string) string {
	trimmed := strings.TrimLeft(line, " \t")

	switch language {
	case "go", "cpp", "c", "csharp", "typescript", "javascript", "kotlin", "swift", "scala", "java":
		if strings.HasPrefix(trimmed, "// ") {
			return trimmed[3:]
		}
		if strings.HasPrefix(trimmed, "//") {
			return trimmed[2:]
		}
		if strings.HasPrefix(trimmed, "* ") {
			return trimmed[2:]
		}
		if trimmed == "*" {
			return ""
		}
	case "php":
		if strings.HasPrefix(trimmed, "// ") {
			return trimmed[3:]
		}
		if strings.HasPrefix(trimmed, "//") {
			return trimmed[2:]
		}
		if strings.HasPrefix(trimmed, "# ") {
			return trimmed[2:]
		}
		if strings.HasPrefix(trimmed, "#") {
			return trimmed[1:]
		}
		if strings.HasPrefix(trimmed, "* ") {
			return trimmed[2:]
		}
		if trimmed == "*" {
			return ""
		}
	case "python", "ruby":
		if strings.HasPrefix(trimmed, "# ") {
			return trimmed[2:]
		}
		if strings.HasPrefix(trimmed, "#") {
			return trimmed[1:]
		}
	case "lua":
		if strings.HasPrefix(trimmed, "-- ") {
			return trimmed[3:]
		}
		if strings.HasPrefix(trimmed, "--") {
			return trimmed[2:]
		}
	case "rust":
		if strings.HasPrefix(trimmed, "/// ") {
			return trimmed[4:]
		}
		if strings.HasPrefix(trimmed, "///") {
			return trimmed[3:]
		}
		if strings.HasPrefix(trimmed, "// ") {
			return trimmed[3:]
		}
		if strings.HasPrefix(trimmed, "//") {
			return trimmed[2:]
		}
	}

	return trimmed
}
