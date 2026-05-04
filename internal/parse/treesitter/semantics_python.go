package treesitter

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/tae2089/code-context-graph/internal/model"
)

// PythonSemantics recovers Python structural relationships not emitted directly by queries.
// @intent emit inheritance edges and docstrings while keeping the generic walker language-agnostic.
type PythonSemantics struct{}

// AdditionalEdges adds Python inherits edges for class definitions with superclasses.
// @intent capture Python class inheritance from the AST so type hierarchy queries work without query-only special cases.
func (PythonSemantics) AdditionalEdges(ctx SemanticContext) []model.Edge {
	if ctx.Root == nil {
		return nil
	}
	var edges []model.Edge
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		if n.Type() == "class_definition" {
			className := pythonClassName(n, ctx.Content)
			if className != "" {
				for _, parentName := range pythonClassParents(n, ctx.Content) {
					edges = append(edges, model.Edge{
						Kind:        model.EdgeKindInherits,
						FilePath:    ctx.FilePath,
						Line:        int(n.StartPoint().Row) + 1,
						Fingerprint: model.BuildInheritsFingerprintV2(ctx.FilePath, className, parentName),
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

// AdditionalComments adds Python docstrings as synthetic comment blocks.
// @intent surface docstrings through the same binder pipeline used for ordinary comments.
func (PythonSemantics) AdditionalComments(ctx CommentContext) []CommentBlock {
	return collectPythonDocstrings(ctx.Root, ctx.Content)
}

// pythonClassName extracts the simple class name from a Python class_definition node.
// @intent keep Python inheritance extraction logic small and explicit by isolating class-name lookup.
func pythonClassName(n *sitter.Node, content []byte) string {
	if n == nil {
		return ""
	}
	if nameNode := n.ChildByFieldName("name"); nameNode != nil {
		return strings.TrimSpace(nameNode.Content(content))
	}
	return ""
}

// pythonClassParents extracts superclass names from a Python class_definition node.
// @intent read the tree-sitter-python superclasses field into simple parent names for inherits edges.
func pythonClassParents(n *sitter.Node, content []byte) []string {
	if n == nil {
		return nil
	}
	superclasses := n.ChildByFieldName("superclasses")
	if superclasses == nil {
		return nil
	}
	var parents []string
	for i := 0; i < int(superclasses.NamedChildCount()); i++ {
		child := superclasses.NamedChild(i)
		if child == nil {
			continue
		}
		name := pythonTypeName(child, content)
		if name != "" {
			parents = append(parents, name)
		}
	}
	return parents
}

// pythonTypeName normalizes a Python superclass expression into a dotted name when possible.
// @intent support simple identifiers and dotted attribute parents in Python inheritance lists.
func pythonTypeName(n *sitter.Node, content []byte) string {
	if n == nil {
		return ""
	}
	switch n.Type() {
	case "identifier", "type":
		return strings.TrimSpace(n.Content(content))
	case "attribute":
		return strings.TrimSpace(n.Content(content))
	}
	if int(n.NamedChildCount()) == 0 {
		return strings.TrimSpace(n.Content(content))
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if name := pythonTypeName(n.NamedChild(i), content); name != "" {
			return name
		}
	}
	return ""
}

// collectPythonDocstrings traverses a Python AST and returns docstring comment blocks.
// @intent move Python docstring extraction out of Walker while preserving binder-facing behavior.
func collectPythonDocstrings(root *sitter.Node, content []byte) []CommentBlock {
	var results []CommentBlock
	walkPythonDocstrings(root, content, &results)
	return results
}

// walkPythonDocstrings recursively traverses the AST and appends docstrings.
// @intent implement Python docstring discovery separately from the generic Walker.
func walkPythonDocstrings(node *sitter.Node, content []byte, results *[]CommentBlock) {
	if node == nil {
		return
	}
	if node.Type() == "expression_statement" {
		if cb, ok := tryExtractPythonDocstring(node, content); ok {
			*results = append(*results, cb)
			return
		}
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child != nil {
			walkPythonDocstrings(child, content, results)
		}
	}
}

// tryExtractPythonDocstring returns a CommentBlock when exprStmt matches Python docstring rules.
// @intent encapsulate docstring acceptance rules so tests can lock the behavior precisely.
func tryExtractPythonDocstring(exprStmt *sitter.Node, content []byte) (CommentBlock, bool) {
	if int(exprStmt.NamedChildCount()) != 1 {
		return CommentBlock{}, false
	}
	stringNode := exprStmt.NamedChild(0)
	if stringNode == nil || stringNode.Type() != "string" {
		return CommentBlock{}, false
	}

	parent := exprStmt.Parent()
	if parent == nil {
		return CommentBlock{}, false
	}
	parentType := parent.Type()

	startLine := int(stringNode.StartPoint().Row) + 1
	endLine := int(stringNode.EndPoint().Row) + 1
	text := stringNode.Content(content)
	if !isSupportedPythonDocstringLiteral(text) {
		return CommentBlock{}, false
	}

	switch parentType {
	case "module":
		if !isFirstStringExprStmt(exprStmt, parent) {
			return CommentBlock{}, false
		}
		return CommentBlock{
			StartLine:      startLine,
			EndLine:        endLine,
			Text:           text,
			IsDocstring:    true,
			OwnerStartLine: 0,
		}, true
	case "block":
		blockParent := parent.Parent()
		if blockParent == nil {
			return CommentBlock{}, false
		}
		blockParentType := blockParent.Type()
		if blockParentType != "function_definition" && blockParentType != "class_definition" {
			return CommentBlock{}, false
		}
		if !isFirstStringExprStmt(exprStmt, parent) {
			return CommentBlock{}, false
		}
		ownerNode := blockParent
		if grandParent := blockParent.Parent(); grandParent != nil && grandParent.Type() == "decorated_definition" {
			ownerNode = grandParent
		}
		ownerStartLine := int(ownerNode.StartPoint().Row) + 1
		return CommentBlock{
			StartLine:      startLine,
			EndLine:        endLine,
			Text:           text,
			IsDocstring:    true,
			OwnerStartLine: ownerStartLine,
		}, true
	default:
		return CommentBlock{}, false
	}
}

// @intent accept only Python string literal forms that can legally act as docstrings.
func isSupportedPythonDocstringLiteral(text string) bool {
	lower := strings.ToLower(text)
	for _, quote := range []string{"\"\"\"", "'''"} {
		idx := strings.Index(lower, quote)
		if idx < 0 {
			continue
		}
		prefix := lower[:idx]
		if prefix == "" || prefix == "r" || prefix == "u" {
			return true
		}
		return false
	}
	return false
}

// isFirstStringExprStmt reports whether exprStmt is the first string-only expression statement under parentNode.
// @intent preserve Python docstring semantics that only the leading string literal counts.
func isFirstStringExprStmt(exprStmt *sitter.Node, parentNode *sitter.Node) bool {
	for i := 0; i < int(parentNode.ChildCount()); i++ {
		child := parentNode.Child(i)
		if child == nil {
			continue
		}
		if child.Type() != "expression_statement" {
			continue
		}
		if child.StartPoint().Row == exprStmt.StartPoint().Row && child.StartPoint().Column == exprStmt.StartPoint().Column {
			if int(child.NamedChildCount()) == 1 {
				nc := child.NamedChild(0)
				if nc != nil && nc.Type() == "string" {
					return true
				}
			}
		}
		return false
	}
	return false
}
