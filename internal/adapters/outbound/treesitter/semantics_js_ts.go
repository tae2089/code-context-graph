package treesitter

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// TypeScriptSemantics recovers class hierarchy edges from TypeScript heritage clauses.
// @intent emit extends and implements relationships for TypeScript classes without adding language branches to Walker.
type TypeScriptSemantics struct{}

// JavaScriptSemantics recovers class inheritance edges from JavaScript class heritage clauses.
// @intent emit extends relationships for JavaScript classes using the same heritage parsing model as TypeScript.
type JavaScriptSemantics struct{}

// explicitReceiverTypeCallRewriter rewrites typed JS/TS receiver chains into qualified calls.
// @intent preserve conservative member-call rewriting when each hop is proven by explicit type annotations.
type explicitReceiverTypeCallRewriter struct {
	bindings map[string]string
	members  map[string]map[string]string
	chain    func(*sitter.Node, []byte) []string
}

// AdditionalEdges adds TypeScript extends and implements edges from class heritage clauses.
// @intent capture TypeScript class hierarchy semantics directly from the parsed AST.
func (TypeScriptSemantics) AdditionalEdges(ctx SemanticContext) []graph.Edge {
	if ctx.Root == nil {
		return nil
	}
	var edges []graph.Edge
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "class_declaration" {
			className := typescriptClassName(n, ctx.Content)
			if className != "" {
				childName := qualifyTypeName(ctx.Package, className)
				imports := typeScriptImportPackages(ctx.Content, ctx.ImportPackages)
				base, traits := typescriptHeritage(n, ctx.Content)
				base = qualifyTypeScriptHeritageTypeName(ctx, base, imports)
				for i := range traits {
					traits[i] = qualifyTypeScriptHeritageTypeName(ctx, traits[i], imports)
				}
				if base != "" {
					edges = append(edges, graph.Edge{
						Kind:        graph.EdgeKindInherits,
						FilePath:    ctx.FilePath,
						Line:        int(n.StartPoint().Row) + 1,
						Fingerprint: graph.BuildInheritsFingerprintV2(ctx.FilePath, childName, base),
					})
				}
				for _, trait := range traits {
					edges = append(edges, graph.Edge{
						Kind:        graph.EdgeKindImplements,
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

// qualifyTypeScriptHeritageTypeName resolves a heritage type name through imports or the current file.
// @intent keep TypeScript extends and implements targets consistently qualified before edge creation.
func qualifyTypeScriptHeritageTypeName(ctx SemanticContext, typeName string, imports map[string]string) string {
	if typeName == "" {
		return typeName
	}
	if importPkg := imports[typeName]; importPkg != "" {
		return qualifyTypeName(importPkg, typeName)
	}
	return qualifySameFileTypeName(ctx, typeName)
}

// qualifySameFileTypeName qualifies a type name when the declaration lives in the same source file.
// @intent keep same-file TypeScript references aligned with the file's package context.
func qualifySameFileTypeName(ctx SemanticContext, typeName string) string {
	if typeName == "" || ctx.Package == "" {
		return typeName
	}
	for _, node := range ctx.Nodes {
		if node.FilePath != ctx.FilePath {
			continue
		}
		if node.Kind != graph.NodeKindClass && node.Kind != graph.NodeKindType {
			continue
		}
		if node.Name == typeName {
			return qualifyTypeName(ctx.Package, typeName)
		}
	}
	return typeName
}

// typeScriptImportPackages maps imported symbol aliases to their package names from source text.
// @intent resolve TypeScript imports into package context for heritage qualification.
func typeScriptImportPackages(content []byte, importPackages map[string]string) map[string]string {
	if len(importPackages) == 0 {
		return nil
	}
	matches := regexp.MustCompile(`(?m)import\s*{([^}]*)}\s*from\s*["']([^"']+)["']`).FindAllStringSubmatch(string(content), -1)
	if len(matches) == 0 {
		return nil
	}
	aliases := make(map[string]string)
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		pkg := importPackages[match[2]]
		if pkg == "" {
			continue
		}
		for _, part := range strings.Split(match[1], ",") {
			name := strings.TrimSpace(part)
			if name == "" {
				continue
			}
			if before, after, ok := strings.Cut(name, " as "); ok {
				name = strings.TrimSpace(after)
				if name == "" {
					name = strings.TrimSpace(before)
				}
			}
			if name != "" {
				aliases[name] = pkg
			}
		}
	}
	if len(aliases) == 0 {
		return nil
	}
	return aliases
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

// CallRewriter returns a conservative TypeScript receiver-type rewriter.
// @intent rewrite member-call chains only when explicit type annotations prove each hop.
func (TypeScriptSemantics) CallRewriter(ctx SemanticContext) CallRewriter {
	return explicitReceiverTypeCallRewriter{
		bindings: collectTypeScriptReceiverBindings(ctx.Root, ctx.Content),
		members:  collectTypeScriptMemberTypes(ctx.Root, ctx.Content),
		chain:    typescriptReceiverChain,
	}
}

// ImplementedTypes returns query-captured relationships unchanged for JavaScript.
// @intent satisfy shared relationship normalization without inventing JS interface semantics.
func (JavaScriptSemantics) ImplementedTypes(ctx DefinitionContext) []string {
	return slices.Clone(ctx.ImplementedTypes)
}

// RewriteCall rewrites a member-call chain when explicit type annotations prove every hop.
// @intent preserve conservative receiver dispatch by upgrading only typed call chains into owner-qualified selectors.
func (r explicitReceiverTypeCallRewriter) RewriteCall(ctx CallRewriteContext) string {
	if len(r.bindings) == 0 {
		return ctx.Callee
	}
	var chain []string
	if r.chain != nil {
		chain = r.chain(ctx.Node, ctx.Content)
	}
	if len(chain) == 0 {
		chain = callChainFromCallee(ctx.Callee)
	}
	if len(chain) < 2 {
		return ctx.Callee
	}
	typeName := r.bindings[chain[0]]
	if typeName == "" {
		return ctx.Callee
	}
	for i := 1; i < len(chain)-1; i++ {
		members := r.members[typeName]
		if len(members) == 0 {
			return ctx.Callee
		}
		nextType := members[chain[i]]
		if nextType == "" || isLooseReceiverType(nextType) {
			return ctx.Callee
		}
		typeName = nextType
	}
	return typeName + "." + chain[len(chain)-1]
}

// collectTypeScriptReceiverBindings extracts explicit TypeScript variable and parameter type bindings.
// @intent seed conservative receiver rewriting with only textually provable TypeScript type annotations.
func collectTypeScriptReceiverBindings(root *sitter.Node, content []byte) map[string]string {
	_ = root
	bindings := make(map[string]string)
	for _, match := range typedIdentifierPattern.FindAllStringSubmatch(string(content), -1) {
		if len(match) < 3 {
			continue
		}
		typeName := normalizeReceiverTypeName(match[2])
		if isLooseReceiverType(typeName) {
			continue
		}
		bindings[match[1]] = typeName
	}
	if len(bindings) == 0 {
		return nil
	}
	return bindings
}

// collectTypeScriptMemberTypes collects explicit TypeScript member types keyed by owner type.
// @intent prove intermediate member hops before rewriting TypeScript call chains.
func collectTypeScriptMemberTypes(root *sitter.Node, content []byte) map[string]map[string]string {
	_ = root
	members := collectTypeScriptMembersFromText(string(content))
	if len(members) == 0 {
		return nil
	}
	return members
}

// typescriptReceiverChain extracts the selector chain from a TypeScript call node.
// @intent recover member-call hops directly from the AST when callee text is insufficient.
func typescriptReceiverChain(callNode *sitter.Node, content []byte) []string {
	if callNode == nil {
		return nil
	}
	fnNode := callNode.ChildByFieldName("function")
	if fnNode == nil {
		return nil
	}
	return memberChainFromNode(fnNode, content)
}

var typedIdentifierPattern = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*:\s*([A-Za-z_][A-Za-z0-9_\.]*)`)

// callChainFromCallee splits a raw callee string into selector segments.
// @intent share one normalized chain representation across language-specific receiver rewriters.
func callChainFromCallee(callee string) []string {
	return selectorChainFromText(callee)
}

// collectTypeScriptMembersFromText scans TypeScript source text for explicitly typed members.
// @intent avoid depending on grammar-specific field captures when proving member-chain types.
func collectTypeScriptMembersFromText(src string) map[string]map[string]string {
	lines := strings.Split(src, "\n")
	members := make(map[string]map[string]string)
	owner := ""
	depth := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if owner == "" {
			if match := regexp.MustCompile(`^(?:export\s+)?(?:interface|class)\s+([A-Za-z_][A-Za-z0-9_]*)\b`).FindStringSubmatch(trimmed); len(match) == 2 {
				owner = match[1]
				depth = strings.Count(line, "{") - strings.Count(line, "}")
				continue
			}
		}
		if owner != "" {
			if match := regexp.MustCompile(`^(?:public\s+|private\s+|protected\s+|readonly\s+)?([A-Za-z_][A-Za-z0-9_]*)\??\s*:\s*([A-Za-z_][A-Za-z0-9_\.]*)`).FindStringSubmatch(trimmed); len(match) == 3 {
				typeName := normalizeReceiverTypeName(match[2])
				if !isLooseReceiverType(typeName) {
					if members[owner] == nil {
						members[owner] = make(map[string]string)
					}
					members[owner][match[1]] = typeName
				}
			}
			depth += strings.Count(line, "{") - strings.Count(line, "}")
			if depth <= 0 {
				owner = ""
				depth = 0
			}
		}
	}
	return members
}

// memberChainFromNode extracts a dotted selector chain from one AST node.
// @intent reuse AST-derived selector parsing when raw callee strings are incomplete.
func memberChainFromNode(n *sitter.Node, content []byte) []string {
	if n == nil {
		return nil
	}
	return selectorChainFromText(n.Content(content))
}

// selectorChainFromText splits selector text into ordered path segments.
// @intent share one normalized selector chain representation across call rewriting helpers.
func selectorChainFromText(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	raw = selectorPrefix(raw)
	if raw == "" || !strings.Contains(raw, ".") {
		return nil
	}
	parts := strings.Split(raw, ".")
	if len(parts) < 2 {
		return nil
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if parts[i] == "" {
			return nil
		}
	}
	return parts
}

// selectorPrefix returns the leading selector segment from raw text.
// @intent support conservative receiver qualification by identifying the base selector quickly.
func selectorPrefix(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for i, r := range raw {
		switch r {
		case '(', '{', ' ', '\t', '\n', '\r':
			return strings.TrimSpace(raw[:i])
		}
	}
	return raw
}

// normalizeReceiverTypeName strips suffix syntax that would destabilize receiver type matching.
// @intent canonicalize explicit type names before they are used as receiver-chain proof.
func normalizeReceiverTypeName(raw string) string {
	raw = strings.TrimSpace(raw)
	for _, sep := range []string{"=", "{", "<", "(", "?", "!"} {
		if before, _, ok := strings.Cut(raw, sep); ok {
			raw = strings.TrimSpace(before)
		}
	}
	raw = strings.TrimPrefix(raw, "readonly ")
	raw = strings.TrimPrefix(raw, "public ")
	raw = strings.TrimPrefix(raw, "private ")
	raw = strings.TrimPrefix(raw, "protected ")
	raw = strings.TrimSpace(raw)
	return raw
}

// isLooseReceiverType reports whether a type is too imprecise for conservative receiver rewriting.
// @intent keep any/unknown/object-style declarations from producing false-positive dispatch rewrites.
func isLooseReceiverType(typeName string) bool {
	switch typeName {
	case "", "any", "unknown", "object", "Object", "Any":
		return true
	default:
		return false
	}
}

// AdditionalEdges adds JavaScript extends edges from class heritage clauses.
// @intent capture JavaScript class inheritance while ignoring TypeScript-only interface semantics.
func (JavaScriptSemantics) AdditionalEdges(ctx SemanticContext) []graph.Edge {
	if ctx.Root == nil {
		return nil
	}
	var edges []graph.Edge
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
					edges = append(edges, graph.Edge{
						Kind:        graph.EdgeKindInherits,
						FilePath:    ctx.FilePath,
						Line:        int(n.StartPoint().Row) + 1,
						Fingerprint: graph.BuildInheritsFingerprintV2(ctx.FilePath, className, base),
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
