package treesitter

import (
	"fmt"
	"slices"
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

// ImplementedTypes normalizes query-captured implements targets through the TypeScript heritage parser.
// @intent keep explicit query captures and AST-derived hierarchy parsing on one normalization path.
func (TypeScriptSemantics) ImplementedTypes(ctx DefinitionContext) []string {
	if ctx.Definition == nil {
		return nil
	}
	base, traits := typescriptHeritage(ctx.Definition, ctx.Content)
	if base == "" && len(traits) == 0 {
		return slices.Clone(ctx.ImplementedTypes)
	}
	return traits
}

// ImplementedTypes returns query-captured relationships unchanged for JavaScript.
// @intent satisfy shared relationship normalization without inventing JS interface semantics.
func (JavaScriptSemantics) ImplementedTypes(ctx DefinitionContext) []string {
	return slices.Clone(ctx.ImplementedTypes)
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
		if base, traits, ok := parseTypeScriptHeritageNode(child, content); ok {
			return base, traits
		}
		return parseTypeScriptHeritageText(child.Content(content))
	}
	return "", nil
}

// parseTypeScriptHeritageNode extracts extends/implements targets from typed heritage children before falling back to text parsing.
// @intent avoid comma-splitting inside generic arguments by preferring grammar-aware node traversal.
func parseTypeScriptHeritageNode(n *sitter.Node, content []byte) (string, []string, bool) {
	if n == nil {
		return "", nil, false
	}
	var base string
	var traits []string
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Type() {
		case "extends_clause":
			if name := firstNamedTypeReference(child, content); name != "" {
				base = name
			}
		case "implements_clause":
			traits = append(traits, namedTypeReferences(child, content)...)
		}
	}
	if base == "" && len(traits) == 0 {
		return "", nil, false
	}
	return base, appendUniquePackageFile(nil, traits...), true
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

// firstNamedTypeReference returns the first normalized type reference beneath a heritage clause child.
// @intent recover stable hierarchy targets from AST nodes instead of brittle text slicing.
func firstNamedTypeReference(n *sitter.Node, content []byte) string {
	refs := namedTypeReferences(n, content)
	if len(refs) == 0 {
		return ""
	}
	return refs[0]
}

// namedTypeReferences returns normalized type names discovered under a heritage-related AST subtree.
// @intent collect direct type reference children while tolerating grammar node-name changes.
func namedTypeReferences(n *sitter.Node, content []byte) []string {
	if n == nil {
		return nil
	}
	var refs []string
	var walk func(*sitter.Node)
	walk = func(cur *sitter.Node) {
		if cur == nil {
			return
		}
		switch cur.Type() {
		case "type_identifier", "nested_type_identifier", "identifier":
			if name := strings.TrimSpace(cur.Content(content)); name != "" {
				refs = append(refs, name)
			}
			return
		case "generic_type", "type_reference", "lookup_type", "member_expression", "qualified_name":
			if name := firstTypeScriptTypeName(cur.Content(content)); name != "" {
				refs = append(refs, name)
				return
			}
		}
		for i := 0; i < int(cur.NamedChildCount()); i++ {
			walk(cur.NamedChild(i))
		}
	}
	walk(n)
	return appendUniquePackageFile(nil, refs...)
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
