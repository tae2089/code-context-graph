package treesitter

import (
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/tae2089/code-context-graph/internal/model"
)

// TypeScriptSemantics recovers class hierarchy edges from TypeScript heritage clauses.
// @intent emit extends and implements relationships for TypeScript classes without adding language branches to Walker.
type TypeScriptSemantics struct{}

// JavaScriptSemantics recovers class inheritance edges from JavaScript class heritage clauses.
// @intent emit extends relationships for JavaScript classes using the same heritage parsing model as TypeScript.
type JavaScriptSemantics struct{}

// AdditionalEdges adds TypeScript extends and implements edges from class heritage clauses.
// @intent capture TypeScript class hierarchy semantics directly from the parsed AST.
func (TypeScriptSemantics) AdditionalEdges(ctx SemanticContext) []model.Edge {
	if ctx.Root == nil {
		return nil
	}
	var edges []model.Edge
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "class_declaration" {
			className := typescriptClassName(n, ctx.Content)
			if className != "" {
				base, traits := typescriptHeritage(n, ctx.Content)
				if base != "" {
					edges = append(edges, model.Edge{
						Kind:        model.EdgeKindInherits,
						FilePath:    ctx.FilePath,
						Line:        int(n.StartPoint().Row) + 1,
						Fingerprint: fmt.Sprintf("inherits:%s:%s:%s", ctx.FilePath, className, base),
					})
				}
				for _, trait := range traits {
					edges = append(edges, model.Edge{
						Kind:        model.EdgeKindImplements,
						FilePath:    ctx.FilePath,
						Line:        int(n.StartPoint().Row) + 1,
						Fingerprint: fmt.Sprintf("implements:%s:%s:%s", ctx.FilePath, className, trait),
					})
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(ctx.Root)
	return edges
}

// AdditionalEdges adds JavaScript extends edges from class heritage clauses.
// @intent capture JavaScript class inheritance while ignoring TypeScript-only interface semantics.
func (JavaScriptSemantics) AdditionalEdges(ctx SemanticContext) []model.Edge {
	if ctx.Root == nil {
		return nil
	}
	var edges []model.Edge
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "class_declaration" {
			className := javascriptClassName(n, ctx.Content)
			if className != "" {
				base, _ := typescriptHeritage(n, ctx.Content)
				if base != "" {
					edges = append(edges, model.Edge{
						Kind:        model.EdgeKindInherits,
						FilePath:    ctx.FilePath,
						Line:        int(n.StartPoint().Row) + 1,
						Fingerprint: fmt.Sprintf("inherits:%s:%s:%s", ctx.FilePath, className, base),
					})
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(ctx.Root)
	return edges
}

// typescriptClassName extracts the declared class name from a TypeScript class_declaration node.
// @intent isolate TypeScript class-name lookup from heritage parsing logic.
func typescriptClassName(n *sitter.Node, content []byte) string {
	if n == nil {
		return ""
	}
	if nameNode := n.ChildByFieldName("name"); nameNode != nil {
		return strings.TrimSpace(nameNode.Content(content))
	}
	return ""
}

// typescriptHeritage extracts extends and implements names from a TypeScript class declaration.
// @intent parse class_heritage text conservatively so hierarchy edges can be emitted without query changes.
func typescriptHeritage(n *sitter.Node, content []byte) (string, []string) {
	if n == nil {
		return "", nil
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if child == nil || child.Type() != "class_heritage" {
			continue
		}
		return parseTypeScriptHeritageText(child.Content(content))
	}
	return "", nil
}

// parseTypeScriptHeritageText parses a class_heritage snippet into one extends target and zero or more implements targets.
// @intent keep TypeScript inheritance extraction robust even when tree-sitter child field names differ across grammar revisions.
func parseTypeScriptHeritageText(raw string) (string, []string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	var base string
	var traits []string
	implPart := ""
	if before, after, ok := strings.Cut(raw, "implements"); ok {
		raw = strings.TrimSpace(before)
		implPart = strings.TrimSpace(after)
	}
	if after, ok := strings.CutPrefix(raw, "extends"); ok {
		base = firstTypeScriptTypeName(strings.TrimSpace(after))
	}
	if implPart != "" {
		for _, part := range strings.Split(implPart, ",") {
			name := firstTypeScriptTypeName(strings.TrimSpace(part))
			if name != "" {
				traits = append(traits, name)
			}
		}
	}
	return base, traits
}

// firstTypeScriptTypeName normalizes one TypeScript heritage target by trimming generics and surrounding syntax.
// @intent extract stable edge endpoint names from extends/implements clauses.
func firstTypeScriptTypeName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for _, sep := range []string{"<", "{"} {
		if before, _, ok := strings.Cut(value, sep); ok {
			value = strings.TrimSpace(before)
		}
	}
	if fields := strings.Fields(value); len(fields) > 0 {
		return strings.Trim(fields[0], ",")
	}
	return ""
}

// javascriptClassName extracts the declared class name from a JavaScript class_declaration node.
// @intent isolate JavaScript class-name lookup from hierarchy extraction logic.
func javascriptClassName(n *sitter.Node, content []byte) string {
	if n == nil {
		return ""
	}
	if nameNode := n.ChildByFieldName("name"); nameNode != nil {
		return strings.TrimSpace(nameNode.Content(content))
	}
	return ""
}
