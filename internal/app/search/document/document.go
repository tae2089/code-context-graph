// @index Pure search-document content construction from graph nodes and annotations.
package document

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/tae2089/code-context-graph/internal/app/search/identtoken"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// Maintenance refreshes persisted search documents and the bound full-text index.
// @intent let inbound orchestration trigger one complete search rebuild without receiving database/backend handles.
type Maintenance interface {
	RefreshDocuments(ctx context.Context) (int, error)
	RebuildIndex(ctx context.Context) error
}

// BuildContent assembles the text indexed for one node's search document.
// @intent combine symbol names, path/language tokens, and annotation evidence without persistence concerns.
func BuildContent(node graph.Node, annotations map[uint]*graph.Annotation) string {
	var builder strings.Builder
	builder.WriteString(node.Name)
	builder.WriteByte(' ')
	builder.WriteString(node.QualifiedName)
	builder.WriteByte(' ')
	builder.WriteString(string(node.Kind))
	for _, token := range identifierSubtokens(node.Name, node.QualifiedName) {
		builder.WriteByte(' ')
		builder.WriteString(token)
	}
	for _, token := range pathTokens(node.FilePath) {
		builder.WriteByte(' ')
		builder.WriteString(token)
	}
	if annotation := annotations[node.ID]; annotation != nil {
		if annotation.Summary != "" {
			builder.WriteByte(' ')
			builder.WriteString(annotation.Summary)
		}
		if annotation.Context != "" {
			builder.WriteByte(' ')
			builder.WriteString(annotation.Context)
		}
		for _, tag := range annotation.Tags {
			builder.WriteByte(' ')
			builder.WriteString(tag.Value)
		}
	}
	return builder.String()
}

// identifierSubtokens returns deduplicated camelCase/separator tokens from node identities.
// @intent improve inner-word recall without inflating term frequency for repeated identity tokens.
func identifierSubtokens(name, qualifiedName string) []string {
	seen := map[string]struct{}{}
	var tokens []string
	for _, raw := range []string{name, qualifiedName} {
		for _, token := range identtoken.Split(raw) {
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			tokens = append(tokens, token)
		}
	}
	return tokens
}

// pathTokens derives lowercase filename segments and an optional language alias.
// @intent make basename, extension, and human language names searchable.
func pathTokens(filePath string) []string {
	base := strings.ToLower(filepath.Base(filePath))
	if base == "" || base == "." {
		return nil
	}
	parts := strings.Split(base, ".")
	tokens := make([]string, 0, len(parts)+1)
	for _, part := range parts {
		if part != "" {
			tokens = append(tokens, part)
		}
	}
	if len(parts) > 1 {
		if alias, ok := languageAlias(parts[len(parts)-1]); ok && alias != parts[len(parts)-1] {
			tokens = append(tokens, alias)
		}
	}
	return tokens
}

// languageAlias maps source extensions to human-friendly search tokens.
// @intent preserve language-name recall for extension-only file paths.
func languageAlias(extension string) (string, bool) {
	switch extension {
	case "go":
		return "go", true
	case "py":
		return "python", true
	case "ts":
		return "typescript", true
	case "java":
		return "java", true
	case "rb":
		return "ruby", true
	case "js":
		return "javascript", true
	case "c":
		return "c", true
	case "cpp":
		return "cpp", true
	case "rs":
		return "rust", true
	case "kt":
		return "kotlin", true
	case "php":
		return "php", true
	case "lua", "luau":
		return "lua", true
	default:
		return "", false
	}
}
