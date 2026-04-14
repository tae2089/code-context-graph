// @index Tree-sitter AST parser based on tags.scm queries.
package treesitter

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"strings"
	"sync"

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
	"github.com/tae2089/trace"

	"github.com/imtaebin/code-context-graph/internal/model"
)

//go:embed queries/*/*.scm
var queriesFS embed.FS

var errUnsupportedLanguage = trace.New("unsupported language")

// Walker parses source files with Tree-sitter and emits graph nodes, edges, and comments.
// @intent turn language-specific ASTs into the project's normalized code graph representation
// @mutates parser and query during one-time initialization in NewWalker
type Walker struct {
	mu     sync.Mutex
	spec   *LangSpec
	logger *slog.Logger
	parser *sitter.Parser // 언어별 1회 초기화 후 재사용
	query  *sitter.Query  // tags.scm 쿼리 1회 컴파일 후 재사용 (nil이면 쿼리 없음)
}

// interfaceInfo captures interface method names for later implementation inference.
// @intent hold the minimum data needed to derive implicit implements edges
type interfaceInfo struct {
	name    string
	methods []string
}

// WalkerOption configures optional Walker behavior during construction.
// @intent allow caller-supplied dependencies such as logging without expanding constructor arguments
type WalkerOption func(*Walker)

// WithLogger installs a logger on a Walker.
// @intent let callers route parser diagnostics through their preferred slog.Logger
// @mutates Walker.logger
func WithLogger(l *slog.Logger) WalkerOption {
	return func(w *Walker) {
		w.logger = l
	}
}

// NewWalker creates a Walker and initializes reusable Tree-sitter resources for one language.
// @intent amortize parser and query compilation cost across many file parses
// @mutates Walker.parser, Walker.query, Walker.logger
// @requires spec is non-nil and names a supported language
// @ensures returned Walker reuses one parser and optional compiled tags query
func NewWalker(spec *LangSpec, opts ...WalkerOption) *Walker {
	w := &Walker{spec: spec}
	for _, opt := range opts {
		opt(w)
	}
	if w.logger == nil {
		w.logger = slog.Default()
	}

	// parser와 query를 1회 초기화하여 파일마다 CGO 할당 오버헤드를 제거한다.
	if lang, err := w.getLanguage(); err == nil {
		p := sitter.NewParser()
		p.SetLanguage(lang)
		w.parser = p

		qPath := fmt.Sprintf("queries/%s/tags.scm", spec.Name)
		if qContent, err := queriesFS.ReadFile(qPath); err == nil {
			if q, err := sitter.NewQuery(qContent, lang); err == nil {
				w.query = q
			} else {
				w.logger.Debug("failed to compile query", "language", spec.Name, "error", err)
			}
		} else {
			w.logger.Debug("no tags.scm found for language", "language", spec.Name)
		}
	}

	return w
}

// Close releases CGo resources held by the underlying tree-sitter parser.
// It should be called when the Walker is no longer needed.
// @intent free parser-side native resources once file parsing is complete
// @sideEffect releases CGo resources owned by the underlying parser
func (w *Walker) Close() {
	if w.parser != nil {
		w.parser.Close()
	}
}

// Language returns the Walker language name.
// @intent expose the language handled by this Walker for downstream coordination
func (w *Walker) Language() string {
	return w.spec.Name
}

// Parse parses a file and returns graph nodes and edges.
// @intent provide the basic parsing entry point when callers do not need comments or custom context
// @see treesitter.Walker.ParseWithComments
func (w *Walker) Parse(filePath string, content []byte) ([]model.Node, []model.Edge, error) {
	nodes, edges, _, err := w.ParseWithComments(context.Background(), filePath, content)
	return nodes, edges, err
}

// ParseWithContext parses filePath with the given context, allowing cancellation.
// @intent let callers cancel Tree-sitter parsing through context propagation
// @see treesitter.Walker.ParseWithComments
func (w *Walker) ParseWithContext(ctx context.Context, filePath string, content []byte) ([]model.Node, []model.Edge, error) {
	nodes, edges, _, err := w.ParseWithComments(ctx, filePath, content)
	return nodes, edges, err
}

// ParseWithComments parses a file and also extracts raw comment blocks.
// @intent produce the full parse result needed for graph building and annotation binding
// @mutates edges by appending contains, implements, and tested_by relationships
// @requires Walker parser is initialized for the language
// @ensures returned nodes always include a file node for filePath
// @see treesitter.Walker.executeQueries
func (w *Walker) ParseWithComments(ctx context.Context, filePath string, content []byte) ([]model.Node, []model.Edge, []CommentBlock, error) {
	if w.parser == nil {
		w.logger.Error("unsupported language", "language", w.spec.Name, "file", filePath)
		return nil, nil, nil, trace.Wrap(errUnsupportedLanguage, w.spec.Name)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.logger.Debug("parsing file", "file", filePath, "language", w.spec.Name)

	tree, err := w.parser.ParseCtx(ctx, nil, content)
	if err != nil {
		w.logger.Error("tree-sitter parse error", "file", filePath, "error", err)
		return nil, nil, nil, trace.Wrap(err, "parse error")
	}
	defer tree.Close()

	root := tree.RootNode()

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

	if w.query != nil {
		nodes, edges, pkgName, interfaces, err = w.executeQueries(root, content, filePath, nodes, edges)
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

// executeQueries runs the compiled tags query and converts captures into nodes and edges.
// @intent map Tree-sitter query captures into normalized graph entities for one file
// @mutates nodes and edges slices through appended parse results
// @requires w.query is compiled for w.spec.Name
// @return pkgName is the detected package/module name when the grammar exposes one
func (w *Walker) executeQueries(root *sitter.Node, content []byte, filePath string, nodes []model.Node, edges []model.Edge) ([]model.Node, []model.Edge, string, []interfaceInfo, error) {
	// w.query는 NewWalker에서 이미 컴파일됨 (불변이므로 공유 안전)
	// QueryCursor는 스레드 안전하지 않아 매번 새로 생성한다.
	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(w.query, root)

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
			capName := w.query.CaptureNameForId(c.Index)
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

// extractTypeNode returns the underlying type node from a declaration capture.
// @intent normalize Go type declarations so downstream extractors see the concrete type form
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

// extractCallName extracts the callable expression text from a call node.
// @intent derive stable callee names for call edge fingerprints across grammars
func (w *Walker) extractCallName(callNode *sitter.Node, content []byte) string {
	if callNode.Type() == "call_expression" || callNode.Type() == "call" {
		fnNode := callNode.ChildByFieldName("function")
		if fnNode != nil {
			return fnNode.Content(content)
		}
	}
	return callNode.Content(content)
}

// extractInterfaceMethods lists method names declared by an interface node.
// @intent gather interface contracts for later structural implementation matching
// @return method names declared directly on the interface node
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

// extractEmbeddings builds inherits edges for embedded Go struct fields.
// @intent capture composition-style inheritance encoded by anonymous embedded fields
// @return edges representing embedded type relationships for the struct
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

// resolveImplements infers implements edges by matching receiver methods to interface method sets.
// @intent recover implicit Go implementation relationships that are not explicit in syntax
// @domainRule a receiver implements an interface only when it defines every interface method
// @mutates edges appends inferred implements relationships
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

// extractReceiverStr normalizes receiver text for qualified-name construction.
// @intent remove pointer syntax noise from method receiver identifiers
func (w *Walker) extractReceiverStr(node *sitter.Node, content []byte) string {
	res := node.Content(content)
	res = strings.TrimPrefix(res, "*")
	return res
}

// mapDefTypeToNodeKind maps query definition labels to internal node kinds.
// @intent keep language query captures aligned with graph node categorization
// @domainRule declarations matching the configured test prefix become test nodes
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

// buildQualifiedName joins package, receiver, and declaration name into a stable identifier.
// @intent generate graph keys that distinguish methods from package-level declarations
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

// resolveTestedBy infers tested_by edges from calls made inside discovered test nodes.
// @intent connect production functions to enclosing tests without language-specific test frameworks
// @domainRule only calls inside test node line ranges create tested_by edges
// @mutates edges appends inferred tested_by relationships
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

// CommentBlock records one contiguous comment region discovered in source.
// @intent preserve raw comment text with source line bounds for later annotation binding
type CommentBlock struct {
	StartLine int
	EndLine   int
	Text      string
}

// ExtractComments parses a file and returns merged comment blocks.
// @intent expose comment extraction without forcing callers to build nodes and edges
// @requires Walker parser is initialized for the language
// @see treesitter.Walker.collectComments
func (w *Walker) ExtractComments(ctx context.Context, filePath string, content []byte) ([]CommentBlock, error) {
	if w.parser == nil {
		return nil, trace.Wrap(errUnsupportedLanguage, w.spec.Name)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	tree, err := w.parser.ParseCtx(ctx, nil, content)
	if err != nil {
		return nil, trace.Wrap(err, "parse error")
	}
	defer tree.Close()

	var comments []CommentBlock
	w.collectComments(tree.RootNode(), content, &comments)
	return comments, nil
}

// collectComments walks the AST and merges adjacent comment nodes into comment blocks.
// @intent keep documentation comments together so binders can attach them as a single unit
// @mutates comments appends or extends contiguous comment ranges in traversal order
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

// getLanguage resolves the Tree-sitter language handle for the Walker spec.
// @intent bind configured language names to the concrete parser implementation
// @return error when the configured language is not supported by this binary
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
		return nil, trace.Wrap(errUnsupportedLanguage, w.spec.Name)
	}
}
