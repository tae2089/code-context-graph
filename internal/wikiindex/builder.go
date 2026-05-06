// @index wikiindex builds the browser-facing docs tree without depending on community postprocessing.
package wikiindex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/pathutil"
	"github.com/tae2089/code-context-graph/internal/ragindex"
)

// Builder writes the wiki-index.json compatibility snapshot for ccg-server's browser UI.
// @intent derive a package/file/symbol presentation tree directly from graph nodes.
type Builder struct {
	DB          *gorm.DB
	OutDir      string
	IndexDir    string
	Namespace   string
	ProjectDesc string
	Exclude     []string
}

// Build creates the wiki-index.json compatibility snapshot and returns package and file counts.
// @intent generate a UI-oriented tree independent of community detection and PageIndex retrieval.
// @sideEffect reads graph tables and writes wiki-index.json.
func (b *Builder) Build(ctx context.Context) (int, int, error) {
	ns := b.namespace(ctx)
	ctx = ctxns.WithNamespace(ctx, ns)
	root, packages, files, err := b.BuildTree(ctx)
	if err != nil {
		return 0, 0, err
	}
	idx := &ragindex.Index{Version: 1, BuiltAt: time.Now().UTC(), Root: root}
	if err := b.writeIndex(ns, idx); err != nil {
		return 0, 0, err
	}
	return packages, files, nil
}

// BuildTree creates the browser-facing package/file/symbol Wiki tree without writing wiki-index.json.
// @intent let runtime callers synthesize the same Wiki tree directly from DB rows when the JSON index has not been generated.
// @ensures successful calls return deterministic folder/package/file/symbol ordering matching Build output.
func (b *Builder) BuildTree(ctx context.Context) (*ragindex.TreeNode, int, int, error) {
	ns := b.Namespace
	if ns == "" {
		ns = ctxns.FromContext(ctx)
	}
	if ns == "" {
		ns = ctxns.DefaultNamespace
	}
	ctx = ctxns.WithNamespace(ctx, ns)
	if b.DB == nil {
		return nil, 0, 0, trace.New("DB not configured")
	}

	nodes, annByID, err := b.loadNodes(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	root := &ragindex.TreeNode{ID: "root", Label: "Root", Kind: "root", Summary: b.ProjectDesc, Children: []*ragindex.TreeNode{}}
	state := &treeState{
		root:     root,
		folders:  map[string]*ragindex.TreeNode{},
		packages: map[string]*ragindex.TreeNode{},
		files:    map[string]*ragindex.TreeNode{},
	}

	for _, node := range nodes {
		if node.Kind == model.NodeKindPackage {
			state.ensurePackage(node, summaryForNode(annByID[node.ID]), ragindex.SearchTextForAnnotation(annByID[node.ID]))
		}
	}
	for _, node := range nodes {
		if node.Kind == model.NodeKindFile {
			state.ensureFile(node, b.docPath(node.FilePath), summaryForNode(annByID[node.ID]), ragindex.SearchTextForAnnotation(annByID[node.ID]))
		}
	}
	for _, node := range nodes {
		if isSymbolKind(node.Kind) {
			fileNode := state.ensureFilePath(node.FilePath, b.docPath(node.FilePath))
			fileNode.Children = append(fileNode.Children, &ragindex.TreeNode{
				ID:         fmt.Sprintf("symbol:%s", node.QualifiedName),
				Label:      node.Name,
				Kind:       string(node.Kind),
				Summary:    summaryForNode(annByID[node.ID]),
				SearchText: ragindex.SearchTextForAnnotation(annByID[node.ID]),
				Details:    detailsForNode(node, annByID[node.ID]),
				Children:   []*ragindex.TreeNode{},
			})
		}
	}

	sortTree(root)
	return root, len(state.packages), len(state.files), nil
}

// BuildSubtree creates one Wiki tree node plus a bounded set of descendants directly from DB rows.
// @intent support GitHub-style lazy Wiki navigation without synthesizing the full tree for every folder expansion.
// @ensures depth > 0 limits descendants relative to the selected node; depth <= 0 preserves full-tree compatibility.
func (b *Builder) BuildSubtree(ctx context.Context, nodeID string, depth int) (*ragindex.TreeNode, error) {
	ctx = ctxns.WithNamespace(ctx, b.namespace(ctx))
	if depth <= 0 {
		root, _, _, err := b.BuildTree(ctx)
		if err != nil {
			return nil, err
		}
		if nodeID == "" {
			return root, nil
		}
		node := ragindex.FindNode(root, nodeID)
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
func (b *Builder) lazyBaseNode(ctx context.Context, nodeID string) (*ragindex.TreeNode, error) {
	switch {
	case nodeID == "":
		return &ragindex.TreeNode{ID: "root", Label: "Root", Kind: "root", Summary: b.ProjectDesc, HasChildren: true, Children: []*ragindex.TreeNode{}}, nil
	case strings.HasPrefix(nodeID, "folder:"):
		folderPath := strings.Trim(strings.TrimPrefix(nodeID, "folder:"), "/")
		if !b.hasPathDescendant(ctx, folderPath) {
			return nil, fmt.Errorf("%w: node_id %q", os.ErrNotExist, nodeID)
		}
		return &ragindex.TreeNode{ID: nodeID, Label: path.Base(folderPath), Kind: "folder", HasChildren: true, Children: []*ragindex.TreeNode{}}, nil
	case strings.HasPrefix(nodeID, "package:"):
		return b.lazyStoredNode(ctx, nodeID, model.NodeKindPackage, strings.TrimPrefix(nodeID, "package:"))
	case strings.HasPrefix(nodeID, "file:"):
		return b.lazyStoredNode(ctx, nodeID, model.NodeKindFile, strings.TrimPrefix(nodeID, "file:"))
	case strings.HasPrefix(nodeID, "symbol:"):
		return b.lazySymbolNode(ctx, strings.TrimPrefix(nodeID, "symbol:"))
	default:
		return nil, fmt.Errorf("%w: node_id %q", os.ErrNotExist, nodeID)
	}
}

// @intent load a stored package or file tree node with annotation summary and expandable state.
func (b *Builder) lazyStoredNode(ctx context.Context, nodeID string, kind model.NodeKind, filePath string) (*ragindex.TreeNode, error) {
	ns := ctxns.FromContext(ctx)
	var node model.Node
	if err := b.DB.WithContext(ctx).
		Where("namespace = ? AND kind = ? AND file_path = ?", ns, kind, filePath).
		First(&node).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			if kind == model.NodeKindFile && b.hasSymbol(ctx, filePath) {
				return &ragindex.TreeNode{
					ID:          "file:" + strings.Trim(path.Clean(filePath), "/"),
					Label:       path.Base(filePath),
					Kind:        string(model.NodeKindFile),
					DocPath:     b.docPath(filePath),
					HasChildren: true,
					Children:    []*ragindex.TreeNode{},
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
	return b.treeNodeForModel(ctx, node, annByID[node.ID]), nil
}

// @intent load a stored symbol tree node by qualified name for direct lazy navigation.
func (b *Builder) lazySymbolNode(ctx context.Context, qualifiedName string) (*ragindex.TreeNode, error) {
	ns := ctxns.FromContext(ctx)
	var node model.Node
	if err := b.DB.WithContext(ctx).
		Where("namespace = ? AND qualified_name = ? AND kind IN ?", ns, qualifiedName, symbolKindStrings()).
		Order("file_path ASC, start_line ASC, id ASC").
		First(&node).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("%w: node_id %q", os.ErrNotExist, "symbol:"+qualifiedName)
		}
		return nil, trace.Wrap(err, "load wiki lazy symbol")
	}
	annByID, err := b.loadAnnotations(ctx, []uint{node.ID})
	if err != nil {
		return nil, err
	}
	return b.treeNodeForModel(ctx, node, annByID[node.ID]), nil
}

// @intent populate a lazy tree node to the requested relative depth.
func (b *Builder) populateLazyChildren(ctx context.Context, node *ragindex.TreeNode, currentDepth, maxDepth int) error {
	if currentDepth >= maxDepth {
		node.Children = []*ragindex.TreeNode{}
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
func (b *Builder) lazyChildren(ctx context.Context, node *ragindex.TreeNode) ([]*ragindex.TreeNode, error) {
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
		return []*ragindex.TreeNode{}, nil
	}
}

// @intent list immediate folder children while allowing package nodes to replace same-path synthetic folders.
func (b *Builder) folderChildren(ctx context.Context, folderPath string) ([]*ragindex.TreeNode, error) {
	nodes, err := b.loadPathNodes(ctx, folderPath, lazyPathNodeKinds())
	if err != nil {
		return nil, err
	}
	entries := map[string]lazyEntry{}
	for _, node := range nodes {
		childPath, immediate, ok := immediateChildPath(folderPath, node.FilePath)
		if !ok {
			continue
		}
		if node.Kind == model.NodeKindPackage && immediate {
			n := node
			entries[childPath] = lazyEntry{kind: string(model.NodeKindPackage), path: childPath, node: &n}
			continue
		}
		if node.Kind == model.NodeKindFile && immediate {
			if existing := entries[childPath]; existing.kind == string(model.NodeKindPackage) {
				continue
			}
			n := node
			entries[childPath] = lazyEntry{kind: string(model.NodeKindFile), path: childPath, node: &n}
			continue
		}
		if isSymbolKind(node.Kind) && immediate {
			if _, ok := entries[childPath]; !ok {
				entries[childPath] = lazyEntry{kind: string(model.NodeKindFile), path: childPath}
			}
			continue
		}
		if existing := entries[childPath]; existing.kind != string(model.NodeKindPackage) {
			entries[childPath] = lazyEntry{kind: "folder", path: childPath}
		}
	}
	return b.materializeLazyEntries(ctx, entries)
}

// @intent list direct files inside one package node.
func (b *Builder) packageChildren(ctx context.Context, packagePath string) ([]*ragindex.TreeNode, error) {
	nodes, err := b.loadPathNodes(ctx, packagePath, append([]string{string(model.NodeKindFile)}, symbolKindStrings()...))
	if err != nil {
		return nil, err
	}
	entries := map[string]lazyEntry{}
	for _, node := range nodes {
		childPath, immediate, ok := immediateChildPath(packagePath, node.FilePath)
		if !ok || !immediate {
			continue
		}
		if node.Kind == model.NodeKindFile {
			n := node
			entries[childPath] = lazyEntry{kind: string(model.NodeKindFile), path: childPath, node: &n}
			continue
		}
		if _, ok := entries[childPath]; !ok {
			entries[childPath] = lazyEntry{kind: string(model.NodeKindFile), path: childPath}
		}
	}
	return b.materializeLazyEntries(ctx, entries)
}

// @intent list symbols declared inside one file node.
func (b *Builder) fileChildren(ctx context.Context, filePath string) ([]*ragindex.TreeNode, error) {
	ns := ctxns.FromContext(ctx)
	var nodes []model.Node
	if err := b.DB.WithContext(ctx).
		Where("namespace = ? AND file_path = ? AND kind IN ?", ns, filePath, symbolKindStrings()).
		Order("start_line ASC, qualified_name ASC").
		Find(&nodes).Error; err != nil {
		return nil, trace.Wrap(err, "load wiki file children")
	}
	ids := nodeIDs(nodes)
	annByID, err := b.loadAnnotations(ctx, ids)
	if err != nil {
		return nil, err
	}
	children := make([]*ragindex.TreeNode, 0, len(nodes))
	for _, node := range nodes {
		children = append(children, b.treeNodeForModel(ctx, node, annByID[node.ID]))
	}
	sortTree(&ragindex.TreeNode{Children: children})
	return children, nil
}

// @intent query package/file path candidates under a folder prefix without loading annotations.
func (b *Builder) loadPathNodes(ctx context.Context, folderPath string, kinds []string) ([]model.Node, error) {
	ns := ctxns.FromContext(ctx)
	var nodes []model.Node
	q := b.DB.WithContext(ctx).
		Where("namespace = ? AND kind IN ?", ns, kinds).
		Order("file_path ASC, start_line ASC, qualified_name ASC")
	folderPath = strings.Trim(path.Clean(folderPath), "/")
	if folderPath != "." && folderPath != "" {
		q = q.Where("file_path LIKE ?", folderPath+"/%")
	}
	if err := q.Find(&nodes).Error; err != nil {
		return nil, trace.Wrap(err, "load wiki path nodes")
	}
	if len(b.Exclude) == 0 {
		return nodes, nil
	}
	filtered := nodes[:0]
	for _, node := range nodes {
		if !pathutil.MatchExcludes(b.Exclude, node.FilePath) {
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}

// @intent convert collected lazy child entries into annotated TreeNode DTOs.
func (b *Builder) materializeLazyEntries(ctx context.Context, entries map[string]lazyEntry) ([]*ragindex.TreeNode, error) {
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
	children := make([]*ragindex.TreeNode, 0, len(entries))
	for _, entry := range entries {
		if entry.node != nil {
			children = append(children, b.treeNodeForModel(ctx, *entry.node, annByID[entry.node.ID]))
			continue
		}
		if entry.kind == string(model.NodeKindFile) {
			children = append(children, &ragindex.TreeNode{
				ID:          "file:" + entry.path,
				Label:       path.Base(entry.path),
				Kind:        string(model.NodeKindFile),
				DocPath:     b.docPath(entry.path),
				HasChildren: b.hasSymbol(ctx, entry.path),
				Children:    []*ragindex.TreeNode{},
			})
			continue
		}
		children = append(children, &ragindex.TreeNode{
			ID:          "folder:" + entry.path,
			Label:       path.Base(entry.path),
			Kind:        "folder",
			HasChildren: true,
			Children:    []*ragindex.TreeNode{},
		})
	}
	sortTree(&ragindex.TreeNode{Children: children})
	return children, nil
}

// @intent convert one graph node into the Wiki tree node shape used by full and lazy builders.
func (b *Builder) treeNodeForModel(ctx context.Context, node model.Node, annotation *model.Annotation) *ragindex.TreeNode {
	treeNode := &ragindex.TreeNode{
		Summary:    summaryForNode(annotation),
		SearchText: ragindex.SearchTextForAnnotation(annotation),
		Children:   []*ragindex.TreeNode{},
	}
	switch node.Kind {
	case model.NodeKindPackage:
		treeNode.ID = "package:" + strings.Trim(path.Clean(node.FilePath), "/")
		treeNode.Label = path.Base(node.FilePath)
		treeNode.Kind = string(model.NodeKindPackage)
		treeNode.HasChildren = b.hasDirectFile(ctx, node.FilePath)
	case model.NodeKindFile:
		treeNode.ID = "file:" + strings.Trim(path.Clean(node.FilePath), "/")
		treeNode.Label = path.Base(node.FilePath)
		treeNode.Kind = string(model.NodeKindFile)
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
func (b *Builder) loadAnnotations(ctx context.Context, ids []uint) (map[uint]*model.Annotation, error) {
	annByID := map[uint]*model.Annotation{}
	if len(ids) == 0 {
		return annByID, nil
	}
	ns := ctxns.FromContext(ctx)
	var annotations []model.Annotation
	if err := b.DB.WithContext(ctx).
		Joins("JOIN nodes ON nodes.id = annotations.node_id").
		Where("annotations.node_id IN ? AND nodes.namespace = ?", ids, ns).
		Preload("Tags", func(db *gorm.DB) *gorm.DB {
			return db.Order("ordinal ASC, id ASC")
		}).
		Find(&annotations).Error; err != nil {
		return nil, trace.Wrap(err, "load wiki annotations")
	}
	for i := range annotations {
		annByID[annotations[i].NodeID] = &annotations[i]
	}
	return annByID, nil
}

// @intent test whether a folder or root path has any descendant package or file node.
func (b *Builder) hasPathDescendant(ctx context.Context, folderPath string) bool {
	nodes, err := b.loadPathNodes(ctx, folderPath, []string{string(model.NodeKindPackage), string(model.NodeKindFile)})
	return err == nil && len(nodes) > 0
}

// @intent test whether a package node has direct file children.
func (b *Builder) hasDirectFile(ctx context.Context, packagePath string) bool {
	nodes, err := b.loadPathNodes(ctx, packagePath, []string{string(model.NodeKindFile)})
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
	ns := ctxns.FromContext(ctx)
	var count int64
	err := b.DB.WithContext(ctx).
		Model(&model.Node{}).
		Where("namespace = ? AND file_path = ? AND kind IN ?", ns, filePath, symbolKindStrings()).
		Limit(1).
		Count(&count).Error
	return err == nil && count > 0
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

// @intent collect graph node IDs for batch annotation lookup.
func nodeIDs(nodes []model.Node) []uint {
	ids := make([]uint, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	return ids
}

// @intent expose symbol node kinds as strings for GORM IN clauses.
func symbolKindStrings() []string {
	return []string{string(model.NodeKindFunction), string(model.NodeKindClass), string(model.NodeKindType), string(model.NodeKindTest)}
}

// @intent expose path-bearing node kinds that can imply folder and file tree entries.
func lazyPathNodeKinds() []string {
	return []string{
		string(model.NodeKindPackage),
		string(model.NodeKindFile),
		string(model.NodeKindFunction),
		string(model.NodeKindClass),
		string(model.NodeKindType),
		string(model.NodeKindTest),
	}
}

// @intent hold one immediate child candidate while lazy tree nodes are materialized.
type lazyEntry struct {
	kind string
	path string
	node *model.Node
}

// @intent resolve the namespace used for both DB reads and wiki-index output paths.
func (b *Builder) namespace(ctx context.Context) string {
	ns := b.Namespace
	if ns == "" {
		ns = ctxns.FromContext(ctx)
	}
	if ns == "" {
		ns = ctxns.DefaultNamespace
	}
	return ns
}

// @intent load the graph node set needed for Wiki navigation and summaries.
func (b *Builder) loadNodes(ctx context.Context) ([]model.Node, map[uint]*model.Annotation, error) {
	ns := ctxns.FromContext(ctx)
	kinds := []string{
		string(model.NodeKindPackage),
		string(model.NodeKindFile),
		string(model.NodeKindFunction),
		string(model.NodeKindClass),
		string(model.NodeKindType),
		string(model.NodeKindTest),
	}
	var nodes []model.Node
	q := b.DB.WithContext(ctx).
		Where("namespace = ? AND kind IN ?", ns, kinds).
		Order("file_path ASC, start_line ASC, qualified_name ASC")
	if err := q.Find(&nodes).Error; err != nil {
		return nil, nil, trace.Wrap(err, "load wiki nodes")
	}
	if len(b.Exclude) > 0 {
		filtered := nodes[:0]
		for _, node := range nodes {
			if !pathutil.MatchExcludes(b.Exclude, node.FilePath) {
				filtered = append(filtered, node)
			}
		}
		nodes = filtered
	}

	ids := make([]uint, 0, len(nodes))
	for _, node := range nodes {
		ids = append(ids, node.ID)
	}
	annByID := map[uint]*model.Annotation{}
	if len(ids) > 0 {
		var annotations []model.Annotation
		if err := b.DB.WithContext(ctx).
			Joins("JOIN nodes ON nodes.id = annotations.node_id").
			Where("annotations.node_id IN ? AND nodes.namespace = ?", ids, ns).
			Preload("Tags", func(db *gorm.DB) *gorm.DB {
				return db.Order("ordinal ASC, id ASC")
			}).
			Find(&annotations).Error; err != nil {
			return nil, nil, trace.Wrap(err, "load wiki annotations")
		}
		for i := range annotations {
			annByID[annotations[i].NodeID] = &annotations[i]
		}
	}
	return nodes, annByID, nil
}

// @intent hold mutable lookup maps while building the folder/package/file Wiki tree.
type treeState struct {
	root     *ragindex.TreeNode
	folders  map[string]*ragindex.TreeNode
	packages map[string]*ragindex.TreeNode
	files    map[string]*ragindex.TreeNode
}

// @intent create folder nodes for path segments that are not themselves packages.
func (s *treeState) ensureFolder(folderPath string) *ragindex.TreeNode {
	folderPath = strings.Trim(path.Clean(folderPath), "/")
	if folderPath == "." || folderPath == "" {
		return s.root
	}
	if node := s.folders[folderPath]; node != nil {
		return node
	}
	parent := s.ensureFolder(path.Dir(folderPath))
	node := &ragindex.TreeNode{
		ID:       "folder:" + folderPath,
		Label:    path.Base(folderPath),
		Kind:     "folder",
		Children: []*ragindex.TreeNode{},
	}
	parent.Children = append(parent.Children, node)
	s.folders[folderPath] = node
	return node
}

// @intent ensure a package node exists under its containing folder.
func (s *treeState) ensurePackage(node model.Node, summary, searchText string) *ragindex.TreeNode {
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
	pkg := &ragindex.TreeNode{
		ID:         "package:" + pkgPath,
		Label:      path.Base(pkgPath),
		Kind:       "package",
		Summary:    summary,
		SearchText: searchText,
		Children:   []*ragindex.TreeNode{},
	}
	parent.Children = append(parent.Children, pkg)
	s.packages[pkgPath] = pkg
	return pkg
}

// @intent create a file node under its package when available, otherwise under its directory folder.
func (s *treeState) ensureFile(node model.Node, docPath, summary, searchText string) *ragindex.TreeNode {
	return s.ensureFileWithSummary(node.FilePath, docPath, summary, searchText)
}

// @intent ensure symbol-only files still appear in the Wiki tree even when no file node was parsed.
func (s *treeState) ensureFilePath(filePath, docPath string) *ragindex.TreeNode {
	return s.ensureFileWithSummary(filePath, docPath, "", "")
}

// @intent deduplicate file tree nodes while preserving the first useful summary and doc path.
func (s *treeState) ensureFileWithSummary(filePath, docPath, summary, searchText string) *ragindex.TreeNode {
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
	file := &ragindex.TreeNode{
		ID:         "file:" + filePath,
		Label:      path.Base(filePath),
		Kind:       "file",
		Summary:    summary,
		DocPath:    docPath,
		SearchText: searchText,
		Children:   []*ragindex.TreeNode{},
	}
	parent.Children = append(parent.Children, file)
	s.files[filePath] = file
	return file
}

// @intent identify graph node kinds that should appear as symbols under a file in the Wiki tree.
func isSymbolKind(kind model.NodeKind) bool {
	return kind == model.NodeKindFunction || kind == model.NodeKindClass || kind == model.NodeKindType || kind == model.NodeKindTest
}

// @intent choose the summary text that makes a Wiki node useful for scanning and search.
func summaryForNode(annotation *model.Annotation) string {
	if annotation == nil {
		return ""
	}
	for _, tag := range annotation.Tags {
		if tag.Kind == model.TagIndex {
			return tag.Value
		}
	}
	for _, tag := range annotation.Tags {
		if tag.Kind == model.TagIntent {
			return tag.Value
		}
	}
	return strings.TrimSpace(annotation.Summary)
}

// @intent expose full structured annotation metadata for Wiki symbol detail views.
func detailsForNode(node model.Node, annotation *model.Annotation) *ragindex.NodeDetails {
	detail := &ragindex.NodeDetails{
		QualifiedName: node.QualifiedName,
		FilePath:      node.FilePath,
		StartLine:     node.StartLine,
		EndLine:       node.EndLine,
		Language:      node.Language,
	}
	if annotation == nil {
		return detail
	}
	tags := make([]ragindex.DocTagDetail, 0, len(annotation.Tags))
	for _, tag := range annotation.Tags {
		tags = append(tags, ragindex.DocTagDetailFromModel(tag))
	}
	detail.Annotation = &ragindex.AnnotationDetail{
		Summary: strings.TrimSpace(annotation.Summary),
		Context: strings.TrimSpace(annotation.Context),
		Tags:    tags,
	}
	return detail
}

// @intent keep Wiki tree output deterministic across builds.
func sortTree(node *ragindex.TreeNode) {
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

// @intent provide the default index output directory when no explicit directory is configured.
func (b *Builder) indexDir() string {
	if b.IndexDir == "" {
		return ".ccg"
	}
	return b.IndexDir
}

// @intent map default and named namespaces to their wiki-index.json output path.
func (b *Builder) indexPath(namespace string) string {
	if ctxns.Normalize(namespace) == ctxns.DefaultNamespace {
		return filepath.Join(b.indexDir(), "wiki-index.json")
	}
	return filepath.Join(b.indexDir(), namespace, "wiki-index.json")
}

// @intent write wiki-index.json atomically so ccg-server never sees a partial tree.
func (b *Builder) writeIndex(namespace string, idx *ragindex.Index) error {
	target := b.indexPath(namespace)
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return trace.Wrap(err, "mkdir wiki index dir")
	}
	f, err := os.CreateTemp(dir, "wiki-index-*.tmp")
	if err != nil {
		return trace.Wrap(err, "create temp wiki index")
	}
	tmpName := f.Name()
	defer os.Remove(tmpName)
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(idx); err != nil {
		_ = f.Close()
		return trace.Wrap(err, "encode wiki index")
	}
	if err := f.Close(); err != nil {
		return trace.Wrap(err, "close wiki index")
	}
	if err := os.Rename(tmpName, target); err != nil {
		return trace.Wrap(err, "rename wiki-index.json")
	}
	return nil
}
