package treesitter

import (
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/tae2089/code-context-graph/internal/model"
)

// JavaSemantics recovers Java extends and implements relationships from class declarations.
// @intent emit Java hierarchy edges directly from class declaration syntax so type resolution can bind them later.
type JavaSemantics struct{}

// KotlinSemantics recovers Kotlin superclass and interface relationships from class declarations.
// @intent emit Kotlin hierarchy edges from declaration text while preserving package-qualified child names.
type KotlinSemantics struct{}

// AdditionalEdges adds Java extends and implements edges from class declarations.
// @intent capture Java class hierarchy semantics with package-qualified child names when available.
func (JavaSemantics) AdditionalEdges(ctx SemanticContext) []model.Edge {
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
			className := javaClassName(n, ctx.Content)
			if className != "" {
				childName := qualifyTypeName(ctx.Package, className)
				imports := importAliasesBySimpleName(ctx.Root, ctx.Content, map[string]struct{}{"import_declaration": {}})
				base, traits := parseJavaClassHierarchy(n.Content(ctx.Content))
				base = qualifyImportedTypeName(base, ctx.Package, imports)
				for i := range traits {
					traits[i] = qualifyImportedTypeName(traits[i], ctx.Package, imports)
				}
				if base != "" {
					edges = append(edges, model.Edge{
						Kind:        model.EdgeKindInherits,
						FilePath:    ctx.FilePath,
						Line:        int(n.StartPoint().Row) + 1,
						Fingerprint: fmt.Sprintf("inherits:%s:%s:%s", ctx.FilePath, childName, base),
					})
				}
				for _, trait := range traits {
					edges = append(edges, model.Edge{
						Kind:        model.EdgeKindImplements,
						FilePath:    ctx.FilePath,
						Line:        int(n.StartPoint().Row) + 1,
						Fingerprint: fmt.Sprintf("implements:%s:%s:%s", ctx.FilePath, childName, trait),
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

// AdditionalEdges adds Kotlin inherits and implements edges from class declarations.
// @intent capture Kotlin supertype relationships by parsing the declaration head after ':'.
func (KotlinSemantics) AdditionalEdges(ctx SemanticContext) []model.Edge {
	if ctx.Root == nil {
		return nil
	}
	var edges []model.Edge
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "class_declaration" || n.Type() == "object_declaration" {
			className := kotlinClassName(n, ctx.Content)
			if className != "" {
				childName := qualifyTypeName(ctx.Package, className)
				imports := importAliasesBySimpleName(ctx.Root, ctx.Content, map[string]struct{}{"import_header": {}})
				base, traits := parseKotlinSupertypes(n.Content(ctx.Content))
				base = qualifyImportedTypeName(base, ctx.Package, imports)
				for i := range traits {
					traits[i] = qualifyImportedTypeName(traits[i], ctx.Package, imports)
				}
				if base != "" {
					edges = append(edges, model.Edge{
						Kind:        model.EdgeKindInherits,
						FilePath:    ctx.FilePath,
						Line:        int(n.StartPoint().Row) + 1,
						Fingerprint: fmt.Sprintf("inherits:%s:%s:%s", ctx.FilePath, childName, base),
					})
				}
				for _, trait := range traits {
					edges = append(edges, model.Edge{
						Kind:        model.EdgeKindImplements,
						FilePath:    ctx.FilePath,
						Line:        int(n.StartPoint().Row) + 1,
						Fingerprint: fmt.Sprintf("implements:%s:%s:%s", ctx.FilePath, childName, trait),
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

// javaClassName extracts the declared class name from a Java class_declaration node.
// @intent isolate Java class-name lookup from hierarchy parsing logic.
func javaClassName(n *sitter.Node, content []byte) string {
	if n == nil {
		return ""
	}
	if nameNode := n.ChildByFieldName("name"); nameNode != nil {
		return strings.TrimSpace(nameNode.Content(content))
	}
	return ""
}

// parseJavaClassHierarchy parses extends and implements targets from a Java class declaration snippet.
// @intent derive Java hierarchy edge endpoints without depending on grammar field-name stability across parser revisions.
func parseJavaClassHierarchy(raw string) (string, []string) {
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
	if beforeBody, _, ok := strings.Cut(raw, "{"); ok {
		raw = strings.TrimSpace(beforeBody)
	}
	if beforeBody, _, ok := strings.Cut(implPart, "{"); ok {
		implPart = strings.TrimSpace(beforeBody)
	}
	if before, after, ok := strings.Cut(raw, "extends"); ok {
		_ = before
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

// qualifyTypeName prefixes a simple type name with the package when the endpoint is not already qualified.
// @intent preserve package context for hierarchy edges so resolvers can bind them deterministically.
func qualifyTypeName(pkgName, typeName string) string {
	typeName = strings.TrimSpace(typeName)
	if typeName == "" || strings.Contains(typeName, ".") || pkgName == "" {
		return typeName
	}
	return pkgName + "." + typeName
}

// qualifyImportedTypeName resolves a simple type name through imports before falling back to the current package.
// @intent let hierarchy edges point to imported types across packages when declarations use short names.
func qualifyImportedTypeName(typeName, pkgName string, imports map[string]string) string {
	typeName = strings.TrimSpace(typeName)
	if typeName == "" || strings.Contains(typeName, ".") {
		return typeName
	}
	if imports != nil {
		if qualified := imports[typeName]; qualified != "" {
			return qualified
		}
	}
	return qualifyTypeName(pkgName, typeName)
}

// importAliasesBySimpleName maps imported simple type names to their fully qualified import paths.
// @intent support cross-package hierarchy resolution by recovering fully qualified imported type names from source imports.
func importAliasesBySimpleName(root *sitter.Node, content []byte, allowedTypes map[string]struct{}) map[string]string {
	if root == nil {
		return nil
	}
	aliases := make(map[string]string)
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if _, ok := allowedTypes[n.Type()]; ok {
			importPath := normalizeImportedTypePath(n.Content(content))
			if importPath != "" {
				aliases[pathBaseName(importPath, ".")] = importPath
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

// normalizeImportedTypePath converts a raw import declaration/header text into a fully qualified type path when possible.
// @intent extract the imported symbol target from Java/Kotlin import syntax for later hierarchy qualification.
func normalizeImportedTypePath(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "import ")
	raw = strings.TrimPrefix(raw, "package ")
	if before, _, ok := strings.Cut(raw, ";"); ok {
		raw = strings.TrimSpace(before)
	}
	if before, _, ok := strings.Cut(raw, " as "); ok {
		raw = strings.TrimSpace(before)
	}
	if strings.HasSuffix(raw, ".*") {
		return ""
	}
	return raw
}

// kotlinClassName extracts the declared class or object name from a Kotlin declaration node.
// @intent isolate Kotlin declaration-name lookup from supertype parsing logic.
func kotlinClassName(n *sitter.Node, content []byte) string {
	if n == nil {
		return ""
	}
	if nameNode := n.ChildByFieldName("name"); nameNode != nil {
		return strings.TrimSpace(nameNode.Content(content))
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if child != nil && (child.Type() == "type_identifier" || child.Type() == "simple_identifier") {
			return strings.TrimSpace(child.Content(content))
		}
	}
	return ""
}

// parseKotlinSupertypes parses one Kotlin declaration head into one superclass and zero or more interfaces.
// @intent distinguish Kotlin constructor-style superclass entries from plain interface references.
func parseKotlinSupertypes(raw string) (string, []string) {
	raw = strings.TrimSpace(raw)
	idx := kotlinHierarchyColonIndex(raw)
	if idx < 0 {
		return "", nil
	}
	head := strings.TrimSpace(raw[idx+1:])
	if beforeBody, _, ok := strings.Cut(head, "{"); ok {
		head = strings.TrimSpace(beforeBody)
	}
	if beforeWhere, _, ok := strings.Cut(head, "where"); ok {
		head = strings.TrimSpace(beforeWhere)
	}
	if head == "" {
		return "", nil
	}
	var base string
	var traits []string
	for _, part := range strings.Split(head, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if strings.Contains(name, " by ") {
			before, _, _ := strings.Cut(name, " by ")
			name = strings.TrimSpace(before)
		}
		isSuperclass := strings.Contains(name, "(")
		name = normalizeKotlinSupertypeName(name)
		if name == "" {
			continue
		}
		if isSuperclass && base == "" {
			base = name
			continue
		}
		traits = append(traits, name)
	}
	return base, traits
}

// kotlinHierarchyColonIndex finds the ':' that starts Kotlin supertype declarations, skipping constructor/type-annotation colons.
// @intent avoid confusing primary-constructor parameter types with the superclass separator.
func kotlinHierarchyColonIndex(raw string) int {
	depthParen := 0
	depthAngle := 0
	inString := false
	escaped := false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '(':
			depthParen++
		case ')':
			if depthParen > 0 {
				depthParen--
			}
		case '<':
			depthAngle++
		case '>':
			if depthAngle > 0 {
				depthAngle--
			}
		case ':':
			if depthParen == 0 && depthAngle == 0 {
				return i
			}
		}
	}
	return -1
}

// normalizeKotlinSupertypeName strips constructor calls and type arguments from a Kotlin supertype reference.
// @intent derive stable edge endpoint names from Kotlin declaration heads.
func normalizeKotlinSupertypeName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if before, _, ok := strings.Cut(value, "("); ok {
		value = strings.TrimSpace(before)
	}
	if before, _, ok := strings.Cut(value, "<"); ok {
		value = strings.TrimSpace(before)
	}
	if fields := strings.Fields(value); len(fields) > 0 {
		return fields[0]
	}
	return ""
}
