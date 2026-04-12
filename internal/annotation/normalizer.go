package annotation

import (
	"strings"
)

type Normalizer struct{}

func NewNormalizer() *Normalizer {
	return &Normalizer{}
}

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
	}

	return trimmed
}
