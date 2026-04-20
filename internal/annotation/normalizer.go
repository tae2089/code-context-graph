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

// stripBlockDelimiters removes outer block-comment wrappers for the given language.
// @intent keep only the inner documentation payload from block-style comments
func stripBlockDelimiters(text string, language string) string {
        switch language {
        case "go", "java", "cpp", "c", "csharp", "typescript", "javascript", "kotlin", "swift", "scala":
                if strings.HasPrefix(text, "/**") {
                        text = strings.TrimPrefix(text, "/**")
                        text = strings.TrimSuffix(strings.TrimSpace(text), "*/")
                } else if strings.HasPrefix(text, "/*") {
                        text = strings.TrimPrefix(text, "/*")
                        text = strings.TrimSuffix(strings.TrimSpace(text), "*/")
                }
        case "python":
                if strings.HasPrefix(text, `"""`) && strings.HasSuffix(text, `"""`) {
                        text = strings.TrimPrefix(text, `"""`)
                        text = strings.TrimSuffix(text, `"""`)
                } else if strings.HasPrefix(text, "'''") && strings.HasSuffix(text, "'''") {
                        text = strings.TrimPrefix(text, "'''")
                        text = strings.TrimSuffix(text, "'''")
                }
        }
        return text
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
