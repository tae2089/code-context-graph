// @index Language-specific graph enrichment logic for recovery of hidden relationships.
package treesitter

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/tae2089/code-context-graph/internal/model"
)

// LanguageSemantics provides optional language-specific graph enrichment hooks.
// @intent keep language-specific inference opt-in while the generic parser remains shared.
type LanguageSemantics interface {
	AdditionalEdges(ctx SemanticContext) []model.Edge
	CallRewriter(ctx SemanticContext) CallRewriter
}

// CallRewriter optionally rewrites raw callee names before call edges are emitted.
// @intent let language specs recover dynamic dispatch targets without adding language branches to Walker.
type CallRewriter interface {
	RewriteCall(ctx CallRewriteContext) string
}

// CallRewriteContext carries one extracted call into a language-specific rewrite hook.
// @intent provide enough call-site metadata for languages with assignment or dispatch-sensitive call names.
type CallRewriteContext struct {
	Root     *sitter.Node
	Node     *sitter.Node
	Content  []byte
	FilePath string
	Callee   string
	Line     int
}

// SemanticContext carries parsed state into language-specific enrichment hooks.
// @intent avoid expanding Walker with one-off language branches as graph inference grows.
type SemanticContext struct {
	Root           *sitter.Node
	Content        []byte
	FilePath       string
	Package        string
	ImportPackages map[string]string
	Nodes          []model.Node
	Interfaces     []interfaceInfo
}

// importPackagesContextKey is the private context key for repo-local import package metadata.
// @intent avoid collisions while threading import-package hints through parser calls.
type importPackagesContextKey struct{}

// WithImportPackages stores repo-local import-path to package-name mappings in ctx.
// @intent let build/update provide package-clause-aware import normalization without widening parser interfaces.
func WithImportPackages(ctx context.Context, packages map[string]string) context.Context {
	if len(packages) == 0 {
		return ctx
	}
	cloned := make(map[string]string, len(packages))
	for importPath, pkgName := range packages {
		if importPath == "" || pkgName == "" {
			continue
		}
		cloned[importPath] = pkgName
	}
	if len(cloned) == 0 {
		return ctx
	}
	return context.WithValue(ctx, importPackagesContextKey{}, cloned)
}

// WithGoImportPackages stores repo-local Go import-path to package-name mappings in ctx.
// @intent preserve compatibility for callers using the original Go-specific helper.
func WithGoImportPackages(ctx context.Context, packages map[string]string) context.Context {
	return WithImportPackages(ctx, packages)
}

// importPackagesFromContext loads repo-local import package metadata from ctx.
// @intent let Go-specific semantic helpers reuse package-name mappings without widening APIs.
func importPackagesFromContext(ctx context.Context) map[string]string {
	if ctx == nil {
		return nil
	}
	packages, _ := ctx.Value(importPackagesContextKey{}).(map[string]string)
	return packages
}

// NoopSemantics is the default implementation for languages without extra inference.
// @intent provide a safe fallback semantics hook when a language does not define extra graph enrichment.
type NoopSemantics struct{}

// AdditionalEdges returns no extra relationships for unsupported language hooks.
// @intent satisfy the LanguageSemantics interface with a no-op implementation.
func (NoopSemantics) AdditionalEdges(SemanticContext) []model.Edge {
	return nil
}

// CallRewriter returns a no-op call rewriter for languages without call-name enrichment.
// @intent keep call extraction generic while unsupported languages opt out safely.
func (NoopSemantics) CallRewriter(SemanticContext) CallRewriter {
	return NoopCallRewriter{}
}

// NoopCallRewriter leaves extracted callee names unchanged.
// @intent provide the default empty implementation for language specs without call rewrite rules.
type NoopCallRewriter struct{}

// RewriteCall returns the original callee unchanged.
// @intent satisfy CallRewriter for languages without additional call inference.
func (NoopCallRewriter) RewriteCall(ctx CallRewriteContext) string {
	return ctx.Callee
}

// GoSemantics recovers Go-specific relationships that are not explicit call edges.
// @intent encapsulate Go-specific graph enrichment logic such as interface implementation discovery.
type GoSemantics struct{}

// AdditionalEdges adds Go structural and assertion-based implementation edges.
// @intent identify "implements" relationships using both structural and explicit compile-time assertions.
func (GoSemantics) AdditionalEdges(ctx SemanticContext) []model.Edge {
	var edges []model.Edge
	edges = append(edges, goStructuralImplements(ctx.Nodes, ctx.Interfaces, ctx.FilePath)...)
	edges = append(edges, goAssertionImplements(ctx.Root, ctx.Content, ctx.FilePath, ctx.ImportPackages)...)
	return edges
}

// CallRewriter returns Go call-name enrichment based on type assertion bindings.
// @intent preserve interface dispatch context for calls made through asserted variables.
func (GoSemantics) CallRewriter(ctx SemanticContext) CallRewriter {
	return goAssertionCallRewriter{bindings: collectGoAssertionCallBindings(ctx.Root, ctx.Content)}
}

// goAssertionCallRewriter rewrites calls made through Go type-asserted variables.
// @intent keep Go assertion call inference behind the language semantics hook.
type goAssertionCallRewriter struct {
	bindings map[string][]receiverTypeBinding
}

// RewriteCall rewrites selector calls to the assertion source type when a prior binding exists.
// @intent preserve interface dispatch context for calls made through asserted variables.
func (r goAssertionCallRewriter) RewriteCall(ctx CallRewriteContext) string {
	if len(r.bindings) == 0 {
		return ctx.Callee
	}
	receiver, method, ok := strings.Cut(ctx.Callee, ".")
	if !ok || receiver == "" || method == "" {
		return ctx.Callee
	}
	var best *receiverTypeBinding
	for i := range r.bindings[receiver] {
		binding := &r.bindings[receiver][i]
		if binding.line > ctx.Line {
			continue
		}
		if best == nil || binding.line > best.line {
			best = binding
		}
	}
	if best == nil || best.sourceType == "" {
		return ctx.Callee
	}
	return best.sourceType + "." + method
}

// receiverTypeBinding records a variable and the source type inferred at one line.
// @intent bind assignment-sensitive call rewrites without exposing language details to Walker.
type receiverTypeBinding struct {
	line       int
	sourceType string
}

// collectGoAssertionCallBindings gathers Go type assertion assignments from the AST.
// @intent support later call-name rewriting when an asserted interface variable is used.
func collectGoAssertionCallBindings(root *sitter.Node, content []byte) map[string][]receiverTypeBinding {
	bindings := make(map[string][]receiverTypeBinding)
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "type_assertion_expression" {
			if name, sourceType, ok := extractGoAssertionCallBinding(n, content); ok {
				bindings[name] = append(bindings[name], receiverTypeBinding{
					line:       int(n.StartPoint().Row) + 1,
					sourceType: sourceType,
				})
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	if len(bindings) == 0 {
		return nil
	}
	return bindings
}

// extractGoAssertionCallBinding resolves the assigned variable name and source interface for one assertion.
// @intent capture enough metadata to rewrite subsequent selector calls on asserted variables.
func extractGoAssertionCallBinding(assertion *sitter.Node, content []byte) (string, string, bool) {
	sourceType := goAssertionCallSourceType(assertion.Content(content))
	if sourceType == "" {
		return "", "", false
	}
	for p := assertion.Parent(); p != nil; p = p.Parent() {
		switch p.Type() {
		case "short_var_declaration":
			if lhs, _, ok := strings.Cut(p.Content(content), ":="); ok {
				name := firstGoAssignmentName(lhs)
				return name, sourceType, name != ""
			}
		case "var_spec":
			if lhs, _, ok := strings.Cut(p.Content(content), "="); ok {
				lhs = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lhs), "var "))
				name := firstGoAssignmentName(lhs)
				return name, sourceType, name != ""
			}
		case "function_declaration", "method_declaration":
			return "", "", false
		}
	}
	return "", "", false
}

// goAssertionCallSourceType derives the source interface-like type from a Go type assertion expression.
// @intent prefer the source-side interface name so rewritten calls keep the dispatch context.
func goAssertionCallSourceType(expr string) string {
	expr = strings.TrimSpace(expr)
	before, after, ok := strings.Cut(expr, ".(")
	if !ok {
		return ""
	}
	asserted := strings.TrimSuffix(strings.TrimSpace(after), ")")
	source := lastGoSelectorSegment(before)
	if isGoExportedIdent(source) {
		return source
	}
	return lastGoSelectorSegment(asserted)
}

// firstGoAssignmentName returns the first valid identifier on the left-hand side of an assignment.
// @intent bind type assertion metadata to the variable that will receive the asserted value.
func firstGoAssignmentName(lhs string) string {
	parts := strings.Split(lhs, ",")
	if len(parts) == 0 {
		return ""
	}
	name := strings.TrimSpace(parts[0])
	if !isGoIdent(name) || name == "_" {
		return ""
	}
	return name
}

// lastGoSelectorSegment returns the final selector segment from a dotted Go expression.
// @intent normalize receiver and asserted-type text before interface dispatch rewriting.
func lastGoSelectorSegment(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "*")
	value = strings.Trim(value, "()")
	value = strings.TrimSpace(value)
	parts := strings.Split(value, ".")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

// isGoExportedIdent reports whether a Go identifier is exported.
// @intent apply Go visibility heuristics when choosing interface-oriented rewrite targets.
func isGoExportedIdent(name string) bool {
	if name == "" {
		return false
	}
	r := rune(name[0])
	return r >= 'A' && r <= 'Z'
}

// isGoIdent reports whether a string is a syntactically valid simple Go identifier.
// @intent reject non-identifier assignment targets when extracting assertion bindings.
func isGoIdent(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

// semanticsOrDefault returns the semantics for the given LangSpec, or NoopSemantics if nil.
// @intent ensure a non-nil LanguageSemantics implementation is always available during parsing.
func semanticsOrDefault(s *LangSpec) LanguageSemantics {
	if s != nil && s.Semantics != nil {
		return s.Semantics
	}
	return NoopSemantics{}
}

// goStructuralImplements derives implementation edges based on method set matching.
// @intent support Go's implicit structural typing by matching concrete method names against interface declarations.
func goStructuralImplements(nodes []model.Node, ifaces []interfaceInfo, filePath string) []model.Edge {
	methodsByReceiver := make(map[string]map[string]bool)
	for _, n := range nodes {
		if n.Kind != model.NodeKindFunction {
			continue
		}
		parts := strings.Split(n.QualifiedName, ".")
		if len(parts) >= 3 {
			receiver := parts[len(parts)-2]
			method := parts[len(parts)-1]
			if methodsByReceiver[receiver] == nil {
				methodsByReceiver[receiver] = make(map[string]bool)
			}
			methodsByReceiver[receiver][method] = true
		}
	}

	var edges []model.Edge
	for _, iface := range ifaces {
		if len(iface.methods) == 0 {
			continue
		}
		for receiver, methods := range methodsByReceiver {
			allMatch := true
			for _, m := range iface.methods {
				if !methods[m] {
					allMatch = false
					break
				}
			}
			if allMatch {
				edges = append(edges, model.Edge{
					Kind:        model.EdgeKindImplements,
					FilePath:    filePath,
					Fingerprint: fmt.Sprintf("implements:%s:%s:%s", filePath, receiver, iface.name),
				})
			}
		}
	}
	return edges
}

// goAssertionImplements finds implementation edges from compile-time type assertions.
// @intent extract "implements" relationships from common Go idioms like `var _ Interface = (*Concrete)(nil)`.
func goAssertionImplements(root *sitter.Node, content []byte, filePath string, repoPackages map[string]string) []model.Edge {
	if root == nil {
		return nil
	}
	importAliases := goImportAliases(root, content, repoPackages)
	var edges []model.Edge
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "var_spec" {
			if impl, iface, ok := goAssertionSpec(n, content, importAliases); ok {
				edges = append(edges, model.Edge{
					Kind:        model.EdgeKindImplements,
					FilePath:    filePath,
					Line:        int(n.StartPoint().Row) + 1,
					Fingerprint: fmt.Sprintf("implements:%s:%s:%s", filePath, impl, iface),
				})
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return edges
}

// goImportAliases maps local import aliases and default package names in a Go source file.
// @intent resolve locally-used package names to their canonical import targets during parsing.
func goImportAliases(root *sitter.Node, content []byte, repoPackages map[string]string) map[string]string {
	aliases := make(map[string]string)
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "import_spec" {
			importPath := ""
			if pathNode := n.ChildByFieldName("path"); pathNode != nil {
				importPath = strings.Trim(pathNode.Content(content), "\"`")
			}
			pkg := defaultGoImportName(importPath)
			if repoPkg := repoPackages[importPath]; repoPkg != "" {
				pkg = repoPkg
			}
			if pkg != "." && pkg != "/" && pkg != "" {
				alias := pkg
				if nameNode := n.ChildByFieldName("name"); nameNode != nil {
					alias = nameNode.Content(content)
				}
				if alias != "_" && alias != "." {
					aliases[alias] = pkg
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return aliases
}

// defaultGoImportName derives the default package name from an import path.
// @intent approximate the package name used in Go source by taking the base segment of the import path.
func defaultGoImportName(importPath string) string {
	importPath = strings.TrimSpace(importPath)
	if importPath == "" {
		return ""
	}
	base := path.Base(importPath)
	if isGoMajorVersionSegment(base) {
		base = path.Base(path.Dir(importPath))
	}
	return trimGoVersionSuffix(base)
}

// isGoMajorVersionSegment reports whether a path segment represents a Go major version (e.g., v2).
// @intent handle Go modules with semantic versioning segments in their import paths.
func isGoMajorVersionSegment(seg string) bool {
	if len(seg) < 2 || seg[0] != 'v' {
		return false
	}
	_, err := strconv.Atoi(seg[1:])
	return err == nil
}

// trimGoVersionSuffix removes version suffixes like .v1 from a package name.
// @intent normalize Go package names by stripping legacy gopkg.in-style version suffixes.
func trimGoVersionSuffix(name string) string {
	idx := strings.LastIndex(name, ".v")
	if idx <= 0 {
		return name
	}
	if _, err := strconv.Atoi(name[idx+2:]); err != nil {
		return name
	}
	return name[:idx]
}

// goAssertionConcreteRE matches `(*Type)(nil)` and `(Type)(nil)` forms.
var goAssertionConcreteRE = regexp.MustCompile(`\(\s*\*?\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)\s*\)`)

// goAssertionStructLiteralRE matches `Type{...}` and `&Type{...}` forms.
// @intent recognise compile-time assertions written with composite literals.
var goAssertionStructLiteralRE = regexp.MustCompile(`^\s*&?\s*([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)?)\s*\{`)

// goAssertionSpec extracts interface and concrete types from a Go compile-time assertion.
// @intent emit implements edges from patterns like `var _ Interface = (*Type)(nil)`.
func goAssertionSpec(n *sitter.Node, content []byte, aliases map[string]string) (string, string, bool) {
	nameNode := n.ChildByFieldName("name")
	typeNode := n.ChildByFieldName("type")
	valueNode := n.ChildByFieldName("value")
	if nameNode == nil || typeNode == nil || valueNode == nil {
		return "", "", false
	}
	if strings.TrimSpace(nameNode.Content(content)) != "_" {
		return "", "", false
	}
	iface := normalizeGoTypeName(typeNode.Content(content), aliases)
	if iface == "" {
		return "", "", false
	}
	value := valueNode.Content(content)
	concrete, ok := extractGoAssertionConcrete(value)
	if !ok {
		return "", "", false
	}
	impl := normalizeGoTypeName(concrete, aliases)
	if impl == "" {
		return "", "", false
	}
	return impl, iface, true
}

// extractGoAssertionConcrete returns the concrete type name from a compile-time
// assertion right-hand side. Supported forms:
//   - (*Type)(nil) / (Type)(nil)
//   - Type{...}
//   - &Type{...}
//
// @intent keep concrete-type extraction in one place so new assertion shapes
// are easy to add without bloating goAssertionSpec.
func extractGoAssertionConcrete(value string) (string, bool) {
	if m := goAssertionConcreteRE.FindStringSubmatch(value); len(m) == 2 {
		return m[1], true
	}
	if m := goAssertionStructLiteralRE.FindStringSubmatch(value); len(m) == 2 {
		return m[1], true
	}
	return "", false
}

// normalizeGoTypeName resolves a type name to its canonical form using import aliases.
// @intent ensure Go type names (e.g., pkg.Type) are mapped to their correct package namespaces.
func normalizeGoTypeName(name string, aliases map[string]string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "*")
	name = strings.Trim(name, "()")
	name = strings.TrimSpace(name)
	parts := strings.Split(name, ".")
	if len(parts) == 2 {
		if mapped, ok := aliases[parts[0]]; ok {
			parts[0] = mapped
		}
		return parts[0] + "." + parts[1]
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return ""
}
