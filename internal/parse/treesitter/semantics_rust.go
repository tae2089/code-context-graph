package treesitter

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/tae2089/code-context-graph/internal/model"
)

// RustSemantics normalizes Rust trait impl names beyond raw query captures.
// @intent keep trait implementation edges stable when impl headers contain paths or generic arguments.
type RustSemantics struct{}

// AdditionalEdges returns no Rust-only file-local edges beyond generic walker output.
// @intent satisfy LanguageSemantics while keeping Rust relationship normalization in definition hooks.
func (RustSemantics) AdditionalEdges(SemanticContext) []model.Edge {
	return nil
}

// DefinitionName normalizes Rust impl target type names captured from impl headers.
// @intent keep impl_item class names stable when the captured type includes generic arguments.
func (RustSemantics) DefinitionName(ctx DefinitionContext) string {
	if ctx.Definition == nil || ctx.Definition.Type() != "impl_item" {
		return ctx.Name
	}
	if typ := ctx.Definition.ChildByFieldName("type"); typ != nil {
		if normalized := rustNormalizeTypeName(typ.Content(ctx.Content)); normalized != "" {
			return normalized
		}
	}
	return ctx.Name
}

// ImplementedTypes normalizes trait names captured from Rust impl blocks.
// @intent strip generic arguments and preserve full path segments for trait implementation edges.
func (RustSemantics) ImplementedTypes(ctx DefinitionContext) []string {
	if ctx.Definition == nil || ctx.Definition.Type() != "impl_item" {
		return append([]string(nil), ctx.ImplementedTypes...)
	}
	trait := rustImplTraitName(ctx.Definition, ctx.Content)
	if trait == "" {
		return append([]string(nil), ctx.ImplementedTypes...)
	}
	imports := rustImportAliases(ctx.Root, ctx.Content)
	trait = rustQualifyImportedTypeName(trait, imports)
	return []string{trait}
}

func rustImplTraitName(def *sitter.Node, content []byte) string {
	if def == nil {
		return ""
	}
	trait := def.ChildByFieldName("trait")
	if trait == nil {
		return ""
	}
	return rustNormalizeTypeName(trait.Content(content))
}

func rustNormalizeTypeName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var b strings.Builder
	depth := 0
	for _, r := range raw {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func rustQualifyImportedTypeName(typeName string, imports map[string]string) string {
	typeName = strings.TrimSpace(typeName)
	if typeName == "" || strings.Contains(typeName, "::") {
		return typeName
	}
	if imports != nil {
		if qualified := imports[typeName]; qualified != "" {
			return qualified
		}
	}
	return typeName
}

func rustImportAliases(root *sitter.Node, content []byte) map[string]string {
	if root == nil {
		return nil
	}
	aliases := make(map[string]string)
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "use_declaration" {
			path := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(n.Content(content), "use "), ";"))
			if path != "" && !strings.HasSuffix(path, "::*") {
				aliases[pathBaseName(path, "::")] = path
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	if len(aliases) == 0 {
		return nil
	}
	return aliases
}
