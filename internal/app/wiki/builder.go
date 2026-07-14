// @index Built-in CCG Wiki eager/lazy tree construction and compatibility snapshot policy.
package wiki

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tae2089/trace"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
	"github.com/tae2089/code-context-graph/internal/pathspec"
)

// Builder writes the wiki-index.json compatibility snapshot for ccg-server's browser UI.
// @intent derive a package/file/symbol presentation tree directly from graph nodes.
type Builder struct {
	Repository  Repository
	IndexWriter IndexWriter
	OutDir      string
	Namespace   string
	ProjectDesc string
	Exclude     []string
}

// Build creates the wiki-index.json compatibility snapshot and returns package and file counts.
// @intent generate a UI-oriented tree independent of community detection and PageIndex retrieval.
// @sideEffect reads graph tables and writes wiki-index.json.
func (b *Builder) Build(ctx context.Context) (int, int, error) {
	ns := b.namespace(ctx)
	ctx = requestctx.WithNamespace(ctx, ns)
	root, packages, files, err := b.BuildTree(ctx)
	if err != nil {
		return 0, 0, err
	}
	idx := &Index{Version: 1, BuiltAt: time.Now().UTC(), Root: root}
	if b.IndexWriter == nil {
		return 0, 0, trace.New("wiki index writer not configured")
	}
	if err := b.IndexWriter.WriteWikiIndex(ctx, ns, idx); err != nil {
		return 0, 0, err
	}
	return packages, files, nil
}

// BuildTree creates the browser-facing package/file/symbol Wiki tree without writing wiki-index.json.
// @intent let runtime callers synthesize the same Wiki tree directly from DB rows when the JSON index has not been generated.
// @ensures successful calls return deterministic folder/package/file/symbol ordering matching Build output.
func (b *Builder) BuildTree(ctx context.Context) (*TreeNode, int, int, error) {
	ns := b.Namespace
	if ns == "" {
		ns = requestctx.FromContext(ctx)
	}
	if ns == "" {
		ns = requestctx.DefaultNamespace
	}
	ctx = requestctx.WithNamespace(ctx, ns)
	if b.Repository == nil {
		return nil, 0, 0, trace.New("wiki repository not configured")
	}

	nodes, annByID, err := b.loadNodes(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	root := &TreeNode{ID: "root", Label: "Root", Kind: "root", Summary: b.ProjectDesc, Children: []*TreeNode{}}
	state := &treeState{
		root:     root,
		folders:  map[string]*TreeNode{},
		packages: map[string]*TreeNode{},
		files:    map[string]*TreeNode{},
	}

	for _, node := range nodes {
		if node.Kind == graph.NodeKindPackage {
			state.ensurePackage(node, summaryForNode(annByID[node.ID]), SearchTextForAnnotation(annByID[node.ID]))
		}
	}
	for _, node := range nodes {
		if node.Kind == graph.NodeKindFile {
			state.ensureFile(node, b.docPath(node.FilePath), summaryForNode(annByID[node.ID]), SearchTextForAnnotation(annByID[node.ID]))
		}
	}
	for _, node := range nodes {
		if isSymbolKind(node.Kind) {
			fileNode := state.ensureFilePath(node.FilePath, b.docPath(node.FilePath))
			fileNode.Children = append(fileNode.Children, &TreeNode{
				ID:         fmt.Sprintf("symbol:%s", node.QualifiedName),
				Label:      node.Name,
				Kind:       string(node.Kind),
				Summary:    summaryForNode(annByID[node.ID]),
				SearchText: SearchTextForAnnotation(annByID[node.ID]),
				Details:    detailsForNode(node, annByID[node.ID]),
				Children:   []*TreeNode{},
			})
		}
	}

	sortTree(root)
	return root, len(state.packages), len(state.files), nil
}

// BuildSubtree creates one Wiki tree node plus a bounded set of descendants directly from DB rows.
// @intent support GitHub-style lazy Wiki navigation without synthesizing the full tree for every folder expansion.
// @ensures depth > 0 limits descendants relative to the selected node; depth <= 0 preserves full-tree compatibility.
func (b *Builder) BuildSubtree(ctx context.Context, nodeID string, depth int) (*TreeNode, error) {
	ctx = requestctx.WithNamespace(ctx, b.namespace(ctx))
	if depth <= 0 {
		root, _, _, err := b.BuildTree(ctx)
		if err != nil {
			return nil, err
		}
		if nodeID == "" {
			return root, nil
		}
		node := FindNode(root, nodeID)
		if node == nil {
			return nil, fmt.Errorf("%w: node_id %q", os.ErrNotExist, nodeID)
		}
		return node, nil
	}
	node, err := b.lazyBaseNode(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	if err := b.populateLazyChildren(ctx, node, 0, depth); err != nil {
		return nil, err
	}
	return node, nil
}

// @intent build the selected lazy tree root without loading unrelated descendants.
func (b *Builder) lazyBaseNode(ctx context.Context, nodeID string) (*TreeNode, error) {
	switch {
	case nodeID == "":
		return &TreeNode{ID: "root", Label: "Root", Kind: "root", Summary: b.ProjectDesc, HasChildren: true, Children: []*TreeNode{}}, nil
	case strings.HasPrefix(nodeID, "folder:"):
		folderPath := strings.Trim(strings.TrimPrefix(nodeID, "folder:"), "/")
		if !b.hasPathDescendant(ctx, folderPath) {
			return nil, fmt.Errorf("%w: node_id %q", os.ErrNotExist, nodeID)
		}
		return &TreeNode{ID: nodeID, Label: path.Base(folderPath), Kind: "folder", HasChildren: true, Children: []*TreeNode{}}, nil
	case strings.HasPrefix(nodeID, "package:"):
		return b.lazyStoredNode(ctx, nodeID, graph.NodeKindPackage, strings.TrimPrefix(nodeID, "package:"))
	case strings.HasPrefix(nodeID, "file:"):
		return b.lazyStoredNode(ctx, nodeID, graph.NodeKindFile, strings.TrimPrefix(nodeID, "file:"))
	case strings.HasPrefix(nodeID, "symbol:"):
		return b.lazySymbolNode(ctx, strings.TrimPrefix(nodeID, "symbol:"))
	default:
		return nil, fmt.Errorf("%w: node_id %q", os.ErrNotExist, nodeID)
	}
}

// @intent load a stored package or file tree node with annotation summary and expandable state.
func (b *Builder) lazyStoredNode(ctx context.Context, nodeID string, kind graph.NodeKind, filePath string) (*TreeNode, error) {
	node, err := b.Repository.StoredNode(ctx, kind, filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if kind == graph.NodeKindFile && b.hasSymbol(ctx, filePath) {
				return &TreeNode{
					ID:          "file:" + strings.Trim(path.Clean(filePath), "/"),
					Label:       path.Base(filePath),
					Kind:        string(graph.NodeKindFile),
					DocPath:     b.docPath(filePath),
					HasChildren: true,
					Children:    []*TreeNode{},
				}, nil
			}
			return nil, fmt.Errorf("%w: node_id %q", os.ErrNotExist, nodeID)
		}
		return nil, trace.Wrap(err, "load wiki lazy node")
	}
	annByID, err := b.loadAnnotations(ctx, []uint{node.ID})
	if err != nil {
		return nil, err
	}
	return b.treeNodeForModel(ctx, *node, annByID[node.ID]), nil
}

// @intent load a stored symbol tree node by qualified name for direct lazy navigation.
func (b *Builder) lazySymbolNode(ctx context.Context, qualifiedName string) (*TreeNode, error) {
	node, err := b.Repository.SymbolNode(ctx, qualifiedName, symbolKinds())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: node_id %q", os.ErrNotExist, "symbol:"+qualifiedName)
		}
		return nil, trace.Wrap(err, "load wiki lazy symbol")
	}
	annByID, err := b.loadAnnotations(ctx, []uint{node.ID})
	if err != nil {
		return nil, err
	}
	return b.treeNodeForModel(ctx, *node, annByID[node.ID]), nil
}

// @intent populate a lazy tree node to the requested relative depth.
func (b *Builder) populateLazyChildren(ctx context.Context, node *TreeNode, currentDepth, maxDepth int) error {
	if currentDepth >= maxDepth {
		node.Children = []*TreeNode{}
		return nil
	}
	children, err := b.lazyChildren(ctx, node)
	if err != nil {
		return err
	}
	node.Children = children
	node.HasChildren = len(children) > 0 || node.HasChildren
	for _, child := range node.Children {
		if err := b.populateLazyChildren(ctx, child, currentDepth+1, maxDepth); err != nil {
			return err
		}
	}
	return nil
}

// @intent resolve immediate children for one lazy Wiki tree node.
func (b *Builder) lazyChildren(ctx context.Context, node *TreeNode) ([]*TreeNode, error) {
	switch {
	case node.ID == "root":
		return b.folderChildren(ctx, "")
	case strings.HasPrefix(node.ID, "folder:"):
		return b.folderChildren(ctx, strings.TrimPrefix(node.ID, "folder:"))
	case strings.HasPrefix(node.ID, "package:"):
		return b.packageChildren(ctx, strings.TrimPrefix(node.ID, "package:"))
	case strings.HasPrefix(node.ID, "file:"):
		return b.fileChildren(ctx, strings.TrimPrefix(node.ID, "file:"))
	default:
		return []*TreeNode{}, nil
	}
}

// @intent list immediate folder children while allowing package nodes to replace same-path synthetic folders.
func (b *Builder) folderChildren(ctx context.Context, folderPath string) ([]*TreeNode, error) {
	nodes, err := b.loadPathNodes(ctx, folderPath, lazyPathNodeKinds())
	if err != nil {
		return nil, err
	}
	entries := map[string]lazyEntry{}
	for _, node := range nodes {
		if node.Kind == graph.NodeKindPackage && isRootPackagePath(node.FilePath) {
			continue
		}
		childPath, immediate, ok := immediateChildPath(folderPath, node.FilePath)
		if !ok {
			continue
		}
		if node.Kind == graph.NodeKindPackage && immediate {
			n := node
			entries[childPath] = lazyEntry{kind: string(graph.NodeKindPackage), path: childPath, node: &n}
			continue
		}
		if node.Kind == graph.NodeKindFile && immediate {
			if existing := entries[childPath]; existing.kind == string(graph.NodeKindPackage) {
				continue
			}
			n := node
			entries[childPath] = lazyEntry{kind: string(graph.NodeKindFile), path: childPath, node: &n}
			continue
		}
		if isSymbolKind(node.Kind) && immediate {
			if _, ok := entries[childPath]; !ok {
				entries[childPath] = lazyEntry{kind: string(graph.NodeKindFile), path: childPath}
			}
			continue
		}
		if existing := entries[childPath]; existing.kind != string(graph.NodeKindPackage) {
			entries[childPath] = lazyEntry{kind: "folder", path: childPath}
		}
	}
	return b.materializeLazyEntries(ctx, entries)
}

// @intent list direct files inside one package node.
func (b *Builder) packageChildren(ctx context.Context, packagePath string) ([]*TreeNode, error) {
	nodes, err := b.loadPathNodes(ctx, packagePath, append([]string{string(graph.NodeKindFile)}, symbolKindStrings()...))
	if err != nil {
		return nil, err
	}
	entries := map[string]lazyEntry{}
	for _, node := range nodes {
		childPath, immediate, ok := immediateChildPath(packagePath, node.FilePath)
		if !ok || !immediate {
			continue
		}
		if node.Kind == graph.NodeKindFile {
			n := node
			entries[childPath] = lazyEntry{kind: string(graph.NodeKindFile), path: childPath, node: &n}
			continue
		}
		if _, ok := entries[childPath]; !ok {
			entries[childPath] = lazyEntry{kind: string(graph.NodeKindFile), path: childPath}
		}
	}
	return b.materializeLazyEntries(ctx, entries)
}

// @intent list symbols declared inside one file node.
func (b *Builder) fileChildren(ctx context.Context, filePath string) ([]*TreeNode, error) {
	nodes, err := b.Repository.FileSymbols(ctx, filePath, symbolKinds())
	if err != nil {
		return nil, trace.Wrap(err, "load wiki file children")
	}
	ids := nodeIDs(nodes)
	annByID, err := b.loadAnnotations(ctx, ids)
	if err != nil {
		return nil, err
	}
	children := make([]*TreeNode, 0, len(nodes))
	for _, node := range nodes {
		children = append(children, b.treeNodeForModel(ctx, node, annByID[node.ID]))
	}
	sortTree(&TreeNode{Children: children})
	return children, nil
}

// @intent query package/file path candidates under a folder prefix without loading annotations.
func (b *Builder) loadPathNodes(ctx context.Context, folderPath string, kinds []string) ([]graph.Node, error) {
	nodes, err := b.Repository.PathNodes(ctx, folderPath, nodeKinds(kinds))
	if err != nil {
		return nil, trace.Wrap(err, "load wiki path nodes")
	}
	if len(b.Exclude) == 0 {
		return nodes, nil
	}
	filtered := nodes[:0]
	for _, node := range nodes {
		if !pathspec.MatchExcludes(b.Exclude, node.FilePath) {
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}

// @intent convert collected lazy child entries into annotated TreeNode DTOs.
func (b *Builder) materializeLazyEntries(ctx context.Context, entries map[string]lazyEntry) ([]*TreeNode, error) {
	ids := make([]uint, 0, len(entries))
	for _, entry := range entries {
		if entry.node != nil {
			ids = append(ids, entry.node.ID)
		}
	}
	annByID, err := b.loadAnnotations(ctx, ids)
	if err != nil {
		return nil, err
	}
	children := make([]*TreeNode, 0, len(entries))
	for _, entry := range entries {
		if entry.node != nil {
			children = append(children, b.treeNodeForModel(ctx, *entry.node, annByID[entry.node.ID]))
			continue
		}
		if entry.kind == string(graph.NodeKindFile) {
			children = append(children, &TreeNode{
				ID:          "file:" + entry.path,
				Label:       path.Base(entry.path),
				Kind:        string(graph.NodeKindFile),
				DocPath:     b.docPath(entry.path),
				HasChildren: b.hasSymbol(ctx, entry.path),
				Children:    []*TreeNode{},
			})
			continue
		}
		children = append(children, &TreeNode{
			ID:          "folder:" + entry.path,
			Label:       path.Base(entry.path),
			Kind:        "folder",
			HasChildren: true,
			Children:    []*TreeNode{},
		})
	}
	sortTree(&TreeNode{Children: children})
	return children, nil
}

// @intent convert one graph node into the Wiki tree node shape used by full and lazy builders.
func (b *Builder) treeNodeForModel(ctx context.Context, node graph.Node, annotation *graph.Annotation) *TreeNode {
	treeNode := &TreeNode{
		Summary:    summaryForNode(annotation),
		SearchText: SearchTextForAnnotation(annotation),
		Children:   []*TreeNode{},
	}
	switch node.Kind {
	case graph.NodeKindPackage:
		treeNode.ID = "package:" + strings.Trim(path.Clean(node.FilePath), "/")
		treeNode.Label = path.Base(node.FilePath)
		treeNode.Kind = string(graph.NodeKindPackage)
		treeNode.HasChildren = b.hasDirectFile(ctx, node.FilePath)
	case graph.NodeKindFile:
		treeNode.ID = "file:" + strings.Trim(path.Clean(node.FilePath), "/")
		treeNode.Label = path.Base(node.FilePath)
		treeNode.Kind = string(graph.NodeKindFile)
		treeNode.DocPath = b.docPath(node.FilePath)
		treeNode.HasChildren = b.hasSymbol(ctx, node.FilePath)
	default:
		treeNode.ID = "symbol:" + node.QualifiedName
		treeNode.Label = node.Name
		treeNode.Kind = string(node.Kind)
		treeNode.Details = detailsForNode(node, annotation)
	}
	return treeNode
}

// @intent batch-load annotations for lazy tree nodes while preserving tag order.
func (b *Builder) loadAnnotations(ctx context.Context, ids []uint) (map[uint]*graph.Annotation, error) {
	annByID := map[uint]*graph.Annotation{}
	if len(ids) == 0 {
		return annByID, nil
	}
	annotations, err := b.Repository.Annotations(ctx, ids)
	if err != nil {
		return nil, trace.Wrap(err, "load wiki annotations")
	}
	for id, annotation := range annotations {
		annByID[id] = annotation
	}
	return annByID, nil
}

// @intent test whether a folder or root path has any descendant package or file node.
func (b *Builder) hasPathDescendant(ctx context.Context, folderPath string) bool {
	nodes, err := b.loadPathNodes(ctx, folderPath, []string{string(graph.NodeKindPackage), string(graph.NodeKindFile)})
	return err == nil && len(nodes) > 0
}

// @intent test whether a package node has direct file children.
func (b *Builder) hasDirectFile(ctx context.Context, packagePath string) bool {
	nodes, err := b.loadPathNodes(ctx, packagePath, []string{string(graph.NodeKindFile)})
	if err != nil {
		return false
	}
	for _, node := range nodes {
		_, immediate, ok := immediateChildPath(packagePath, node.FilePath)
		if ok && immediate {
			return true
		}
	}
	return false
}

// @intent test whether a file node has symbol children.
func (b *Builder) hasSymbol(ctx context.Context, filePath string) bool {
	found, err := b.Repository.HasSymbol(ctx, filePath, symbolKinds())
	return err == nil && found
}

// @intent resolve the namespace used for both DB reads and wiki-index output paths.
func (b *Builder) namespace(ctx context.Context) string {
	ns := b.Namespace
	if ns == "" {
		ns = requestctx.FromContext(ctx)
	}
	if ns == "" {
		ns = requestctx.DefaultNamespace
	}
	return ns
}

// @intent load the graph node set needed for Wiki navigation and summaries.
func (b *Builder) loadNodes(ctx context.Context) ([]graph.Node, map[uint]*graph.Annotation, error) {
	kinds := []graph.NodeKind{
		graph.NodeKindPackage,
		graph.NodeKindFile,
		graph.NodeKindFunction,
		graph.NodeKindClass,
		graph.NodeKindType,
		graph.NodeKindTest,
	}
	nodes, err := b.Repository.NavigationNodes(ctx, kinds)
	if err != nil {
		return nil, nil, trace.Wrap(err, "load wiki nodes")
	}
	if len(b.Exclude) > 0 {
		filtered := nodes[:0]
		for _, node := range nodes {
			if !pathspec.MatchExcludes(b.Exclude, node.FilePath) {
				filtered = append(filtered, node)
			}
		}
		nodes = filtered
	}

	ids := make([]uint, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	annByID, err := b.Repository.Annotations(ctx, ids)
	if err != nil {
		return nil, nil, trace.Wrap(err, "load wiki annotations")
	}
	return nodes, annByID, nil
}

// @intent convert a repository-relative source path to the generated Markdown doc path.
func (b *Builder) docPath(filePath string) string {
	outDir := b.OutDir
	if outDir == "" {
		outDir = "docs"
	}
	rel := filePath
	if filepath.IsAbs(filePath) {
		rel = strings.TrimPrefix(filePath, string(filepath.Separator))
	}
	return filepath.Join(outDir, rel+".md")
}

// @intent hold mutable lookup maps while building the folder/package/file Wiki tree.
type treeState struct {
	root     *TreeNode
	folders  map[string]*TreeNode
	packages map[string]*TreeNode
	files    map[string]*TreeNode
}

// @intent create folder nodes for path segments that are not themselves packages.
func (s *treeState) ensureFolder(folderPath string) *TreeNode {
	folderPath = strings.Trim(path.Clean(folderPath), "/")
	if folderPath == "." || folderPath == "" {
		return s.root
	}
	if node := s.folders[folderPath]; node != nil {
		return node
	}
	parent := s.ensureFolder(path.Dir(folderPath))
	node := &TreeNode{
		ID:       "folder:" + folderPath,
		Label:    path.Base(folderPath),
		Kind:     "folder",
		Children: []*TreeNode{},
	}
	parent.Children = append(parent.Children, node)
	s.folders[folderPath] = node
	return node
}

// @intent ensure a package node exists under its containing folder.
func (s *treeState) ensurePackage(node graph.Node, summary, searchText string) *TreeNode {
	pkgPath := strings.Trim(path.Clean(node.FilePath), "/")
	if pkgPath == "." || pkgPath == "" {
		return s.root
	}
	if existing := s.packages[pkgPath]; existing != nil {
		if existing.Summary == "" {
			existing.Summary = summary
		}
		if existing.SearchText == "" {
			existing.SearchText = searchText
		}
		return existing
	}
	parent := s.ensureFolder(path.Dir(pkgPath))
	pkg := &TreeNode{
		ID:         "package:" + pkgPath,
		Label:      path.Base(pkgPath),
		Kind:       "package",
		Summary:    summary,
		SearchText: searchText,
		Children:   []*TreeNode{},
	}
	parent.Children = append(parent.Children, pkg)
	s.packages[pkgPath] = pkg
	return pkg
}

// @intent create a file node under its package when available, otherwise under its directory folder.
func (s *treeState) ensureFile(node graph.Node, docPath, summary, searchText string) *TreeNode {
	return s.ensureFileWithSummary(node.FilePath, docPath, summary, searchText)
}

// @intent ensure symbol-only files still appear in the Wiki tree even when no file node was parsed.
func (s *treeState) ensureFilePath(filePath, docPath string) *TreeNode {
	return s.ensureFileWithSummary(filePath, docPath, "", "")
}

// @intent deduplicate file tree nodes while preserving the first useful summary and doc path.
func (s *treeState) ensureFileWithSummary(filePath, docPath, summary, searchText string) *TreeNode {
	filePath = strings.Trim(path.Clean(filePath), "/")
	if existing := s.files[filePath]; existing != nil {
		if existing.Summary == "" {
			existing.Summary = summary
		}
		if existing.SearchText == "" {
			existing.SearchText = searchText
		}
		if existing.DocPath == "" {
			existing.DocPath = docPath
		}
		return existing
	}
	dir := path.Dir(filePath)
	parent := s.packages[dir]
	if parent == nil {
		parent = s.ensureFolder(dir)
	}
	file := &TreeNode{
		ID:         "file:" + filePath,
		Label:      path.Base(filePath),
		Kind:       "file",
		Summary:    summary,
		DocPath:    docPath,
		SearchText: searchText,
		Children:   []*TreeNode{},
	}
	parent.Children = append(parent.Children, file)
	s.files[filePath] = file
	return file
}

// @intent hold one immediate child candidate while lazy tree nodes are materialized.
type lazyEntry struct {
	kind string
	path string
	node *graph.Node
}

// @intent compute the immediate child path under a folder prefix.
func immediateChildPath(parentPath, filePath string) (string, bool, bool) {
	parentPath = strings.Trim(path.Clean(parentPath), "/")
	if parentPath == "." {
		parentPath = ""
	}
	filePath = strings.Trim(path.Clean(filePath), "/")
	rel := filePath
	if parentPath != "" {
		prefix := parentPath + "/"
		if !strings.HasPrefix(filePath, prefix) {
			return "", false, false
		}
		rel = strings.TrimPrefix(filePath, prefix)
	}
	parts := strings.Split(rel, "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", false, false
	}
	childPath := parts[0]
	if parentPath != "" {
		childPath = parentPath + "/" + parts[0]
	}
	return childPath, len(parts) == 1, true
}

// @intent identify package nodes that represent the repository root rather than a sidebar child.
// @domainRule the root package is folded into the Wiki root so top-level files are not duplicated under a synthetic "." package.
func isRootPackagePath(filePath string) bool {
	filePath = strings.Trim(path.Clean(filePath), "/")
	return filePath == "." || filePath == ""
}

// @intent collect graph node IDs for batch annotation lookup.
func nodeIDs(nodes []graph.Node) []uint {
	ids := make([]uint, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	return ids
}

// @intent expose symbol node kinds as strings for GORM IN clauses.
func symbolKindStrings() []string {
	return []string{string(graph.NodeKindFunction), string(graph.NodeKindClass), string(graph.NodeKindType), string(graph.NodeKindTest)}
}

// @intent centralize the symbol kinds eligible for built-in Wiki navigation.
func symbolKinds() []graph.NodeKind {
	return []graph.NodeKind{graph.NodeKindFunction, graph.NodeKindClass, graph.NodeKindType, graph.NodeKindTest}
}

// @intent adapt legacy string kind sets into the repository's typed node-kind request.
func nodeKinds(values []string) []graph.NodeKind {
	kinds := make([]graph.NodeKind, len(values))
	for i := range values {
		kinds[i] = graph.NodeKind(values[i])
	}
	return kinds
}

// @intent expose path-bearing node kinds that can imply folder and file tree entries.
func lazyPathNodeKinds() []string {
	return []string{
		string(graph.NodeKindPackage),
		string(graph.NodeKindFile),
		string(graph.NodeKindFunction),
		string(graph.NodeKindClass),
		string(graph.NodeKindType),
		string(graph.NodeKindTest),
	}
}

// @intent identify graph node kinds that should appear as symbols under a file in the Wiki tree.
func isSymbolKind(kind graph.NodeKind) bool {
	return kind == graph.NodeKindFunction || kind == graph.NodeKindClass || kind == graph.NodeKindType || kind == graph.NodeKindTest
}

// @intent choose the summary text that makes a Wiki node useful for scanning and search.
func summaryForNode(annotation *graph.Annotation) string {
	if annotation == nil {
		return ""
	}
	for _, tag := range annotation.Tags {
		if tag.Kind == graph.TagIndex {
			return tag.Value
		}
	}
	for _, tag := range annotation.Tags {
		if tag.Kind == graph.TagIntent {
			return tag.Value
		}
	}
	return strings.TrimSpace(annotation.Summary)
}

// @intent expose full structured annotation metadata for Wiki symbol detail views.
func detailsForNode(node graph.Node, annotation *graph.Annotation) *NodeDetails {
	detail := &NodeDetails{
		QualifiedName: node.QualifiedName,
		FilePath:      node.FilePath,
		StartLine:     node.StartLine,
		EndLine:       node.EndLine,
		Language:      node.Language,
	}
	if annotation == nil {
		return detail
	}
	tags := make([]DocTagDetail, 0, len(annotation.Tags))
	for _, tag := range annotation.Tags {
		tags = append(tags, DocTagDetailFromModel(tag))
	}
	detail.Annotation = &AnnotationDetail{
		Summary: strings.TrimSpace(annotation.Summary),
		Context: strings.TrimSpace(annotation.Context),
		Tags:    tags,
	}
	return detail
}

// @intent keep Wiki tree output deterministic across builds.
func sortTree(node *TreeNode) {
	sort.SliceStable(node.Children, func(i, j int) bool {
		if node.Children[i].Kind != node.Children[j].Kind {
			return kindRank(node.Children[i].Kind) < kindRank(node.Children[j].Kind)
		}
		return node.Children[i].Label < node.Children[j].Label
	})
	for _, child := range node.Children {
		sortTree(child)
	}
}

// @intent sort folders before packages, packages before files, and files before symbols.
func kindRank(kind string) int {
	switch kind {
	case "folder":
		return 0
	case "package":
		return 1
	case "file":
		return 2
	default:
		return 3
	}
}
