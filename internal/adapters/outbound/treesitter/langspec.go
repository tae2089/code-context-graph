// @index Language-specific grammar specifications and discovery hooks for the Tree-sitter parser.
package treesitter

import (
	"context"

	"github.com/tae2089/code-context-graph/internal/app/ingest"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// NodeTypeMapping maps a Tree-sitter node type to an internal graph node kind.
// @intent describe how grammar-specific node names translate into model semantics
type NodeTypeMapping struct {
	ASTType  string
	NodeKind graph.NodeKind
}

// PackageInfo describes a source package/module node discovered outside a single file parse.
// @intent let language specs model multi-file import targets without service-level language branches.
type PackageInfo = ingest.PackageInfo

// PackageDiscoveryOptions carries repository traversal services into language package discovery.
// @intent keep language-specific discovery in LangSpec while reusing service include/exclude policies.
type PackageDiscoveryOptions = ingest.PackageDiscoveryOptions

// PackageDiscovery is the optional language hook for building package/module nodes.
// @intent let each language define its own multi-file import model without changing the ingest workflow service.
type PackageDiscovery interface {
	DiscoverPackages(ctx context.Context, opts PackageDiscoveryOptions) (map[string]PackageInfo, error)
}

// NoopPackageDiscovery is used by languages that do not yet provide package/module discovery.
// @intent provide a default no-op implementation of the PackageDiscovery interface.
type NoopPackageDiscovery struct{}

// DiscoverPackages returns no package nodes for unsupported language hooks.
// @intent let callers reuse one package-discovery flow even when a language has no package model.
func (NoopPackageDiscovery) DiscoverPackages(context.Context, PackageDiscoveryOptions) (map[string]PackageInfo, error) {
	return nil, nil
}

// DiscoverPackages delegates repository package discovery to the walker's language specification.
// @intent implement the ingest package-discovery port without exposing LangSpec to the application.
func (w *Walker) DiscoverPackages(ctx context.Context, opts ingest.PackageDiscoveryOptions) (map[string]ingest.PackageInfo, error) {
	return PackageDiscoveryOrDefault(w.spec).DiscoverPackages(ctx, opts)
}

// PackageEdges derives package-wide semantic relationships through the walker's language specification.
// @intent implement the ingest package-edge port while keeping language semantics inside the Tree-sitter adapter.
func (w *Walker) PackageEdges(ctx ingest.PackageContext) []graph.Edge {
	return PackageEdgesFor(w.spec.Semantics, ctx)
}

// LangSpec describes the Tree-sitter grammar shapes used to extract graph data for one language.
// @intent centralize language-specific AST node names, test conventions, and extraction hints
type LangSpec struct {
	Name             string
	FunctionTypes    []string
	ClassTypes       []string
	InterfaceTypes   []string
	ImportTypes      []string
	CallTypes        []string
	TestPrefix       string
	TestAttributes   []string
	ImplTypes        []string
	ExtensionTypes   []string
	PackageNodeType  string
	Semantics        LanguageSemantics
	PackageDiscovery PackageDiscovery
}

var GoSpec = &LangSpec{
	Name:             "go",
	FunctionTypes:    []string{"function_declaration", "method_declaration"},
	ClassTypes:       []string{"type_declaration"},
	InterfaceTypes:   []string{},
	ImportTypes:      []string{"import_declaration", "import_spec"},
	CallTypes:        []string{"call_expression"},
	TestPrefix:       "Test",
	PackageNodeType:  "package_clause",
	Semantics:        GoSemantics{},
	PackageDiscovery: GoPackageDiscovery{},
}

var PythonSpec = &LangSpec{
	Name:             "python",
	FunctionTypes:    []string{"function_definition"},
	ClassTypes:       []string{"class_definition"},
	ImportTypes:      []string{"import_statement", "import_from_statement"},
	CallTypes:        []string{"call"},
	TestPrefix:       "test_",
	Semantics:        PythonSemantics{},
	PackageDiscovery: PythonPackageDiscovery{},
}

var TypeScriptSpec = &LangSpec{
	Name:             "typescript",
	FunctionTypes:    []string{"function_declaration", "method_definition", "arrow_function"},
	ClassTypes:       []string{"class_declaration"},
	ImportTypes:      []string{"import_statement"},
	CallTypes:        []string{"call_expression"},
	TestPrefix:       "test",
	Semantics:        TypeScriptSemantics{},
	PackageDiscovery: TypeScriptPackageDiscovery{},
}

var JavaSpec = &LangSpec{
	Name:             "java",
	FunctionTypes:    []string{"method_declaration", "constructor_declaration"},
	ClassTypes:       []string{"class_declaration"},
	InterfaceTypes:   []string{"interface_declaration"},
	ImportTypes:      []string{"import_declaration"},
	CallTypes:        []string{"method_invocation"},
	TestPrefix:       "test",
	Semantics:        JavaSemantics{},
	PackageDiscovery: JavaPackageDiscovery{},
}

var CSpec = &LangSpec{
	Name:             "c",
	FunctionTypes:    []string{"function_definition"},
	ClassTypes:       []string{"struct_specifier"},
	ImportTypes:      []string{"preproc_include"},
	CallTypes:        []string{"call_expression"},
	TestPrefix:       "test_",
	PackageDiscovery: NoopPackageDiscovery{},
}

var RustSpec = &LangSpec{
	Name:             "rust",
	FunctionTypes:    []string{"function_item"},
	ClassTypes:       []string{"struct_item", "enum_item"},
	InterfaceTypes:   []string{"trait_item"},
	ImportTypes:      []string{"use_declaration"},
	CallTypes:        []string{"call_expression"},
	TestPrefix:       "test_",
	TestAttributes:   []string{"test"},
	ImplTypes:        []string{"impl_item"},
	Semantics:        RustSemantics{},
	PackageDiscovery: NoopPackageDiscovery{},
}

var CppSpec = &LangSpec{
	Name:             "cpp",
	FunctionTypes:    []string{"function_definition"},
	ClassTypes:       []string{"class_specifier", "struct_specifier"},
	InterfaceTypes:   []string{},
	ImportTypes:      []string{"preproc_include"},
	CallTypes:        []string{"call_expression"},
	TestPrefix:       "TEST",
	PackageDiscovery: NoopPackageDiscovery{},
}

var JavaScriptSpec = &LangSpec{
	Name:             "javascript",
	FunctionTypes:    []string{"function_declaration", "method_definition", "arrow_function"},
	ClassTypes:       []string{"class_declaration"},
	ImportTypes:      []string{"import_statement"},
	CallTypes:        []string{"call_expression"},
	TestPrefix:       "test",
	Semantics:        JavaScriptSemantics{},
	PackageDiscovery: JavaScriptPackageDiscovery{},
}

var RubySpec = &LangSpec{
	Name:             "ruby",
	FunctionTypes:    []string{"method"},
	ClassTypes:       []string{"class"},
	ImportTypes:      []string{"call"},
	CallTypes:        []string{"call"},
	TestPrefix:       "test_",
	PackageDiscovery: NoopPackageDiscovery{},
}

var KotlinSpec = &LangSpec{
	Name:             "kotlin",
	FunctionTypes:    []string{"function_declaration"},
	ClassTypes:       []string{"class_declaration", "object_declaration"},
	InterfaceTypes:   []string{"interface_declaration"},
	ImportTypes:      []string{"import_header"},
	CallTypes:        []string{"call_expression"},
	TestPrefix:       "test",
	Semantics:        KotlinSemantics{},
	PackageDiscovery: KotlinPackageDiscovery{},
}

var PHPSpec = &LangSpec{
	Name:             "php",
	FunctionTypes:    []string{"function_definition", "method_declaration"},
	ClassTypes:       []string{"class_declaration"},
	InterfaceTypes:   []string{"interface_declaration"},
	ImportTypes:      []string{"namespace_use_declaration"},
	CallTypes:        []string{"function_call_expression", "method_call_expression"},
	TestPrefix:       "test",
	PackageDiscovery: NoopPackageDiscovery{},
}

var LuaSpec = &LangSpec{
	Name:             "lua",
	FunctionTypes:    []string{"function_statement"},
	ClassTypes:       []string{},
	ImportTypes:      []string{},
	CallTypes:        []string{"function_call"},
	TestPrefix:       "test_",
	PackageDiscovery: NoopPackageDiscovery{},
}

// PackageDiscoveryOrDefault returns the configured discovery hook for a language or a no-op implementation.
// @intent ensure callers always have a valid PackageDiscovery implementation to call during repository traversal
func PackageDiscoveryOrDefault(spec *LangSpec) PackageDiscovery {
	if spec != nil && spec.PackageDiscovery != nil {
		return spec.PackageDiscovery
	}
	return NoopPackageDiscovery{}
}
