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

// BuildReasons lists the texts indexed for one node's recorded reasons, one
// entry per reason.
//
// This is deliberately not BuildContent with a filter. BuildContent answers "what
// is this called", so it mixes the identifier, its split subtokens, and the path
// into the same text as the annotation prose. A question like "why do we verify
// the webhook signature" then competes against every node whose *name* happens to
// contain a query word. Keeping the reason in its own index is what lets that
// question be scored on the reason alone.
//
// One entry per reason rather than one joined string per node, because scoring
// gives a long document less credit per word. Joined, a node's @intent was
// scored on its own length plus the length of every @domainRule beside it, so
// rules the question never touched cost the intent that answered it much of its
// score. Separated, a question that matches one reason is scored on that
// reason's length and nothing else.
//
// Which tag kinds count as a reason is now a list rather than a trade. Adding a
// kind used to lengthen every existing document on that node and cost it score;
// now it only adds documents beside them. reasonKinds is where that decision
// lives.
// @intent index each reason a node exists as its own document, so writing one reason down never costs another its score.
// @return returns one entry per recorded reason in tag order, and nothing when no reason was recorded.
func BuildReasons(node graph.Node, annotations map[uint]*graph.Annotation) []string {
	annotation := annotations[node.ID]
	if annotation == nil {
		return nil
	}
	var reasons []string
	intentTaken := false
	for _, tag := range annotation.Tags {
		if !reasonKinds[tag.Kind] {
			continue
		}
		// A declaration states one purpose, so a second @intent is a writing
		// mistake rather than a list. graph.Node.Intent shows the first one, and
		// indexing the rest would make text findable that can never be shown as
		// the reason it was found by.
		if tag.Kind == graph.TagIntent {
			if intentTaken {
				continue
			}
			intentTaken = true
		}
		value := strings.TrimSpace(tag.Value)
		if value == "" {
			continue
		}
		reasons = append(reasons, value)
	}
	return reasons
}

// reasonKinds are the tag kinds that answer why the code exists. The remaining
// kinds describe what it does or what it takes, which is the name index's job.
// @intent keep the choice of what counts as a reason in one editable place.
var reasonKinds = map[graph.TagKind]bool{
	graph.TagIntent:     true,
	graph.TagDomainRule: true,
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
