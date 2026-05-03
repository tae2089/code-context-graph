// @index Language-specific graph enrichment interfaces and shared parser context helpers.
package treesitter

import (
	"context"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/tae2089/code-context-graph/internal/model"
)

// LanguageSemantics provides optional language-specific graph enrichment hooks.
// @intent keep language-specific inference opt-in while the generic parser remains shared.
type LanguageSemantics interface {
	AdditionalEdges(ctx SemanticContext) []model.Edge
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
	Nodes          []model.Node
	Interfaces     []interfaceInfo
}

// DefinitionContext carries one matched definition into a language-specific enrichment hook.
// @intent expose definition-local AST state so languages can derive extra edges and metadata.
type DefinitionContext struct {
	Definition     *sitter.Node
	DefinitionType string
	Name           string
	QualifiedName  string
	Content        []byte
	FilePath       string
}

// DefinitionResult carries language-specific enrichment derived from a definition.
// @intent keep Walker generic while still allowing languages to accumulate interfaces and edges.
type DefinitionResult struct {
	Interfaces []interfaceInfo
	Edges      []model.Edge
}

// CommentContext carries file-level parse state into language-specific comment extraction hooks.
// @intent expose AST and file content so languages can surface docstrings as comment blocks.
type CommentContext struct {
	Root     *sitter.Node
	Content  []byte
	FilePath string
	Nodes    []model.Node
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

// additionalCommentsOrDefault returns language-specific comment blocks when implemented.
// @intent let languages expose docstring-like constructs without affecting generic comment extraction.
func additionalCommentsOrDefault(semantics LanguageSemantics, ctx CommentContext) []CommentBlock {
	if commenter, ok := semantics.(CommentSemantics); ok {
		return commenter.AdditionalComments(ctx)
	}
	return nil
}
