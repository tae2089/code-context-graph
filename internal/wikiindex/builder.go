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

// Builder writes wiki-index.json for ccg-server's browser UI.
// @intent derive a package/file/symbol presentation tree directly from graph nodes.
type Builder struct {
	DB          *gorm.DB
	OutDir      string
	IndexDir    string
	Namespace   string
	ProjectDesc string
	Exclude     []string
}

// Build creates wiki-index.json and returns package and file counts.
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
