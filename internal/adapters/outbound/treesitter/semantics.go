// @index Language-specific graph enrichment interfaces and shared parser context helpers.
package treesitter

import (
	"context"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// LanguageSemantics provides optional language-specific graph enrichment hooks.
// @intent keep language-specific inference opt-in while the generic parser remains shared.
type LanguageSemantics interface {
	AdditionalEdges(ctx SemanticContext) []graph.Edge
}

// CallRewriteSemantics provides optional call-site rewriting hooks.
// @intent avoid forcing languages without call rewrite needs to implement no-op methods.
type CallRewriteSemantics interface {
	CallRewriter(ctx SemanticContext) CallRewriter
}

// DefinitionSemantics provides optional per-definition enrichment hooks.
// @intent let languages enrich parsed definitions without adding language branches to Walker.
type DefinitionSemantics interface {
	EnrichDefinition(ctx DefinitionContext) DefinitionResult
}

// DefinitionNameSemantics provides optional definition-name normalization hooks.
// @intent let languages normalize captured definition names before node and edge fingerprints are emitted.
type DefinitionNameSemantics interface {
	DefinitionName(ctx DefinitionContext) string
}

// RelationshipSemantics provides optional per-definition relationship normalization hooks.
// @intent let languages normalize query-captured relationships through the same definition path.
type RelationshipSemantics interface {
	ImplementedTypes(ctx DefinitionContext) []string
}

// PackageSemantics provides optional package-level enrichment hooks across multiple files.
// @intent let languages derive relationships that require package-wide context without widening Walker's per-file parse path.
type PackageSemantics interface {
	PackageEdges(ctx PackageContext) []graph.Edge
}

// CommentSemantics provides optional comment extraction hooks beyond raw AST comments.
// @intent let languages contribute docstrings or similar constructs without Walker language branches.
type CommentSemantics interface {
	AdditionalComments(ctx CommentContext) []CommentBlock
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
	Nodes          []graph.Node
	Interfaces     []interfaceInfo
}

// PackageInterfaceInfo captures one interface name plus its declared methods.
// @intent let package-level enrichment travel across package boundaries without exposing walker internals.
type PackageInterfaceInfo = ingest.PackageInterfaceInfo

// PackageContext carries package-wide parse state into language-specific enrichment hooks.
// @intent expose aggregated nodes and interfaces for languages whose relationships span multiple files.
type PackageContext = ingest.PackageContext

// DefinitionContext carries one matched definition into a language-specific enrichment hook.
// @intent expose definition-local AST state so languages can derive extra edges and metadata.
type DefinitionContext struct {
	Definition       *sitter.Node
	DefinitionType   string
	Name             string
	QualifiedName    string
	Root             *sitter.Node
	Package          string
	ImplementedTypes []string
	Content          []byte
	FilePath         string
}

// DefinitionResult carries language-specific enrichment derived from a definition.
// @intent keep Walker generic while still allowing languages to accumulate interfaces and edges.
type DefinitionResult struct {
	Interfaces []interfaceInfo
	Edges      []graph.Edge
}

// CommentContext carries file-level parse state into language-specific comment extraction hooks.
// @intent expose AST and file content so languages can surface docstrings as comment blocks.
type CommentContext struct {
	Root     *sitter.Node
	Content  []byte
	FilePath string
	Nodes    []graph.Node
}

// WithImportPackages stores repo-local import-path to package-name mappings in ctx.
// @intent let build/update provide package-clause-aware import normalization without widening parser interfaces.
func WithImportPackages(ctx context.Context, packages map[string]string) context.Context {
	return ingest.WithImportPackages(ctx, packages)
}

// WithGoImportPackages stores repo-local Go import-path to package-name mappings in ctx.
// @intent preserve compatibility for callers using the original Go-specific helper.
func WithGoImportPackages(ctx context.Context, packages map[string]string) context.Context {
	return WithImportPackages(ctx, packages)
}

// importPackagesFromContext loads repo-local import package metadata from ctx.
// @intent let Go-specific semantic helpers reuse package-name mappings without widening APIs.
func importPackagesFromContext(ctx context.Context) map[string]string {
	return ingest.ImportPackagesFromContext(ctx)
}

// WithFilePackages stores repo-local file-path to canonical import-path mappings in ctx.
// @intent let parsers stamp package-less languages with a deterministic file-level package prefix.
func WithFilePackages(ctx context.Context, packages map[string]string) context.Context {
	return ingest.WithFilePackages(ctx, packages)
}

// filePackagesFromContext loads repo-local file package metadata from ctx.
// @intent let walkers seed qualified names from a file's canonical import path when no package capture exists.
func filePackagesFromContext(ctx context.Context) map[string]string {
	return ingest.FilePackagesFromContext(ctx)
}

// NoopSemantics is the default implementation for languages without extra inference.
// @intent provide a safe fallback semantics hook when a language does not define extra graph enrichment.
type NoopSemantics struct{}

// AdditionalEdges returns no extra relationships for unsupported language hooks.
// @intent satisfy the LanguageSemantics interface with a no-op implementation.
func (NoopSemantics) AdditionalEdges(SemanticContext) []graph.Edge {
	return nil
}

// NoopCallRewriter leaves extracted callee names unchanged.
// @intent provide the default empty implementation for language specs without call rewrite rules.
type NoopCallRewriter struct{}

// RewriteCall returns the original callee unchanged.
// @intent satisfy CallRewriter for languages without additional call inference.
func (NoopCallRewriter) RewriteCall(ctx CallRewriteContext) string {
	return ctx.Callee
}

// semanticsOrDefault returns the semantics for the given LangSpec, or NoopSemantics if nil.
// @intent ensure a non-nil LanguageSemantics implementation is always available during parsing.
func semanticsOrDefault(s *LangSpec) LanguageSemantics {
	if s != nil && s.Semantics != nil {
		return s.Semantics
	}
	return NoopSemantics{}
}

// callRewriterOrDefault returns a language-specific rewriter when available.
// @intent keep call rewriting optional so languages without call inference avoid boilerplate.
func callRewriterOrDefault(semantics LanguageSemantics, ctx SemanticContext) CallRewriter {
	if rewriter, ok := semantics.(CallRewriteSemantics); ok {
		if value := rewriter.CallRewriter(ctx); value != nil {
			return value
		}
	}
	return NoopCallRewriter{}
}

// definitionResultOrDefault returns per-definition enrichment when implemented by a language.
// @intent keep Walker generic while allowing opt-in definition hooks.
func definitionResultOrDefault(semantics LanguageSemantics, ctx DefinitionContext) DefinitionResult {
	if enricher, ok := semantics.(DefinitionSemantics); ok {
		return enricher.EnrichDefinition(ctx)
	}
	return DefinitionResult{}
}

// definitionNameOrDefault returns a normalized definition name when implemented by a language.
// @intent centralize per-language symbol-name normalization behind an optional hook.
func definitionNameOrDefault(semantics LanguageSemantics, ctx DefinitionContext) string {
	if namer, ok := semantics.(DefinitionNameSemantics); ok {
		if name := namer.DefinitionName(ctx); name != "" {
			return name
		}
	}
	return ctx.Name
}

// implementedTypesOrDefault returns normalized implemented type names for one definition.
// @intent centralize query-captured implements relationships behind an optional language hook.
func implementedTypesOrDefault(semantics LanguageSemantics, ctx DefinitionContext) []string {
	if rel, ok := semantics.(RelationshipSemantics); ok {
		return rel.ImplementedTypes(ctx)
	}
	return append([]string(nil), ctx.ImplementedTypes...)
}

// additionalCommentsOrDefault returns language-specific comment blocks when implemented.
// @intent let languages expose docstring-like constructs without affecting generic comment extraction.
func additionalCommentsOrDefault(semantics LanguageSemantics, ctx CommentContext) []CommentBlock {
	if commenter, ok := semantics.(CommentSemantics); ok {
		return commenter.AdditionalComments(ctx)
	}
	return nil
}

// packageEdgesOrDefault returns language-specific package edges when implemented.
// @intent centralize package-level enrichment behind an optional semantics hook.
func packageEdgesOrDefault(semantics LanguageSemantics, ctx PackageContext) []graph.Edge {
	if enricher, ok := semantics.(PackageSemantics); ok {
		return enricher.PackageEdges(ctx)
	}
	return nil
}

// PackageEdgesFor exposes package-level semantics to callers outside the treesitter package.
// @intent let build/update orchestration reuse optional package-level enrichment hooks.
func PackageEdgesFor(semantics LanguageSemantics, ctx PackageContext) []graph.Edge {
	return packageEdgesOrDefault(semantics, ctx)
}

// SemanticsForLanguage returns the configured semantics for a language name.
// @intent let non-parser orchestration reuse the centralized language semantics registry without local language switches.
func SemanticsForLanguage(language string) LanguageSemantics {
	for _, spec := range []*LangSpec{GoSpec, PythonSpec, TypeScriptSpec, JavaSpec, CSpec, RustSpec, CppSpec, JavaScriptSpec, RubySpec, KotlinSpec, PHPSpec, LuaSpec} {
		if spec != nil && spec.Name == language {
			return semanticsOrDefault(spec)
		}
	}
	return NoopSemantics{}
}
