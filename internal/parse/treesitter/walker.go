// @index Tree-sitter AST parser based on tags.scm queries.
package treesitter

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/kotlin"
	"github.com/smacker/go-tree-sitter/lua"
	"github.com/smacker/go-tree-sitter/php"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/ruby"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/imtaebin/code-context-graph/internal/model"
)

//go:embed queries/*/*.scm
var queriesFS embed.FS

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

func (w *Walker) Language() string {
	return w.spec.Name
}

func (w *Walker) Parse(filePath string, content []byte) ([]model.Node, []model.Edge, error) {
	nodes, edges, _, err := w.ParseWithComments(filePath, content)
	return nodes, edges, err
}

func (w *Walker) ParseWithComments(filePath string, content []byte) ([]model.Node, []model.Edge, []CommentBlock, error) {
	lang, err := w.getLanguage()
	if err != nil {
		w.logger.Error("unsupported language", "language", w.spec.Name, "file", filePath, "error", err)
		return nil, nil, nil, err
	}

	w.logger.Debug("parsing file", "file", filePath, "language", w.spec.Name)

	parser := sitter.NewParser()
	parser.SetLanguage(lang)

	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		w.logger.Error("tree-sitter parse error", "file", filePath, "error", err)
		return nil, nil, nil, fmt.Errorf("parse error: %w", err)
	}

	root := tree.RootNode()

	queryContent, err := queriesFS.ReadFile(fmt.Sprintf("queries/%s/tags.scm", w.spec.Name))
	if err != nil {
		w.logger.Debug("no query file found for language", "language", w.spec.Name)
		queryContent = []byte{}
	}

	var nodes []model.Node
	var edges []model.Edge
	var comments []CommentBlock
	var interfaces []interfaceInfo

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

	var pkgName string

	if len(queryContent) > 0 {
		nodes, edges, pkgName, interfaces, err = w.executeQueries(queryContent, lang, root, content, filePath, nodes, edges)
		if err != nil {
			return nil, nil, nil, err
		}
	}

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

	w.resolveTestedBy(nodes, &edges, filePath, pkgName)
	w.collectComments(root, content, &comments)

	w.logger.Debug("parse completed", "file", filePath, "nodes", len(nodes), "edges", len(edges))

	return nodes, edges, comments, nil
}

func (w *Walker) executeQueries(queryContent []byte, lang *sitter.Language, root *sitter.Node, content []byte, filePath string, nodes []model.Node, edges []model.Edge) ([]model.Node, []model.Edge, string, []interfaceInfo, error) {
	q, err := sitter.NewQuery(queryContent, lang)
	if err != nil {
		return nil, nil, "", nil, fmt.Errorf("invalid query: %w", err)
	}

	qc := sitter.NewQueryCursor()
	qc.Exec(q, root)

	var pkgName string
	var interfaces []interfaceInfo

	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}

		m = qc.FilterPredicates(m, content)

		var defNode, nameNode, receiverNode, importNode, callNode, packageNode, implementsNode *sitter.Node
		var defType string

		for _, c := range m.Captures {
			capName := q.CaptureNameForId(c.Index)
			if strings.HasPrefix(capName, "definition.") {
				defNode = c.Node
				defType = strings.TrimPrefix(capName, "definition.")
			} else if strings.HasPrefix(capName, "name.") {
				subType := strings.TrimPrefix(capName, "name.")
				if subType == "receiver" {
					receiverNode = c.Node
				} else if subType == "package" {
					packageNode = c.Node
				} else if subType == "import" {
					importNode = c.Node
				} else if subType == "call" {
					callNode = c.Node
				} else {
					nameNode = c.Node
				}
			} else if capName == "reference.call" {
				if callNode == nil {
					callNode = c.Node
				}
			} else if capName == "reference.import" {
				if importNode == nil {
					importNode = c.Node
				}
			} else if capName == "reference.implements" {
				implementsNode = c.Node
			}
		}

		if packageNode != nil {
			pkgName = packageNode.Content(content)
		}

		if defNode != nil && nameNode != nil {
			name := nameNode.Content(content)
			var receiver string
			if receiverNode != nil {
				receiver = w.extractReceiverStr(receiverNode, content)
			}

			qName := w.buildQualifiedName(pkgName, receiver, name)
			startLine := int(defNode.StartPoint().Row) + 1
			endLine := int(defNode.EndPoint().Row) + 1

			kind := w.mapDefTypeToNodeKind(defType, name)

			// Handle struct embeddings and interface methods for Go
			if w.spec.Name == "go" {
				if defType == "interface" {
					typeNode := w.extractTypeNode(defNode)
					if typeNode != nil && typeNode.Type() == "interface_type" {
						methods := w.extractInterfaceMethods(typeNode, content)
						interfaces = append(interfaces, interfaceInfo{name: name, methods: methods})
					}
				} else if defType == "class" {
					typeNode := w.extractTypeNode(defNode)
					if typeNode != nil && typeNode.Type() == "struct_type" {
						embeddedEdges := w.extractEmbeddings(typeNode, content, filePath, pkgName, name)
						edges = append(edges, embeddedEdges...)
					}
				}
			}

			// For deduplication. Queries can match multiple times if overlapping
			exists := false
			for i, n := range nodes {
				if n.Name == name && n.StartLine == startLine && n.EndLine == endLine {
					// Keep the one with a receiver if we get both
					if receiver != "" && !strings.Contains(nodes[i].QualifiedName, receiver) {
						nodes[i].QualifiedName = qName
					}
					// Upgrade less specific kinds
					if n.Kind == model.NodeKindType && kind == model.NodeKindClass {
						nodes[i].Kind = kind
					}
					exists = true
					break
				}
			}
			
			if !exists {
				nodes = append(nodes, model.Node{
					QualifiedName: qName,
					Kind:          kind,
					Name:          name,
					FilePath:      filePath,
					StartLine:     startLine,
					EndLine:       endLine,
					Language:      w.spec.Name,
				})
			}
			
			if implementsNode != nil {
				traitName := implementsNode.Content(content)
				edges = append(edges, model.Edge{
					Kind:        model.EdgeKindImplements,
					FilePath:    filePath,
					Fingerprint: fmt.Sprintf("implements:%s:%s:%s", filePath, qName, traitName),
				})
			}
		}

		if callNode != nil {
			callee := w.extractCallName(callNode, content)
			if callee != "" {
				line := int(callNode.StartPoint().Row) + 1
				edges = append(edges, model.Edge{
					Kind:        model.EdgeKindCalls,
					FilePath:    filePath,
					Line:        line,
					Fingerprint: fmt.Sprintf("calls:%s:%s:%d", filePath, callee, line),
				})
			}
		}

		if importNode != nil {
			importPath := importNode.Content(content)
			importPath = strings.Trim(importPath, "\"'`")
			line := int(importNode.StartPoint().Row) + 1
			
			exists := false
			for _, e := range edges {
				if e.Kind == model.EdgeKindImportsFrom && e.Line == line && strings.Contains(e.Fingerprint, importPath) {
					exists = true
					break
				}
			}
			
			if !exists {
				edges = append(edges, model.Edge{
					Kind:        model.EdgeKindImportsFrom,
					FilePath:    filePath,
					Line:        line,
					Fingerprint: fmt.Sprintf("imports_from:%s:%s:%d", filePath, importPath, line),
				})
			}
		}
	}

	return nodes, edges, pkgName, interfaces, nil
}

func (w *Walker) extractTypeNode(defNode *sitter.Node) *sitter.Node {
	if defNode.Type() == "type_declaration" {
		for i := 0; i < int(defNode.ChildCount()); i++ {
			child := defNode.Child(i)
			if child.Type() == "type_spec" {
				return child.ChildByFieldName("type")
			}
		}
	}
	return defNode.ChildByFieldName("type")
}

func (w *Walker) extractCallName(callNode *sitter.Node, content []byte) string {
	if callNode.Type() == "call_expression" || callNode.Type() == "call" {
		fnNode := callNode.ChildByFieldName("function")
		if fnNode != nil {
			return fnNode.Content(content)
		}
	}
	return callNode.Content(content)
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

func (w *Walker) extractReceiverStr(node *sitter.Node, content []byte) string {
	res := node.Content(content)
	res = strings.TrimPrefix(res, "*")
	return res
}

func (w *Walker) mapDefTypeToNodeKind(defType string, name string) model.NodeKind {
	if w.spec.TestPrefix != "" && strings.HasPrefix(name, w.spec.TestPrefix) {
		return model.NodeKindTest
	}

	switch defType {
	case "class":
		return model.NodeKindClass
	case "interface", "type":
		return model.NodeKindType
	case "function", "method":
		return model.NodeKindFunction
	default:
		return model.NodeKindFunction
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

func (w *Walker) resolveTestedBy(nodes []model.Node, edges *[]model.Edge, filePath string, pkgName string) {
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

type CommentBlock struct {
	StartLine int
	EndLine   int
	Text      string
}

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
	case "kotlin":
		return kotlin.GetLanguage(), nil
	case "php":
		return php.GetLanguage(), nil
	case "lua":
		return lua.GetLanguage(), nil
	default:
		return nil, fmt.Errorf("unsupported language: %s", w.spec.Name)
	}
}
