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

	"github.com/tae2089/code-context-graph/internal/model"
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
	if w.query != nil {
		w.query.Close()
	}
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

	// Python 전용: docstring을 CommentBlock으로 수집 후 StartLine 오름차순 병합.
	// docstring은 IsDocstring=true, OwnerStartLine=소속 심볼 StartLine으로 설정되어
	// binder가 gap 로직 대신 OwnerStartLine 일치로 바인딩한다.
	if w.spec.Name == "python" {
		docstrings := w.collectDocstrings(root, content, nodes)
		comments = mergeCommentBlocks(comments, docstrings)
	}

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

	// nodeKey → index in nodes slice for O(1) dedup lookup
	type nodeKey struct {
		name      string
		startLine int
		endLine   int
	}
	nodeIndex := make(map[nodeKey]int)
	for i, n := range nodes {
		nodeIndex[nodeKey{n.Name, n.StartLine, n.EndLine}] = i
	}

	// nameIndex: Name → index in nodes slice. 같은 이름의 심볼이 여러 쿼리 패턴에
	// 의해 중복 매칭될 때(예: decorated_definition + function_definition) StartLine이
	// 더 작은 쪽(데코레이터 첫 줄)을 우선 보존하기 위한 보조 인덱스.
	nameIndex := make(map[string]int)
	for i, n := range nodes {
		if n.Kind != model.NodeKindFile {
			nameIndex[n.Name] = i
		}
	}

	// import dedup: "importPath:line" → true
	importSeen := make(map[string]bool)

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

			// O(1) dedup via map. Queries can match multiple times if overlapping.
			key := nodeKey{name, startLine, endLine}
			if idx, exists := nodeIndex[key]; exists {
				// Keep the one with a receiver if we get both
				if receiver != "" && !strings.Contains(nodes[idx].QualifiedName, receiver) {
					nodes[idx].QualifiedName = qName
				}
				// Upgrade less specific kinds
				if nodes[idx].Kind == model.NodeKindType && kind == model.NodeKindClass {
					nodes[idx].Kind = kind
				}
			} else if idx, exists := nameIndex[name]; exists && rangesOverlap(nodes[idx].StartLine, nodes[idx].EndLine, startLine, endLine) {
				// 같은 이름의 심볼이 이미 등록된 경우: decorated_definition + function_definition처럼
				// 래퍼 노드와 내부 노드가 같은 함수를 중복 매칭할 때 발생.
				// 범위가 겹치는(overlap) 경우에만 같은 심볼로 간주해 dedup한다.
				// 겹치지 않으면 같은 이름의 별개 심볼(예: 서로 다른 클래스의 동명 메서드)이므로
				// 아래 else 분기에서 별도 노드로 추가된다.
				// StartLine이 더 작은 쪽(래퍼 노드, 데코레이터 첫 줄)을 우선 보존한다.
				slog.Debug("중복 심볼 감지: 이름 기준 dedup 적용",
					"name", name, "existing_start", nodes[idx].StartLine, "new_start", startLine)
				if startLine < nodes[idx].StartLine {
					// 새 매칭이 더 위에서 시작 → 기존 엔트리를 갱신
					oldKey := nodeKey{nodes[idx].Name, nodes[idx].StartLine, nodes[idx].EndLine}
					delete(nodeIndex, oldKey)
					nodes[idx].StartLine = startLine
					nodes[idx].EndLine = endLine
					nodes[idx].QualifiedName = qName
					nodeIndex[nodeKey{name, startLine, endLine}] = idx
				}
				// 새 매칭이 더 아래에서 시작하면(내부 function_definition) 기존 유지, 무시
			} else {
				nodeIndex[key] = len(nodes)
				nameIndex[name] = len(nodes)
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

			importKey := fmt.Sprintf("%s:%d", importPath, line)
			if !importSeen[importKey] {
				importSeen[importKey] = true
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
	StartLine      int
	EndLine        int
	Text           string
	IsDocstring    bool // Python docstring 여부 (true이면 OwnerStartLine으로 바인딩)
	OwnerStartLine int  // docstring이 귀속된 심볼의 StartLine (모듈 docstring은 0)
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

	if nodeType == "comment" || nodeType == "line_comment" || nodeType == "block_comment" || nodeType == "multiline_comment" {
		startLine := int(node.StartPoint().Row) + 1
		endLine := int(node.EndPoint().Row) + 1
		// tree-sitter reports EndPoint.Row as the NEXT row for line_comment nodes that end
		// exactly at a line boundary (EndPoint.Column == 0).  Normalize to the actual last
		// line so that gap calculation in the binder is correct.
		// Example: Rust `///` at source line N → raw EndPoint.Row = N, Column = 0 → endLine
		// would be N+1 without this correction, causing gap = 0 when the declaration is on
		// line N+1.
		if nodeType == "line_comment" && node.EndPoint().Column == 0 && endLine > startLine {
			endLine--
		}
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

// collectDocstrings는 Python AST를 탐색하여 docstring CommentBlock 목록을 반환한다.
//
// 수집 조건 (Python PEP 257 기반):
//   - 노드 타입이 expression_statement이고
//   - 유일한 named child가 string 타입이며
//   - 부모가 block 또는 module인 경우:
//     - block: 부모 체인에 function_definition 또는 class_definition이 있어야 하고
//       block 내 첫 번째 expression_statement>string만 수집한다.
//     - module: 모듈 레벨 docstring (OwnerStartLine=0)
//
// @intent Python docstring 노드를 CommentBlock으로 승격하여 binder가 처리할 수 있게 준비
// @sideEffect nodes 슬라이스를 참조하여 심볼 StartLine을 조회함 (변경 없음)
// @requires w.spec.Name == "python"
func (w *Walker) collectDocstrings(root *sitter.Node, content []byte, nodes []model.Node) []CommentBlock {
	// 심볼 StartLine → 노드 인덱스 맵 (OwnerStartLine 결정에 사용)
	startLineToNode := make(map[int]model.Node, len(nodes))
	for _, n := range nodes {
		if n.Kind != model.NodeKindFile {
			startLineToNode[n.StartLine] = n
		}
	}

	var results []CommentBlock
	w.walkDocstrings(root, content, &results)
	return results
}

// walkDocstrings는 AST를 재귀 탐색하며 Python docstring을 수집한다.
// @intent collectDocstrings의 재귀 탐색 구현
func (w *Walker) walkDocstrings(node *sitter.Node, content []byte, results *[]CommentBlock) {
	if node == nil {
		return
	}

	nodeType := node.Type()

	// expression_statement 발견 시 docstring 여부 판별
	if nodeType == "expression_statement" {
		if cb, ok := w.tryExtractDocstring(node, content); ok {
			*results = append(*results, cb)
			// docstring 이후 자식은 탐색할 필요 없음
			return
		}
	}

	// 자식 노드 재귀 탐색
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child != nil {
			w.walkDocstrings(child, content, results)
		}
	}
}

// tryExtractDocstring은 expression_statement 노드가 docstring 조건을 충족하면
// CommentBlock을 반환한다. 조건 불충족 시 두 번째 반환값이 false.
//
// 조건:
//  1. named child가 정확히 1개이고 그 타입이 "string"
//  2. 부모가 "block" 또는 "module"
//  3. block인 경우: block의 부모가 function_definition 또는 class_definition
//     (decorated_definition으로 감싸진 경우 decorated_definition.StartLine 사용)
//  4. block 안에서 첫 번째 expression_statement>string만 수집
//
// @intent Python docstring 수집 조건 판별과 CommentBlock 생성을 단일 함수로 캡슐화
func (w *Walker) tryExtractDocstring(exprStmt *sitter.Node, content []byte) (CommentBlock, bool) {
	// 조건 1: named child가 정확히 1개이고 타입이 "string"
	if int(exprStmt.NamedChildCount()) != 1 {
		return CommentBlock{}, false
	}
	stringNode := exprStmt.NamedChild(0)
	if stringNode == nil || stringNode.Type() != "string" {
		return CommentBlock{}, false
	}

	// 조건 2: 부모 타입 확인
	parent := exprStmt.Parent()
	if parent == nil {
		return CommentBlock{}, false
	}
	parentType := parent.Type()

	startLine := int(stringNode.StartPoint().Row) + 1
	endLine := int(stringNode.EndPoint().Row) + 1
	text := stringNode.Content(content)

	switch parentType {
	case "module":
		// 모듈 docstring: OwnerStartLine=0
		// 모듈 내 첫 번째 expression_statement>string인지 확인
		if !isFirstStringExprStmt(exprStmt, parent) {
			return CommentBlock{}, false
		}
		w.logger.Debug("Python 모듈 docstring 수집",
			"startLine", startLine, "endLine", endLine)
		return CommentBlock{
			StartLine:      startLine,
			EndLine:        endLine,
			Text:           text,
			IsDocstring:    true,
			OwnerStartLine: 0,
		}, true

	case "block":
		// 조건 3: block의 부모가 function_definition 또는 class_definition
		blockParent := parent.Parent()
		if blockParent == nil {
			return CommentBlock{}, false
		}
		blockParentType := blockParent.Type()
		if blockParentType != "function_definition" && blockParentType != "class_definition" {
			return CommentBlock{}, false
		}

		// 조건 4: block 내 첫 번째 expression_statement>string만 수집
		if !isFirstStringExprStmt(exprStmt, parent) {
			return CommentBlock{}, false
		}

		// OwnerStartLine 결정:
		// function_definition/class_definition의 부모가 decorated_definition이면
		// decorated_definition의 StartLine을 사용 (결정 A와 일치).
		ownerNode := blockParent
		if grandParent := blockParent.Parent(); grandParent != nil &&
			grandParent.Type() == "decorated_definition" {
			ownerNode = grandParent
		}
		ownerStartLine := int(ownerNode.StartPoint().Row) + 1

		w.logger.Debug("Python 함수/클래스 docstring 수집",
			"ownerType", blockParentType,
			"ownerStartLine", ownerStartLine,
			"startLine", startLine,
			"endLine", endLine)

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

// isFirstStringExprStmt는 exprStmt가 parentNode의 자식 중
// 첫 번째 expression_statement>string 노드인지 확인한다.
// @intent block 또는 module 내에서 두 번째 이후 string expression은 docstring이 아님
func isFirstStringExprStmt(exprStmt *sitter.Node, parentNode *sitter.Node) bool {
	for i := 0; i < int(parentNode.ChildCount()); i++ {
		child := parentNode.Child(i)
		if child == nil {
			continue
		}
		if child.Type() != "expression_statement" {
			// 비-expression_statement 노드는 건너뜀 (주석, 데코레이터 등)
			continue
		}
		// 첫 번째 expression_statement를 발견했을 때 exprStmt와 동일한지 확인
		if child.StartPoint().Row == exprStmt.StartPoint().Row &&
			child.StartPoint().Column == exprStmt.StartPoint().Column {
			// 이 expression_statement가 string을 유일한 named child로 갖는지도 재확인
			if int(child.NamedChildCount()) == 1 {
				nc := child.NamedChild(0)
				if nc != nil && nc.Type() == "string" {
					return true
				}
			}
		}
		// 첫 번째 expression_statement가 string이 아니거나 exprStmt와 다르면 false
		return false
	}
	return false
}

// mergeCommentBlocks는 기존 comments와 새 docstrings를 StartLine 오름차순으로 병합한다.
// @intent collectComments 결과와 collectDocstrings 결과를 단일 슬라이스로 합성
func mergeCommentBlocks(comments, docstrings []CommentBlock) []CommentBlock {
	merged := make([]CommentBlock, 0, len(comments)+len(docstrings))
	i, j := 0, 0
	for i < len(comments) && j < len(docstrings) {
		if comments[i].StartLine <= docstrings[j].StartLine {
			merged = append(merged, comments[i])
			i++
		} else {
			merged = append(merged, docstrings[j])
			j++
		}
	}
	merged = append(merged, comments[i:]...)
	merged = append(merged, docstrings[j:]...)
	return merged
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

// rangesOverlap reports whether two inclusive line ranges share at least one line.
// @intent detect whether two symbol captures refer to overlapping source spans
func rangesOverlap(aStart, aEnd, bStart, bEnd int) bool {
	return aStart <= bEnd && bStart <= aEnd
}
