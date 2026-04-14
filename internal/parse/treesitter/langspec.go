package treesitter

import (
	"github.com/imtaebin/code-context-graph/internal/model"
)

// NodeTypeMapping maps a Tree-sitter node type to an internal graph node kind.
// @intent describe how grammar-specific node names translate into model semantics
type NodeTypeMapping struct {
	ASTType  string
	NodeKind model.NodeKind
}

// LangSpec describes the Tree-sitter grammar shapes used to extract graph data for one language.
// @intent centralize language-specific AST node names, test conventions, and extraction hints
type LangSpec struct {
	Name            string
	FunctionTypes   []string
	ClassTypes      []string
	InterfaceTypes  []string
	ImportTypes     []string
	CallTypes       []string
	TestPrefix      string
	TestAttributes  []string
	ImplTypes       []string
	ExtensionTypes  []string
	PackageNodeType string
}

var GoSpec = &LangSpec{
	Name:            "go",
	FunctionTypes:   []string{"function_declaration", "method_declaration"},
	ClassTypes:      []string{"type_declaration"},
	InterfaceTypes:  []string{},
	ImportTypes:     []string{"import_declaration", "import_spec"},
	CallTypes:       []string{"call_expression"},
	TestPrefix:      "Test",
	PackageNodeType: "package_clause",
}

var PythonSpec = &LangSpec{
	Name:          "python",
	FunctionTypes: []string{"function_definition"},
	ClassTypes:    []string{"class_definition"},
	ImportTypes:   []string{"import_statement", "import_from_statement"},
	CallTypes:     []string{"call"},
	TestPrefix:    "test_",
}

var TypeScriptSpec = &LangSpec{
	Name:          "typescript",
	FunctionTypes: []string{"function_declaration", "method_definition", "arrow_function"},
	ClassTypes:    []string{"class_declaration"},
	ImportTypes:   []string{"import_statement"},
	CallTypes:     []string{"call_expression"},
	TestPrefix:    "test",
}

var JavaSpec = &LangSpec{
	Name:           "java",
	FunctionTypes:  []string{"method_declaration", "constructor_declaration"},
	ClassTypes:     []string{"class_declaration"},
	InterfaceTypes: []string{"interface_declaration"},
	ImportTypes:    []string{"import_declaration"},
	CallTypes:      []string{"method_invocation"},
	TestPrefix:     "test",
}

var CSpec = &LangSpec{
	Name:          "c",
	FunctionTypes: []string{"function_definition"},
	ClassTypes:    []string{"struct_specifier"},
	ImportTypes:   []string{"preproc_include"},
	CallTypes:     []string{"call_expression"},
	TestPrefix:    "test_",
}

var RustSpec = &LangSpec{
	Name:           "rust",
	FunctionTypes:  []string{"function_item"},
	ClassTypes:     []string{"struct_item", "enum_item"},
	InterfaceTypes: []string{"trait_item"},
	ImportTypes:    []string{"use_declaration"},
	CallTypes:      []string{"call_expression"},
	TestPrefix:     "test_",
	TestAttributes: []string{"test"},
	ImplTypes:      []string{"impl_item"},
}

var CppSpec = &LangSpec{
	Name:           "cpp",
	FunctionTypes:  []string{"function_definition"},
	ClassTypes:     []string{"class_specifier", "struct_specifier"},
	InterfaceTypes: []string{},
	ImportTypes:    []string{"preproc_include"},
	CallTypes:      []string{"call_expression"},
	TestPrefix:     "TEST",
}

var JavaScriptSpec = &LangSpec{
	Name:          "javascript",
	FunctionTypes: []string{"function_declaration", "method_definition", "arrow_function"},
	ClassTypes:    []string{"class_declaration"},
	ImportTypes:   []string{"import_statement"},
	CallTypes:     []string{"call_expression"},
	TestPrefix:    "test",
}

var RubySpec = &LangSpec{
	Name:          "ruby",
	FunctionTypes: []string{"method"},
	ClassTypes:    []string{"class"},
	ImportTypes:   []string{"call"},
	CallTypes:     []string{"call"},
	TestPrefix:    "test_",
}

var KotlinSpec = &LangSpec{
	Name:           "kotlin",
	FunctionTypes:  []string{"function_declaration"},
	ClassTypes:     []string{"class_declaration", "object_declaration"},
	InterfaceTypes: []string{"interface_declaration"},
	ImportTypes:    []string{"import_header"},
	CallTypes:      []string{"call_expression"},
	TestPrefix:     "test",
}

var PHPSpec = &LangSpec{
	Name:           "php",
	FunctionTypes:  []string{"function_definition", "method_declaration"},
	ClassTypes:     []string{"class_declaration"},
	InterfaceTypes: []string{"interface_declaration"},
	ImportTypes:    []string{"namespace_use_declaration"},
	CallTypes:      []string{"function_call_expression", "method_call_expression"},
	TestPrefix:     "test",
}

var LuaSpec = &LangSpec{
	Name:          "lua",
	FunctionTypes: []string{"function_statement"},
	ClassTypes:    []string{},
	ImportTypes:   []string{},
	CallTypes:     []string{"function_call"},
	TestPrefix:    "test_",
}
