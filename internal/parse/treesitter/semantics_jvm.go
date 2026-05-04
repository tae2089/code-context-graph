package treesitter

import (
	"fmt"
	"slices"
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
				base, traits := javaClassHierarchy(n, ctx.Content)
				base = qualifyImportedTypeName(base, ctx.Package, imports)
				for i := range traits {
					traits[i] = qualifyImportedTypeName(traits[i], ctx.Package, imports)
				}
				if base != "" {
					edges = append(edges, model.Edge{
						Kind:        model.EdgeKindInherits,
						FilePath:    ctx.FilePath,
						Line:        int(n.StartPoint().Row) + 1,
						Fingerprint: model.BuildInheritsFingerprintV2(ctx.FilePath, childName, base),
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

// ImplementedTypes normalizes query-captured implements targets through Java hierarchy parsing.
// @intent keep generic-safe relationship extraction consistent between direct hierarchy parsing and query captures.
func (JavaSemantics) ImplementedTypes(ctx DefinitionContext) []string {
	if ctx.Definition == nil {
		return nil
	}
	_, traits := javaClassHierarchy(ctx.Definition, ctx.Content)
	if len(traits) == 0 {
		return slices.Clone(ctx.ImplementedTypes)
	}
	imports := importAliasesBySimpleName(ctx.Root, ctx.Content, map[string]struct{}{"import_declaration": {}})
	for i := range traits {
		traits[i] = qualifyImportedTypeName(traits[i], ctx.Package, imports)
	}
	return traits
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
				base, traits := kotlinSupertypes(n, ctx.Content)
				base = qualifyImportedTypeName(base, ctx.Package, imports)
				for i := range traits {
					traits[i] = qualifyImportedTypeName(traits[i], ctx.Package, imports)
				}
				if base != "" {
					edges = append(edges, model.Edge{
						Kind:        model.EdgeKindInherits,
						FilePath:    ctx.FilePath,
						Line:        int(n.StartPoint().Row) + 1,
						Fingerprint: model.BuildInheritsFingerprintV2(ctx.FilePath, childName, base),
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

// ImplementedTypes normalizes Kotlin supertype interfaces through the same parsing path used for hierarchy edges.
// @intent keep declaration-time and query-time interface extraction aligned for Kotlin.
func (KotlinSemantics) ImplementedTypes(ctx DefinitionContext) []string {
	if ctx.Definition == nil {
		return nil
	}
	_, traits := kotlinSupertypes(ctx.Definition, ctx.Content)
	if len(traits) == 0 {
		return slices.Clone(ctx.ImplementedTypes)
	}
	imports := importAliasesBySimpleName(ctx.Root, ctx.Content, map[string]struct{}{"import_header": {}})
	for i := range traits {
		traits[i] = qualifyImportedTypeName(traits[i], ctx.Package, imports)
	}
	return traits
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

// javaClassHierarchy extracts extends/implements targets from a Java class AST, falling back to text parsing when needed.
// @intent prefer grammar-aware traversal so commas inside generics do not split hierarchy targets.
func javaClassHierarchy(n *sitter.Node, content []byte) (string, []string) {
	if n == nil {
		return "", nil
	}
	if base, traits, ok := parseJavaClassHierarchyNode(n, content); ok {
		return base, traits
	}
	return parseJavaClassHierarchy(n.Content(content))
}

// parseJavaClassHierarchyNode extracts Java hierarchy targets from declaration children when grammar nodes are available.
// @intent avoid string splitting for generic type arguments in extends/implements clauses.
func parseJavaClassHierarchyNode(n *sitter.Node, content []byte) (string, []string, bool) {
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
		case "superclass":
			if name := firstJVMTypeReference(child, content); name != "" {
				base = name
			}
		case "super_interfaces":
			traits = append(traits, jvmTypeReferences(child, content)...)
		}
	}
	if base == "" && len(traits) == 0 {
		return "", nil, false
	}
	return base, appendUniquePackageFile(nil, traits...), true
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

// kotlinSupertypes extracts Kotlin superclass/interfaces from AST children before falling back to declaration text parsing.
// @intent avoid text-only parsing when tree-sitter exposes dedicated supertype nodes.
func kotlinSupertypes(n *sitter.Node, content []byte) (string, []string) {
	if n == nil {
		return "", nil
	}
	if base, traits, ok := parseKotlinSupertypesNode(n, content); ok {
		return base, traits
	}
	return parseKotlinSupertypes(n.Content(content))
}

// parseKotlinSupertypesNode extracts superclass/interface targets from Kotlin delegation specifier nodes.
// @intent prefer AST-aware extraction so commas inside generic arguments do not create false interfaces.
func parseKotlinSupertypesNode(n *sitter.Node, content []byte) (string, []string, bool) {
	if n == nil {
		return "", nil, false
	}
	var base string
	var traits []string
	var found bool
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if child == nil || child.Type() != "delegation_specifier" {
			continue
		}
		found = true
		name := normalizeKotlinSupertypeName(firstJVMTypeReference(child, content))
		if name == "" {
			continue
		}
		if base == "" && kotlinDelegationSpecifierHasConstructorInvocation(child) {
			base = name
			continue
		}
		traits = append(traits, name)
	}
	if !found || (base == "" && len(traits) == 0) {
		return "", nil, false
	}
	return base, appendUniquePackageFile(nil, traits...), true
}

// kotlinDelegationSpecifierHasConstructorInvocation reports whether a Kotlin delegation specifier is a superclass constructor call.
// @intent distinguish superclass entries from interface references in Kotlin AST-based supertype parsing.
func kotlinDelegationSpecifierHasConstructorInvocation(n *sitter.Node) bool {
	if n == nil {
		return false
	}
	var found bool
	var walk func(*sitter.Node)
	walk = func(cur *sitter.Node) {
		if cur == nil || found {
			return
		}
		if cur.Type() == "constructor_invocation" {
			found = true
			return
		}
		for i := 0; i < int(cur.NamedChildCount()); i++ {
			walk(cur.NamedChild(i))
		}
	}
	walk(n)
	return found
}

// firstJVMTypeReference returns the first normalized Java/Kotlin type reference under a subtree.
// @intent share simple type extraction between Java and Kotlin hierarchy walkers.
func firstJVMTypeReference(n *sitter.Node, content []byte) string {
	refs := jvmTypeReferences(n, content)
	if len(refs) == 0 {
		return ""
	}
	return refs[0]
}

// jvmTypeReferences collects normalized Java/Kotlin type names from an AST subtree.
// @intent recover hierarchy endpoints from grammar nodes while remaining tolerant of parser version differences.
func jvmTypeReferences(n *sitter.Node, content []byte) []string {
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
		case "type_identifier", "scoped_type_identifier", "user_type", "identifier", "simple_identifier":
			if name := strings.TrimSpace(cur.Content(content)); name != "" {
				refs = append(refs, name)
			}
			return
		case "generic_type", "type_identifier_list", "type_arguments", "constructor_invocation":
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
	value = strings.TrimSuffix(value, "()")
	if fields := strings.Fields(value); len(fields) > 0 {
		return fields[0]
	}
	return ""
}
