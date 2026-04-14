// @index Tree-sitter AST 파서. 15개 언어의 소스 코드를 파싱하여 노드, 엣지, 주석을 추출한다.
package treesitter

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/bash"
	"github.com/smacker/go-tree-sitter/csharp"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/kotlin"
	"github.com/smacker/go-tree-sitter/lua"
	"github.com/smacker/go-tree-sitter/php"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/scala"
	"github.com/smacker/go-tree-sitter/swift"
	"github.com/smacker/go-tree-sitter/ruby"
	"github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/imtaebin/code-context-graph/internal/model"
)

type Walker struct {
	spec   *LangSpec
	logger *slog.Logger
}

type interfaceInfo struct {
	name    string
	methods []string
}

type WalkerOption func(*Walker)

func WithLogger(l *slog.Logger) WalkerOption {
	return func(w *Walker) {
		w.logger = l
	}
}

func NewWalker(spec *LangSpec, opts ...WalkerOption) *Walker {
	w := &Walker{spec: spec}
	for _, opt := range opts {
		opt(w)
	}
	if w.logger == nil {
		w.logger = slog.Default()
	}
	return w
}

// Language returns the language name of this walker's spec.
func (w *Walker) Language() string {
	return w.spec.Name
}

// Parse processes a source file via Tree-sitter AST and extracts nodes and edges.
// Core parsing engine supporting 15 languages.
//
// @param filePath relative path of the source file
// @param content raw source file bytes
// @return extracted nodes, edges, and any parse error
// @intent build code knowledge graph from source files
// @domainRule creates File node plus Function, Class, Type nodes per declaration
// @domainRule generates CALLS, IMPORTS_FROM, CONTAINS, INHERITS, IMPLEMENTS edges
// @sideEffect allocates Tree-sitter parser per invocation
func (w *Walker) Parse(filePath string, content []byte) ([]model.Node, []model.Edge, error) {
	lang, err := w.getLanguage()
	if err != nil {
		w.logger.Error("unsupported language", "language", w.spec.Name, "file", filePath, "error", err)
		return nil, nil, err
	}

	w.logger.Debug("parsing file", "file", filePath, "language", w.spec.Name)

	parser := sitter.NewParser()
	parser.SetLanguage(lang)

	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		w.logger.Error("tree-sitter parse error", "file", filePath, "error", err)
		return nil, nil, fmt.Errorf("parse error: %w", err)
	}

	root := tree.RootNode()
	pkgName := w.extractPackageName(root, content)

	var nodes []model.Node
	var edges []model.Edge

	fileNode := model.Node{
		QualifiedName: filePath,
		Kind:          model.NodeKindFile,
		Name:          filePath,
		FilePath:      filePath,
		StartLine:     1,
		EndLine:       int(root.EndPoint().Row) + 1,
		Language:      w.spec.Name,
	}
	nodes = append(nodes, fileNode)

	var interfaces []interfaceInfo
	w.walkNode(root, content, filePath, pkgName, &nodes, &edges, &interfaces)

	w.resolveImplements(nodes, interfaces, filePath, &edges)

	for _, n := range nodes {
		if n.Kind == model.NodeKindFile {
			continue
		}
		edges = append(edges, model.Edge{
			Kind:        model.EdgeKindContains,
			FilePath:    filePath,
			Line:        n.StartLine,
			Fingerprint: fmt.Sprintf("contains:%s:%s", filePath, n.QualifiedName),
		})
	}

	w.resolveTestedBy(root, content, filePath, pkgName, nodes, &edges)

	w.logger.Debug("parse completed", "file", filePath, "nodes", len(nodes), "edges", len(edges))

	return nodes, edges, nil
}

// ParseWithComments parses a file and extracts nodes, edges, and comment blocks in a single pass.
func (w *Walker) ParseWithComments(filePath string, content []byte) ([]model.Node, []model.Edge, []CommentBlock, error) {
	lang, err := w.getLanguage()
	if err != nil {
		return nil, nil, nil, err
	}

	parser := sitter.NewParser()
	parser.SetLanguage(lang)

	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse error: %w", err)
	}

	root := tree.RootNode()
	pkgName := w.extractPackageName(root, content)

	var nodes []model.Node
	var edges []model.Edge
	var comments []CommentBlock

	fileNode := model.Node{
		QualifiedName: filePath,
		Kind:          model.NodeKindFile,
		Name:          filePath,
		FilePath:      filePath,
		StartLine:     1,
		EndLine:       int(root.EndPoint().Row) + 1,
		Language:      w.spec.Name,
	}
	nodes = append(nodes, fileNode)

	var interfaces []interfaceInfo
	w.walkNode(root, content, filePath, pkgName, &nodes, &edges, &interfaces)
	w.collectComments(root, content, &comments)

	w.resolveImplements(nodes, interfaces, filePath, &edges)

	for _, n := range nodes {
		if n.Kind == model.NodeKindFile {
			continue
		}
		edges = append(edges, model.Edge{
			Kind:        model.EdgeKindContains,
			FilePath:    filePath,
			Line:        n.StartLine,
			Fingerprint: fmt.Sprintf("contains:%s:%s", filePath, n.QualifiedName),
		})
	}

	w.resolveTestedBy(root, content, filePath, pkgName, nodes, &edges)

	return nodes, edges, comments, nil
}

func (w *Walker) walkNode(node *sitter.Node, content []byte, filePath, pkgName string, nodes *[]model.Node, edges *[]model.Edge, ifaces *[]interfaceInfo) {
	nodeType := node.Type()

	if w.isFunctionType(nodeType) {
		n := w.extractFunction(node, content, filePath, pkgName)
		if n != nil {
			*nodes = append(*nodes, *n)
		}
	} else if w.isInterfaceType(nodeType) {
		n := w.extractClassGeneric(node, content, filePath, pkgName)
		if n != nil {
			n.Kind = model.NodeKindType
			*nodes = append(*nodes, *n)
		}
	} else if w.isClassType(nodeType) {
		// Swift extension: class_declaration의 첫 자식이 "extension"이면 extension으로 처리
		if w.isSwiftExtension(node) {
			typeName := w.extractExtensionTypeName(node, content)
			if typeName != "" {
				w.extractImplFunctions(node, content, filePath, pkgName, typeName, nodes, edges)
			}
		} else if w.spec.Name == "go" {
			extracted := w.extractTypeDecl(node, content, filePath, pkgName, ifaces, edges)
			*nodes = append(*nodes, extracted...)
		} else {
			n := w.extractClassGeneric(node, content, filePath, pkgName)
			if n != nil {
				*nodes = append(*nodes, *n)
			}
		}
	} else if w.isImplType(nodeType) || w.isExtensionType(nodeType) {
		w.processImplBlock(node, content, filePath, pkgName, nodes, edges)
	} else if w.isCallType(nodeType) || w.isImportType(nodeType) {
		// Bash: command 노드가 call과 import 양쪽에 있을 수 있음
		if w.isImportType(nodeType) {
			importEdges := w.extractImports(node, content, filePath)
			if len(importEdges) > 0 {
				*edges = append(*edges, importEdges...)
			} else if w.isCallType(nodeType) {
				e := w.extractCall(node, content, filePath)
				if e != nil {
					*edges = append(*edges, *e)
				}
			}
		} else {
			e := w.extractCall(node, content, filePath)
			if e != nil {
				*edges = append(*edges, *e)
			}
		}
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child != nil {
			w.walkNode(child, content, filePath, pkgName, nodes, edges, ifaces)
		}
	}
}

func (w *Walker) extractPackageName(root *sitter.Node, content []byte) string {
	for i := 0; i < int(root.ChildCount()); i++ {
		child := root.Child(i)
		if child != nil && child.Type() == "package_clause" {
			for j := 0; j < int(child.ChildCount()); j++ {
				gc := child.Child(j)
				if gc != nil && gc.Type() == "package_identifier" {
					return gc.Content(content)
				}
			}
		}
	}
	return ""
}

func (w *Walker) extractFunction(node *sitter.Node, content []byte, filePath, pkgName string) *model.Node {
	var name string
	var receiver string

	if node.Type() == "method_declaration" {
		nameNode := node.ChildByFieldName("name")
		if nameNode != nil {
			name = nameNode.Content(content)
		}
		recvNode := node.ChildByFieldName("receiver")
		if recvNode != nil {
			receiver = w.extractReceiver(recvNode, content)
		}
	} else {
		nameNode := node.ChildByFieldName("name")
		if nameNode != nil {
			name = nameNode.Content(content)
		}
		// C/C++: function_definition → declarator(function_declarator) → declarator(identifier)
		if name == "" {
			declNode := node.ChildByFieldName("declarator")
			if declNode != nil {
				if declNode.Type() == "function_declarator" {
					innerDecl := declNode.ChildByFieldName("declarator")
					if innerDecl != nil {
						name = innerDecl.Content(content)
					}
				}
			}
		}
		// Lua/Bash 등: name 필드 없는 경우 자식에서 이름 추출
		if name == "" {
			for i := 0; i < int(node.ChildCount()); i++ {
				child := node.Child(i)
				if child == nil {
					continue
				}
				ct := child.Type()
				if ct == "function_name" || ct == "identifier" || ct == "word" || ct == "simple_identifier" {
					// function_name 내부에 identifier가 있을 수 있음
					if ct == "function_name" {
						for j := 0; j < int(child.ChildCount()); j++ {
							gc := child.Child(j)
							if gc != nil && gc.Type() == "identifier" {
								name = gc.Content(content)
								break
							}
						}
					}
					if name == "" {
						name = child.Content(content)
					}
					break
				}
			}
		}
	}

	if name == "" {
		// Arrow function: 부모가 variable_declarator이면 변수명을 함수명으로 사용
		parent := node.Parent()
		if parent != nil && parent.Type() == "variable_declarator" {
			nameNode := parent.ChildByFieldName("name")
			if nameNode != nil {
				name = nameNode.Content(content)
			}
		}
	}

	if name == "" {
		return nil
	}

	qName := w.buildQualifiedName(pkgName, receiver, name)
	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	kind := model.NodeKindFunction
	if w.spec.TestPrefix != "" && strings.HasPrefix(name, w.spec.TestPrefix) {
		kind = model.NodeKindTest
	}

	return &model.Node{
		QualifiedName: qName,
		Kind:          kind,
		Name:          name,
		FilePath:      filePath,
		StartLine:     startLine,
		EndLine:       endLine,
		Language:      w.spec.Name,
	}
}

func (w *Walker) extractReceiver(recvNode *sitter.Node, content []byte) string {
	for i := 0; i < int(recvNode.ChildCount()); i++ {
		child := recvNode.Child(i)
		if child == nil {
			continue
		}
		if child.Type() == "parameter_declaration" {
			typeNode := child.ChildByFieldName("type")
			if typeNode != nil {
				typeName := typeNode.Content(content)
				typeName = strings.TrimPrefix(typeName, "*")
				return typeName
			}
		}
	}
	return ""
}

func (w *Walker) extractClassGeneric(node *sitter.Node, content []byte, filePath, pkgName string) *model.Node {
	nameNode := node.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := nameNode.Content(content)
	qName := w.buildQualifiedName(pkgName, "", name)
	startLine := int(node.StartPoint().Row) + 1
	endLine := int(node.EndPoint().Row) + 1

	return &model.Node{
		QualifiedName: qName,
		Kind:          model.NodeKindClass,
		Name:          name,
		FilePath:      filePath,
		StartLine:     startLine,
		EndLine:       endLine,
		Language:      w.spec.Name,
	}
}

func (w *Walker) extractTypeDecl(node *sitter.Node, content []byte, filePath, pkgName string, ifaces *[]interfaceInfo, edges *[]model.Edge) []model.Node {
	var result []model.Node

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		if child.Type() == "type_spec" {
			nameNode := child.ChildByFieldName("name")
			typeNode := child.ChildByFieldName("type")
			if nameNode == nil {
				continue
			}

			name := nameNode.Content(content)
			kind := model.NodeKindClass

			if typeNode != nil && typeNode.Type() == "interface_type" {
				kind = model.NodeKindType
				methods := w.extractInterfaceMethods(typeNode, content)
				*ifaces = append(*ifaces, interfaceInfo{name: name, methods: methods})
			}

			if typeNode != nil && typeNode.Type() == "struct_type" {
				embeddedEdges := w.extractEmbeddings(typeNode, content, filePath, pkgName, name)
				*edges = append(*edges, embeddedEdges...)
			}

			qName := w.buildQualifiedName(pkgName, "", name)
			startLine := int(child.StartPoint().Row) + 1
			endLine := int(child.EndPoint().Row) + 1

			result = append(result, model.Node{
				QualifiedName: qName,
				Kind:          kind,
				Name:          name,
				FilePath:      filePath,
				StartLine:     startLine,
				EndLine:       endLine,
				Language:      w.spec.Name,
			})
		}
	}

	return result
}

func (w *Walker) extractCall(node *sitter.Node, content []byte, filePath string) *model.Edge {
	fnNode := node.ChildByFieldName("function")
	var callee string
	if fnNode != nil {
		callee = fnNode.Content(content)
	} else {
		// Swift: simple_identifier, Lua: identifier, Bash: command_name
		callee = w.extractCalleeFromChildren(node, content)
	}

	if callee == "" {
		return nil
	}

	line := int(node.StartPoint().Row) + 1

	return &model.Edge{
		Kind:        model.EdgeKindCalls,
		FilePath:    filePath,
		Line:        line,
		Fingerprint: fmt.Sprintf("calls:%s:%s:%d", filePath, callee, line),
	}
}

func (w *Walker) extractCalleeFromChildren(node *sitter.Node, content []byte) string {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		nodeType := child.Type()
		if nodeType == "simple_identifier" || nodeType == "identifier" || nodeType == "command_name" {
			return child.Content(content)
		}
	}
	return ""
}

func (w *Walker) extractImports(node *sitter.Node, content []byte, filePath string) []model.Edge {
	var edges []model.Edge

	if w.spec.Name == "go" {
		w.collectImportSpecs(node, content, filePath, &edges)
	} else {
		// 범용 import 처리: 노드의 문자열 자식에서 import 경로 추출
		importPath := w.extractImportPath(node, content)
		if importPath != "" {
			line := int(node.StartPoint().Row) + 1
			edges = append(edges, model.Edge{
				Kind:        model.EdgeKindImportsFrom,
				FilePath:    filePath,
				Line:        line,
				Fingerprint: fmt.Sprintf("imports_from:%s:%s:%d", filePath, importPath, line),
			})
		}
	}

	return edges
}

func (w *Walker) extractImportPath(node *sitter.Node, content []byte) string {
	// 문자열 리터럴에서 import 경로 추출
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		nodeType := child.Type()
		if nodeType == "string" || nodeType == "interpreted_string_literal" || nodeType == "string_literal" || nodeType == "system_lib_string" {
			path := child.Content(content)
			path = strings.Trim(path, "\"'`")
			return path
		}
		// path 필드 확인
		if nodeType == "path" || nodeType == "name" {
			return child.Content(content)
		}
		// 재귀적으로 source 필드 확인
		sourceNode := child.ChildByFieldName("source")
		if sourceNode != nil {
			path := sourceNode.Content(content)
			path = strings.Trim(path, "\"'`")
			return path
		}
	}
	// source 필드 직접 확인
	sourceNode := node.ChildByFieldName("source")
	if sourceNode != nil {
		path := sourceNode.Content(content)
		path = strings.Trim(path, "\"'`")
		return path
	}
	// path 필드 직접 확인
	pathNode := node.ChildByFieldName("path")
	if pathNode != nil {
		path := pathNode.Content(content)
		path = strings.Trim(path, "\"'`")
		return path
	}
	// scoped_identifier, identifier 등 비문자열 노드에서 경로 추출 (Rust use, etc.)
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		nodeType := child.Type()
		if nodeType == "scoped_identifier" || nodeType == "identifier" || nodeType == "scoped_use_list" {
			return child.Content(content)
		}
		// PHP: namespace_use_clause -> qualified_name
		if nodeType == "namespace_use_clause" {
			return child.Content(content)
		}
		// Swift: import_declaration 내 identifier
		if nodeType == "simple_identifier" {
			return child.Content(content)
		}
		// Bash: command에서 source 다음의 word를 import 경로로 추출
		if nodeType == "word" && i > 0 {
			return child.Content(content)
		}
	}
	// Bash source 감지: command 노드에서 command_name이 "source"이면 다음 word를 import로 처리
	if node.Type() == "command" {
		cmdName := ""
		for i := 0; i < int(node.ChildCount()); i++ {
			child := node.Child(i)
			if child == nil {
				continue
			}
			if child.Type() == "command_name" {
				cmdName = child.Content(content)
			}
			if child.Type() == "word" && (cmdName == "source" || cmdName == ".") {
				return child.Content(content)
			}
		}
	}
	return ""
}

func (w *Walker) collectImportSpecs(node *sitter.Node, content []byte, filePath string, edges *[]model.Edge) {
	if node.Type() == "import_spec" {
		pathNode := node.ChildByFieldName("path")
		if pathNode != nil {
			importPath := pathNode.Content(content)
			importPath = strings.Trim(importPath, "\"")
			line := int(node.StartPoint().Row) + 1
			*edges = append(*edges, model.Edge{
				Kind:        model.EdgeKindImportsFrom,
				FilePath:    filePath,
				Line:        line,
				Fingerprint: fmt.Sprintf("imports_from:%s:%s:%d", filePath, importPath, line),
			})
		}
		return
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child != nil {
			w.collectImportSpecs(child, content, filePath, edges)
		}
	}
}

func (w *Walker) extractInterfaceMethods(ifaceNode *sitter.Node, content []byte) []string {
	var methods []string
	for i := 0; i < int(ifaceNode.ChildCount()); i++ {
		child := ifaceNode.Child(i)
		if child == nil {
			continue
		}
		if child.Type() == "method_spec" || child.Type() == "method_elem" {
			nameNode := child.ChildByFieldName("name")
			if nameNode == nil {
				for j := 0; j < int(child.ChildCount()); j++ {
					gc := child.Child(j)
					if gc != nil && gc.Type() == "field_identifier" {
						methods = append(methods, gc.Content(content))
						break
					}
				}
			} else {
				methods = append(methods, nameNode.Content(content))
			}
		}
	}
	return methods
}

func (w *Walker) extractEmbeddings(structNode *sitter.Node, content []byte, filePath, pkgName, structName string) []model.Edge {
	var edges []model.Edge
	for i := 0; i < int(structNode.ChildCount()); i++ {
		child := structNode.Child(i)
		if child == nil {
			continue
		}
		if child.Type() == "field_declaration_list" {
			for j := 0; j < int(child.ChildCount()); j++ {
				field := child.Child(j)
				if field == nil || field.Type() != "field_declaration" {
					continue
				}
				hasFieldName := false
				var typeName string
				for k := 0; k < int(field.ChildCount()); k++ {
					fc := field.Child(k)
					if fc == nil {
						continue
					}
					if fc.Type() == "field_identifier" {
						hasFieldName = true
					}
					if fc.Type() == "type_identifier" {
						typeName = fc.Content(content)
					}
					if fc.Type() == "pointer_type" {
						typeName = strings.TrimPrefix(fc.Content(content), "*")
					}
				}
				if !hasFieldName && typeName != "" {
					line := int(field.StartPoint().Row) + 1
					edges = append(edges, model.Edge{
						Kind:        model.EdgeKindInherits,
						FilePath:    filePath,
						Line:        line,
						Fingerprint: fmt.Sprintf("inherits:%s:%s:%s", filePath, structName, typeName),
					})
				}
			}
		}
	}
	return edges
}

func (w *Walker) resolveImplements(nodes []model.Node, ifaces []interfaceInfo, filePath string, edges *[]model.Edge) {
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
				*edges = append(*edges, model.Edge{
					Kind:        model.EdgeKindImplements,
					FilePath:    filePath,
					Fingerprint: fmt.Sprintf("implements:%s:%s:%s", filePath, receiver, iface.name),
				})
			}
		}
	}
}

func (w *Walker) buildQualifiedName(pkg, receiver, name string) string {
	var parts []string
	if pkg != "" {
		parts = append(parts, pkg)
	}
	if receiver != "" {
		parts = append(parts, receiver)
	}
	parts = append(parts, name)
	return strings.Join(parts, ".")
}

func (w *Walker) isFunctionType(nodeType string) bool {
	for _, t := range w.spec.FunctionTypes {
		if t == nodeType {
			return true
		}
	}
	return false
}

func (w *Walker) isClassType(nodeType string) bool {
	for _, t := range w.spec.ClassTypes {
		if t == nodeType {
			return true
		}
	}
	return false
}

func (w *Walker) isCallType(nodeType string) bool {
	for _, t := range w.spec.CallTypes {
		if t == nodeType {
			return true
		}
	}
	return false
}

func (w *Walker) isSwiftExtension(node *sitter.Node) bool {
	if len(w.spec.ExtensionTypes) == 0 {
		return false
	}
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child != nil && child.Type() == "extension" {
			return true
		}
	}
	return false
}

func (w *Walker) isImplType(nodeType string) bool {
	for _, t := range w.spec.ImplTypes {
		if t == nodeType {
			return true
		}
	}
	return false
}

func (w *Walker) isExtensionType(nodeType string) bool {
	for _, t := range w.spec.ExtensionTypes {
		if t == nodeType {
			return true
		}
	}
	return false
}

func (w *Walker) processImplBlock(node *sitter.Node, content []byte, filePath, pkgName string, nodes *[]model.Node, edges *[]model.Edge) {
	// impl/extension 블록에서 타입 이름과 trait 이름 추출
	var typeName string
	var traitName string

	if w.spec.Name == "rust" {
		// Rust: impl Foo { ... } 또는 impl Trait for Foo { ... }
		typeName, traitName = w.extractRustImplInfo(node, content)
	} else if w.spec.Name == "swift" {
		// Swift: extension Foo { ... }
		typeName = w.extractExtensionTypeName(node, content)
	}

	if typeName == "" {
		return
	}

	// impl Trait for Type → IMPLEMENTS 엣지
	if traitName != "" {
		*edges = append(*edges, model.Edge{
			Kind:        model.EdgeKindImplements,
			FilePath:    filePath,
			Fingerprint: fmt.Sprintf("implements:%s:%s:%s", filePath, typeName, traitName),
		})
	}

	// impl/extension 블록 내 함수를 찾아 노드 추가 + CONTAINS 엣지 생성
	w.extractImplFunctions(node, content, filePath, pkgName, typeName, nodes, edges)
}

func (w *Walker) extractRustImplInfo(node *sitter.Node, content []byte) (typeName, traitName string) {
	// impl [Trait for] Type { ... }
	// 자식 순서: "impl" [trait_type "for"] type_identifier declaration_list
	hasFor := false
	var typeIdentifiers []string

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		if child.Type() == "for" {
			hasFor = true
		}
		if child.Type() == "type_identifier" {
			typeIdentifiers = append(typeIdentifiers, child.Content(content))
		}
	}

	if hasFor && len(typeIdentifiers) >= 2 {
		// impl Trait for Type
		traitName = typeIdentifiers[0]
		typeName = typeIdentifiers[1]
	} else if len(typeIdentifiers) >= 1 {
		// impl Type
		typeName = typeIdentifiers[0]
	}

	return
}

func (w *Walker) extractExtensionTypeName(node *sitter.Node, content []byte) string {
	// Swift: extension TypeName { ... }
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		if child.Type() == "type_identifier" || child.Type() == "simple_identifier" || child.Type() == "user_type" {
			return child.Content(content)
		}
	}
	return ""
}

func (w *Walker) extractImplFunctions(node *sitter.Node, content []byte, filePath, pkgName, typeName string, nodes *[]model.Node, edges *[]model.Edge) {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		if w.isFunctionType(child.Type()) {
			// 함수 이름만 추출하여 CONTAINS 엣지 생성 (노드 자체는 walkNode가 추가)
			fn := w.extractFunction(child, content, filePath, pkgName)
			if fn != nil {
				*edges = append(*edges, model.Edge{
					Kind:        model.EdgeKindContains,
					FilePath:    filePath,
					Line:        fn.StartLine,
					Fingerprint: fmt.Sprintf("contains:%s:%s:%s", filePath, typeName, fn.QualifiedName),
				})
			}
		}
		// 재귀적으로 declaration_list, function_body 등 내부 탐색
		if child.Type() == "declaration_list" || child.Type() == "class_body" {
			w.extractImplFunctions(child, content, filePath, pkgName, typeName, nodes, edges)
		}
	}
}

func (w *Walker) isInterfaceType(nodeType string) bool {
	for _, t := range w.spec.InterfaceTypes {
		if t == nodeType {
			return true
		}
	}
	return false
}

func (w *Walker) isImportType(nodeType string) bool {
	for _, t := range w.spec.ImportTypes {
		if t == nodeType {
			return true
		}
	}
	return false
}

func (w *Walker) resolveTestedBy(root *sitter.Node, content []byte, filePath, pkgName string, nodes []model.Node, edges *[]model.Edge) {
	if w.spec.TestPrefix == "" {
		return
	}

	testNodes := make(map[string]model.Node)
	nonTestNames := make(map[string]bool)

	for _, n := range nodes {
		if n.Kind == model.NodeKindTest {
			testNodes[n.QualifiedName] = n
		} else if n.Kind == model.NodeKindFunction {
			nonTestNames[n.Name] = true
		}
	}

	for _, e := range *edges {
		if e.Kind != model.EdgeKindCalls {
			continue
		}
		calleeName := e.Fingerprint
		parts := strings.Split(calleeName, ":")
		if len(parts) < 3 {
			continue
		}
		callee := parts[2]

		calleeParts := strings.Split(callee, ".")
		bareCallee := calleeParts[len(calleeParts)-1]

		if !nonTestNames[bareCallee] {
			continue
		}

		for testQName, testNode := range testNodes {
			if e.Line >= testNode.StartLine && e.Line <= testNode.EndLine {
				*edges = append(*edges, model.Edge{
					Kind:        model.EdgeKindTestedBy,
					FilePath:    filePath,
					Line:        e.Line,
					Fingerprint: fmt.Sprintf("tested_by:%s:%s:%s", filePath, bareCallee, testQName),
				})
			}
		}
	}
}

// CommentBlock represents a contiguous block of comment lines.
type CommentBlock struct {
	StartLine int
	EndLine   int
	Text      string
}

// ExtractComments parses the file and returns comment blocks.
func (w *Walker) ExtractComments(filePath string, content []byte) ([]CommentBlock, error) {
	lang, err := w.getLanguage()
	if err != nil {
		return nil, err
	}

	parser := sitter.NewParser()
	parser.SetLanguage(lang)

	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	var comments []CommentBlock
	w.collectComments(tree.RootNode(), content, &comments)
	return comments, nil
}

func (w *Walker) collectComments(node *sitter.Node, content []byte, comments *[]CommentBlock) {
	nodeType := node.Type()

	if nodeType == "comment" || nodeType == "line_comment" || nodeType == "block_comment" {
		startLine := int(node.StartPoint().Row) + 1
		endLine := int(node.EndPoint().Row) + 1
		text := node.Content(content)

		// Merge with previous comment if adjacent
		if len(*comments) > 0 {
			last := &(*comments)[len(*comments)-1]
			if startLine-last.EndLine <= 1 {
				last.EndLine = endLine
				last.Text += "\n" + text
				return
			}
		}

		*comments = append(*comments, CommentBlock{
			StartLine: startLine,
			EndLine:   endLine,
			Text:      text,
		})
		return
	}

	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child != nil {
			w.collectComments(child, content, comments)
		}
	}
}

func (w *Walker) getLanguage() (*sitter.Language, error) {
	switch w.spec.Name {
	case "go":
		return golang.GetLanguage(), nil
	case "python":
		return python.GetLanguage(), nil
	case "typescript":
		return typescript.GetLanguage(), nil
	case "java":
		return java.GetLanguage(), nil
	case "ruby":
		return ruby.GetLanguage(), nil
	case "javascript":
		return javascript.GetLanguage(), nil
	case "c":
		return c.GetLanguage(), nil
	case "cpp":
		return cpp.GetLanguage(), nil
	case "rust":
		return rust.GetLanguage(), nil
	case "csharp":
		return csharp.GetLanguage(), nil
	case "kotlin":
		return kotlin.GetLanguage(), nil
	case "php":
		return php.GetLanguage(), nil
	case "swift":
		return swift.GetLanguage(), nil
	case "scala":
		return scala.GetLanguage(), nil
	case "lua":
		return lua.GetLanguage(), nil
	case "bash":
		return bash.GetLanguage(), nil
	default:
		return nil, fmt.Errorf("unsupported language: %s", w.spec.Name)
	}
}
