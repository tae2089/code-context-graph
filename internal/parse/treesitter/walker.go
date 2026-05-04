// @index Tree-sitter AST parser based on tags.scm queries.
package treesitter

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"slices"
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
	spec   *LangSpec
	logger *slog.Logger
	parser *sitter.Parser // tests/debug helpers inspect this prototype parser
	query  *sitter.Query  // tags.scm 쿼리 1회 컴파일 후 재사용 (nil이면 쿼리 없음)
	lang   *sitter.Language
	pool   sync.Pool
}

// interfaceInfo captures interface method names for later implementation inference.
// @intent hold the minimum data needed to derive implicit implements edges
type interfaceInfo struct {
	name    string
	methods []string
}

// ParseMetadata carries extra language-specific parse metadata beyond nodes/edges/comments.
// @intent expose package-level enrichment inputs to build/update paths without changing the base parser contract.
type ParseMetadata struct {
	Package    string
	Interfaces []PackageInterfaceInfo
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
		w.lang = lang
		p := sitter.NewParser()
		p.SetLanguage(lang)
		w.parser = p
		w.pool.New = func() any {
			pooled := sitter.NewParser()
			pooled.SetLanguage(lang)
			return pooled
		}

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

// Spec returns the language specification backing this walker.
// @intent expose the configured language rules and query paths for this walker instance
func (w *Walker) Spec() *LangSpec {
	if w == nil {
		return nil
	}
	return w.spec
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
	nodes, edges, comments, _, err := w.ParseWithCommentsAndMetadata(ctx, filePath, content)
	return nodes, edges, comments, err
}

// ParseWithCommentsAndMetadata parses a file and returns package-level enrichment metadata too.
// @intent give build/update paths access to interface method metadata needed for package-wide relationship inference.
func (w *Walker) ParseWithCommentsAndMetadata(ctx context.Context, filePath string, content []byte) ([]model.Node, []model.Edge, []CommentBlock, ParseMetadata, error) {
	if w.parser == nil {
		w.logger.Error("unsupported language", "language", w.spec.Name, "file", filePath)
		return nil, nil, nil, ParseMetadata{}, trace.Wrap(errUnsupportedLanguage, w.spec.Name)
	}

	w.logger.Debug("parsing file", "file", filePath, "language", w.spec.Name)

	tree, err := w.parseSourceCtx(ctx, content)
	if err != nil {
		w.logger.Error("tree-sitter parse error", "file", filePath, "error", err)
		return nil, nil, nil, ParseMetadata{}, trace.Wrap(err, "parse error")
	}
	defer tree.Close()

	root := tree.RootNode()

	var nodes []model.Node
	var edges []model.Edge
	var comments []CommentBlock
	var interfaces []interfaceInfo
	var pkgName string

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

	if w.query != nil {
		nodes, edges, pkgName, interfaces, err = w.executeQueries(root, content, filePath, importPackagesFromContext(ctx), nodes, edges)
		if err != nil {
			return nil, nil, nil, ParseMetadata{}, err
		}
		semantics := semanticsOrDefault(w.spec)
		edges = append(edges, semantics.AdditionalEdges(SemanticContext{
			Root:           root,
			Content:        content,
			FilePath:       filePath,
			Package:        pkgName,
			ImportPackages: importPackagesFromContext(ctx),
			Nodes:          nodes,
			Interfaces:     interfaces,
		})...)
	}

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

	w.resolveTestedBy(nodes, &edges, filePath)
	w.collectComments(root, content, &comments)

	if extra := additionalCommentsOrDefault(semanticsOrDefault(w.spec), CommentContext{
		Root:     root,
		Content:  content,
		FilePath: filePath,
		Nodes:    nodes,
	}); len(extra) > 0 {
		comments = mergeCommentBlocks(comments, extra)
	}

	w.logger.Debug("parse completed", "file", filePath, "nodes", len(nodes), "edges", len(edges))

	return nodes, edges, comments, ParseMetadata{Package: pkgName, Interfaces: exportPackageInterfaces(interfaces)}, nil
}

func exportPackageInterfaces(interfaces []interfaceInfo) []PackageInterfaceInfo {
	if len(interfaces) == 0 {
		return nil
	}
	out := make([]PackageInterfaceInfo, 0, len(interfaces))
	for _, iface := range interfaces {
		out = append(out, PackageInterfaceInfo{Name: iface.name, Methods: append([]string(nil), iface.methods...)})
	}
	return out
}

// executeQueries runs the compiled tags query and converts captures into nodes and edges.
// @intent map Tree-sitter query captures into normalized graph entities for one file
// @mutates nodes and edges slices through appended parse results
// @requires w.query is compiled for w.spec.Name
// @return pkgName is the detected package/module name when the grammar exposes one
func (w *Walker) executeQueries(root *sitter.Node, content []byte, filePath string, importPackages map[string]string, nodes []model.Node, edges []model.Edge) ([]model.Node, []model.Edge, string, []interfaceInfo, error) {
	// w.query는 NewWalker에서 이미 컴파일됨 (불변이므로 공유 안전)
	// QueryCursor는 스레드 안전하지 않아 매번 새로 생성한다.
	qc := sitter.NewQueryCursor()
	defer qc.Close()
	qc.Exec(w.query, root)

	var pkgName string
	var interfaces []interfaceInfo

	// nodeKey → index in nodes slice for O(1) dedup lookup
	// @intent key duplicate symbol matches by name and source span during one query execution.
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
	semantics := semanticsOrDefault(w.spec)
	callRewriter := callRewriterOrDefault(semantics, SemanticContext{
		Root:           root,
		Content:        content,
		FilePath:       filePath,
		ImportPackages: importPackages,
	})

	edgeSeen := make(map[string]struct{})
	interfaceSeen := make(map[string]struct{})

	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}

		m = qc.FilterPredicates(m, content)

		var defNode, nameNode, receiverNode, importNode, callNode, callRefNode, packageNode, implementsNode *sitter.Node
		var defType string

		for _, c := range m.Captures {
			capName := w.query.CaptureNameForId(c.Index)
			if after, ok := strings.CutPrefix(capName, "definition."); ok {
				defNode = c.Node
				defType = after
			} else if subType, ok := strings.CutPrefix(capName, "name."); ok {
				switch subType {
				case "receiver":
					receiverNode = c.Node
				case "package":
					packageNode = c.Node
				case "import":
					importNode = c.Node
				case "call":
					callNode = c.Node
				default:
					nameNode = c.Node
				}
			} else if capName == "reference.call" {
				callRefNode = c.Node
			} else if capName == "reference.import" {
				if importNode == nil {
					importNode = c.Node
				}
			} else if capName == "reference.implements" {
				implementsNode = c.Node
			}
		}
		if callRefNode != nil {
			callNode = callRefNode
		}

		if packageNode != nil {
			pkgName = packageNode.Content(content)
		}

		if defNode != nil && nameNode != nil {
			name := nameNode.Content(content)
			name = definitionNameOrDefault(semantics, DefinitionContext{
				Definition:     defNode,
				DefinitionType: defType,
				Name:           name,
				Root:           root,
				Package:        pkgName,
				Content:        content,
				FilePath:       filePath,
			})
			var receiver string
			if receiverNode != nil {
				receiver = w.extractReceiverStr(receiverNode, content)
			}

			qName := w.buildQualifiedName(pkgName, receiver, name)
			startLine := int(defNode.StartPoint().Row) + 1
			endLine := int(defNode.EndPoint().Row) + 1

			kind := w.mapDefTypeToNodeKind(defType, name)
			implementedTypes := implementedTypesOrDefault(semantics, DefinitionContext{
				Definition:       defNode,
				DefinitionType:   defType,
				Name:             name,
				QualifiedName:    qName,
				Root:             root,
				Package:          pkgName,
				ImplementedTypes: contentForImplementedTypes(content, implementsNode),
				Content:          content,
				FilePath:         filePath,
			})

			acceptedQName := qName
			shouldEnrich := false

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
				acceptedQName = nodes[idx].QualifiedName
				shouldEnrich = true
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
					nodes[idx].Kind = kind
					nodeIndex[nodeKey{name, startLine, endLine}] = idx
					acceptedQName = nodes[idx].QualifiedName
					shouldEnrich = true
				} else {
					acceptedQName = nodes[idx].QualifiedName
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
				shouldEnrich = true
			}

			if shouldEnrich {
				result := definitionResultOrDefault(semantics, DefinitionContext{
					Definition:       defNode,
					DefinitionType:   defType,
					Name:             name,
					QualifiedName:    acceptedQName,
					Root:             root,
					Package:          pkgName,
					ImplementedTypes: slices.Clone(implementedTypes),
					Content:          content,
					FilePath:         filePath,
				})
				interfaces = appendUniqueInterfaces(interfaces, interfaceSeen, result.Interfaces...)
				edges = appendUniqueEdges(edges, edgeSeen, result.Edges...)
				if len(implementedTypes) == 0 && implementsNode != nil {
					implementedTypes = contentForImplementedTypes(content, implementsNode)
				}
				for _, traitName := range implementedTypes {
					edges = appendUniqueEdges(edges, edgeSeen, model.Edge{
						Kind:        model.EdgeKindImplements,
						FilePath:    filePath,
						Line:        startLine,
						Fingerprint: fmt.Sprintf("implements:%s:%s:%s", filePath, acceptedQName, traitName),
					})
				}
			}
		}

		if callNode != nil {
			callee := w.extractCallName(callNode, content)
			if callee != "" {
				line := int(callNode.StartPoint().Row) + 1
				callee = callRewriter.RewriteCall(CallRewriteContext{
					Root:     root,
					Node:     callNode,
					Content:  content,
					FilePath: filePath,
					Callee:   callee,
					Line:     line,
				})
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

func contentForImplementedTypes(content []byte, implementsNode *sitter.Node) []string {
	if implementsNode == nil {
		return nil
	}
	value := strings.TrimSpace(implementsNode.Content(content))
	if value == "" {
		return nil
	}
	return splitTopLevelCSV(value)
}

func appendUniqueEdges(edges []model.Edge, seen map[string]struct{}, add ...model.Edge) []model.Edge {
	for _, edge := range add {
		if edge.Fingerprint == "" {
			edges = append(edges, edge)
			continue
		}
		if _, ok := seen[edge.Fingerprint]; ok {
			continue
		}
		seen[edge.Fingerprint] = struct{}{}
		edges = append(edges, edge)
	}
	return edges
}

func appendUniqueInterfaces(interfaces []interfaceInfo, seen map[string]struct{}, add ...interfaceInfo) []interfaceInfo {
	for _, iface := range add {
		key := iface.name + ":" + strings.Join(iface.methods, ",")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		interfaces = append(interfaces, iface)
	}
	return interfaces
}

func splitTopLevelCSV(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var parts []string
	start := 0
	depthAngle := 0
	depthParen := 0
	depthBracket := 0
	for i, r := range value {
		switch r {
		case '<':
			depthAngle++
		case '>':
			if depthAngle > 0 {
				depthAngle--
			}
		case '(':
			depthParen++
		case ')':
			if depthParen > 0 {
				depthParen--
			}
		case '[':
			depthBracket++
		case ']':
			if depthBracket > 0 {
				depthBracket--
			}
		case ',':
			if depthAngle == 0 && depthParen == 0 && depthBracket == 0 {
				part := strings.TrimSpace(value[start:i])
				if part != "" {
					parts = append(parts, part)
				}
				start = i + 1
			}
		}
	}
	if part := strings.TrimSpace(value[start:]); part != "" {
		parts = append(parts, part)
	}
	return appendUniquePackageFile(nil, parts...)
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
func (w *Walker) resolveTestedBy(nodes []model.Node, edges *[]model.Edge, filePath string) {
	if w.spec.TestPrefix == "" {
		return
	}

	testNodes := make(map[string]model.Node)

	for _, n := range nodes {
		if n.Kind == model.NodeKindTest {
			testNodes[n.QualifiedName] = n
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

		for testQName, testNode := range testNodes {
			if e.Line >= testNode.StartLine && e.Line <= testNode.EndLine {
				*edges = append(*edges, model.Edge{
					Kind:        model.EdgeKindTestedBy,
					FilePath:    filePath,
					Line:        e.Line,
					Fingerprint: fmt.Sprintf("tested_by:%s:%s:%s", filePath, callee, testQName),
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

	tree, err := w.parseSourceCtx(ctx, content)
	if err != nil {
		return nil, trace.Wrap(err, "parse error")
	}
	defer tree.Close()

	var comments []CommentBlock
	w.collectComments(tree.RootNode(), content, &comments)
	return comments, nil
}

// parseSourceCtx parses one source buffer with cancellation support, returning the resulting tree.
// @intent let long parses honor caller cancellation while reusing pooled parsers for throughput.
func (w *Walker) parseSourceCtx(ctx context.Context, content []byte) (*sitter.Tree, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	parser := w.acquireParser()
	defer w.releaseParser(parser)
	input := sitter.Input{
		Encoding: sitter.InputEncodingUTF8,
		Read: func(offset uint32, position sitter.Point) []byte {
			_ = position
			if err := ctx.Err(); err != nil {
				return nil
			}
			if offset >= uint32(len(content)) {
				return nil
			}
			return content[offset:]
		},
	}

	tree, err := parser.ParseInputCtx(ctx, nil, input)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		if tree != nil {
			tree.Close()
		}
		return nil, err
	}
	return tree, nil
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

// acquireParser borrows a Tree-sitter parser from the per-walker pool, creating one on first use.
// @intent amortize parser construction cost across many parses on the same language.
func (w *Walker) acquireParser() *sitter.Parser {
	if w.lang == nil {
		return nil
	}
	if p, ok := w.pool.Get().(*sitter.Parser); ok && p != nil {
		return p
	}
	p := sitter.NewParser()
	p.SetLanguage(w.lang)
	return p
}

// releaseParser returns a parser instance to the pool for reuse.
// @intent keep allocated parsers alive between parses instead of letting them be garbage collected.
func (w *Walker) releaseParser(p *sitter.Parser) {
	if p == nil || w.lang == nil {
		return
	}
	w.pool.Put(p)
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
// @intent detect overlapping source spans when deduplicating competing Tree-sitter captures.
func rangesOverlap(aStart, aEnd, bStart, bEnd int) bool {
	return aStart <= bEnd && bStart <= aEnd
}
