// @index HTTP handlers for the ccg-server Wiki UI and its viewer-oriented JSON API.
package wikiserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/tae2089/code-context-graph/internal/ctxns"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/ragindex"
	wsvc "github.com/tae2089/code-context-graph/internal/workspace"
	"github.com/tae2089/trace"
)

const maxDocContentBytes = 1 << 20

// Config describes the external Wiki asset directory and data roots.
// @intent keep Wiki UI serving outside the ccg binary while letting ccg-server expose docs/RAG data.
type Config struct {
	StaticDir     string
	RagIndexDir   string
	NamespaceRoot string
	DB            *gorm.DB
	Logger        *slog.Logger
}

// Server serves static Wiki assets and small JSON APIs for docs exploration.
// @intent isolate browser-facing Wiki behavior from MCP transport and webhook handlers.
type Server struct {
	staticRoot    string
	ragIndexDir   string
	namespaceRoot string
	db            *gorm.DB
	logger        *slog.Logger
}

// New validates the Wiki static directory and creates a server instance.
// @intent fail server startup early when --wiki-dir points at an unusable dist directory.
func New(cfg Config) (*Server, error) {
	if strings.TrimSpace(cfg.StaticDir) == "" {
		return nil, fmt.Errorf("wiki static directory is required")
	}
	root, err := resolveExistingDir(cfg.StaticDir)
	if err != nil {
		return nil, trace.Wrap(err, "resolve wiki directory")
	}
	if _, err := os.Stat(filepath.Join(root, "index.html")); err != nil {
		return nil, trace.Wrap(err, "stat wiki index.html")
	}
	ragDir := cfg.RagIndexDir
	if ragDir == "" {
		ragDir = ".ccg"
	}
	nsRoot := cfg.NamespaceRoot
	if nsRoot == "" {
		nsRoot = "workspaces"
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		staticRoot:    root,
		ragIndexDir:   ragDir,
		namespaceRoot: nsRoot,
		db:            cfg.DB,
		logger:        logger,
	}, nil
}

// StaticHandler serves the React dist directory with SPA fallback to index.html.
// @intent let ccg-server expose the Wiki UI without embedding frontend assets into the binary.
// @sideEffect reads static files from --wiki-dir.
func (s *Server) StaticHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		rel := strings.TrimPrefix(r.URL.Path, "/wiki/")
		if rel == "" || rel == "." {
			rel = "index.html"
		}
		target, ok := s.safeStaticPath(rel)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if info, err := os.Stat(target); err == nil && !info.IsDir() {
			http.ServeFile(w, r, target)
			return
		}
		http.ServeFile(w, r, filepath.Join(s.staticRoot, "index.html"))
	})
}

// APIHandler routes Wiki JSON API requests.
// @intent provide browser-friendly access to namespaces, Wiki trees, docs, search, and copied context.
func (s *Server) APIHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/wiki/api/namespaces", s.handleNamespaces)
	mux.HandleFunc("/wiki/api/tree", s.handleTree)
	mux.HandleFunc("/wiki/api/doc", s.handleDoc)
	mux.HandleFunc("/wiki/api/search", s.handleSearch)
	mux.HandleFunc("/wiki/api/retrieve", s.handleRetrieve)
	mux.HandleFunc("/wiki/api/graph", s.handleGraph)
	mux.HandleFunc("/wiki/api/context", s.handleContext)
	return mux
}

// @intent resolve a request path under the static dist directory without allowing traversal.
func (s *Server) safeStaticPath(rel string) (string, bool) {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", false
	}
	target := filepath.Join(s.staticRoot, clean)
	cleanTarget := filepath.Clean(target)
	if cleanTarget != s.staticRoot && !strings.HasPrefix(cleanTarget, s.staticRoot+string(os.PathSeparator)) {
		return "", false
	}
	return cleanTarget, true
}

// @intent return namespaces discovered from graph data and existing wiki-index files.
func (s *Server) handleNamespaces(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	namespaces := map[string]struct{}{ctxns.DefaultNamespace: {}}
	if s.db != nil {
		var rows []string
		if err := s.db.WithContext(r.Context()).Model(&model.Node{}).Distinct("namespace").Order("namespace ASC").Pluck("namespace", &rows).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "list namespaces", err)
			return
		}
		for _, ns := range rows {
			namespaces[ctxns.Normalize(ns)] = struct{}{}
		}
	}
	for _, ns := range s.indexNamespaces() {
		namespaces[ns] = struct{}{}
	}
	items := make([]string, 0, len(namespaces))
	for ns := range namespaces {
		items = append(items, ns)
	}
	sort.Strings(items)
	writeJSON(w, http.StatusOK, map[string]any{"namespaces": items})
}

// @intent return the active namespace Wiki tree, optionally pruned for lighter UI payloads.
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	ns, ok := namespaceParam(w, r)
	if !ok {
		return
	}
	depth, err := boundedIntParam(r, "depth", 0, 0, 20)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	idx, err := s.loadIndex(ns)
	if err != nil {
		writeError(w, statusForReadErr(err), "load wiki-index", err)
		return
	}
	root := ragindex.PruneTree(idx.Root, depth)
	writeJSON(w, http.StatusOK, map[string]any{"namespace": ns, "built_at": idx.BuiltAt, "root": root})
}

// @intent search Wiki tree labels and summaries for the active namespace.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	ns, ok := namespaceParam(w, r)
	if !ok {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "q must not be empty", nil)
		return
	}
	limit, err := boundedIntParam(r, "limit", 20, 1, 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	idx, err := s.loadIndex(ns)
	if err != nil {
		writeError(w, statusForReadErr(err), "load wiki-index", err)
		return
	}
	results := ragindex.Search(idx.Root, query, limit)
	if results == nil {
		results = []ragindex.SearchResult{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"namespace": ns, "results": results})
}

// @intent run PageIndex-style retrieval against doc-index.json for LLM context discovery in the Wiki UI.
func (s *Server) handleRetrieve(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	ns, ok := namespaceParam(w, r)
	if !ok {
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "q must not be empty", nil)
		return
	}
	limit, err := boundedIntParam(r, "limit", 10, 1, 50)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	contentLimit, err := boundedIntParam(r, "content_limit", 0, 0, 20000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	idx, err := s.loadRAGIndex(ns)
	if err != nil {
		writeError(w, statusForReadErr(err), "load doc-index", err)
		return
	}
	candidates := ragindex.Retrieve(idx.Root, query, limit)
	results := make([]retrieveResult, 0, len(candidates))
	for _, candidate := range candidates {
		item := retrieveResult{RetrieveResult: candidate}
		if contentLimit > 0 && candidate.DocPath != "" {
			content, _, err := s.readDoc(ns, candidate.DocPath)
			if err != nil {
				writeError(w, statusForReadErr(err), "read retrieved doc", err)
				return
			}
			if len(content) > contentLimit {
				item.Content = content[:contentLimit]
				item.ContentTruncated = true
			} else {
				item.Content = content
			}
		}
		results = append(results, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"namespace": ns, "results": results})
}

// @intent return a bounded namespace graph for the browser force-directed graph viewer.
func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	if s.db == nil {
		writeError(w, http.StatusServiceUnavailable, "graph database is not configured", nil)
		return
	}
	ns, ok := namespaceParam(w, r)
	if !ok {
		return
	}
	limit, err := boundedIntParam(r, "limit", 800, 1, 2000)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	edgeKinds, err := graphEdgeKindsParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}

	var totalNodes int64
	if err := s.db.WithContext(r.Context()).
		Model(&model.Node{}).
		Where("namespace = ?", ns).
		Count(&totalNodes).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "count graph nodes", err)
		return
	}

	var nodes []model.Node
	if err := s.db.WithContext(r.Context()).
		Where("namespace = ?", ns).
		Order("kind ASC, file_path ASC, start_line ASC, qualified_name ASC").
		Limit(limit).
		Find(&nodes).Error; err != nil {
		writeError(w, http.StatusInternalServerError, "list graph nodes", err)
		return
	}

	nodeIDs := make([]uint, 0, len(nodes))
	nodeSet := make(map[uint]struct{}, len(nodes))
	graphNodes := make([]graphNode, 0, len(nodes))
	for _, node := range nodes {
		nodeIDs = append(nodeIDs, node.ID)
		nodeSet[node.ID] = struct{}{}
		graphNodes = append(graphNodes, graphNodeFromModel(node))
	}

	var edges []model.Edge
	if len(nodeIDs) > 0 {
		query := s.db.WithContext(r.Context()).
			Where("namespace = ? AND from_node_id IN ? AND to_node_id IN ?", ns, nodeIDs, nodeIDs)
		if len(edgeKinds) > 0 {
			query = query.Where("kind IN ?", edgeKinds)
		}
		if err := query.
			Order("kind ASC, file_path ASC, line ASC, id ASC").
			Limit(limit * 4).
			Find(&edges).Error; err != nil {
			writeError(w, http.StatusInternalServerError, "list graph edges", err)
			return
		}
	}

	graphEdges := make([]graphEdge, 0, len(edges))
	for _, edge := range edges {
		if _, ok := nodeSet[edge.FromNodeID]; !ok {
			continue
		}
		if _, ok := nodeSet[edge.ToNodeID]; !ok {
			continue
		}
		graphEdges = append(graphEdges, graphEdgeFromModel(edge))
	}

	writeJSON(w, http.StatusOK, graphResponse{
		Namespace: ns,
		Limit:     limit,
		Truncated: totalNodes > int64(len(nodes)),
		Nodes:     graphNodes,
		Edges:     graphEdges,
	})
}

// @intent read one generated Markdown document for display in the Wiki viewer.
func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	ns, ok := namespaceParam(w, r)
	if !ok {
		return
	}
	docPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if docPath == "" {
		writeError(w, http.StatusBadRequest, "path must not be empty", nil)
		return
	}
	content, resolved, err := s.readDoc(ns, docPath)
	if err != nil {
		if idx, indexErr := s.loadIndex(ns); indexErr == nil {
			if node := findDocPath(idx.Root, docPath); node != nil {
				writeJSON(w, http.StatusOK, map[string]any{
					"namespace": ns,
					"path":      docPath,
					"resolved":  "",
					"content":   nodeMarkdown(node),
					"generated": false,
				})
				return
			}
		}
		writeError(w, statusForReadErr(err), "read doc", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"namespace": ns,
		"path":      docPath,
		"resolved":  resolved,
		"content":   content,
		"generated": true,
	})
}

// @intent assemble selected docs or summaries into one Markdown block for LLM context.
func (s *Server) handleContext(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req contextRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "decode context request", err)
		return
	}
	ns := ctxns.Normalize(strings.TrimSpace(req.Namespace))
	if err := validateNamespace(ns); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	if len(req.Paths) > 20 {
		writeError(w, http.StatusBadRequest, "paths must contain <= 20 items", nil)
		return
	}
	idx, err := s.loadIndex(ns)
	if err != nil {
		writeError(w, statusForReadErr(err), "load wiki-index", err)
		return
	}
	sections := make([]string, 0, len(req.Paths))
	items := make([]contextItem, 0, len(req.Paths))
	for _, rawPath := range req.Paths {
		docPath := strings.TrimSpace(rawPath)
		if docPath == "" {
			continue
		}
		item := contextItem{Path: docPath}
		if content, _, err := s.readDoc(ns, docPath); err == nil {
			item.Found = true
			item.Markdown = fmt.Sprintf("## %s\n\n%s", docPath, strings.TrimSpace(content))
			sections = append(sections, item.Markdown)
		} else if node := findDocPath(idx.Root, docPath); node != nil {
			item.Found = true
			item.Label = node.Label
			item.Markdown = fmt.Sprintf("## %s\n\n%s", node.Label, strings.TrimSpace(node.Summary))
			sections = append(sections, item.Markdown)
		} else {
			item.Error = "not found"
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, contextResponse{Markdown: strings.Join(sections, "\n\n"), Items: items})
}

// @intent decode selected Wiki document paths from the context-copy request body.
type contextRequest struct {
	Namespace string   `json:"namespace"`
	Paths     []string `json:"paths"`
}

// @intent report whether one requested context item was found in docs or tree summaries.
type contextItem struct {
	Path     string `json:"path"`
	Label    string `json:"label,omitempty"`
	Found    bool   `json:"found"`
	Markdown string `json:"markdown,omitempty"`
	Error    string `json:"error,omitempty"`
}

// @intent return the assembled Markdown and per-item resolution status.
type contextResponse struct {
	Markdown string        `json:"markdown"`
	Items    []contextItem `json:"items"`
}

// @intent return PageIndex retrieval metadata and optional bounded Markdown content to the Wiki UI.
type retrieveResult struct {
	ragindex.RetrieveResult
	Content          string `json:"content,omitempty"`
	ContentTruncated bool   `json:"content_truncated,omitempty"`
}

// @intent describe one graph node in the Wiki force graph API.
type graphNode struct {
	ID            string                `json:"id"`
	Label         string                `json:"label"`
	Kind          string                `json:"kind"`
	QualifiedName string                `json:"qualified_name"`
	FilePath      string                `json:"file_path"`
	DocPath       string                `json:"doc_path,omitempty"`
	StartLine     int                   `json:"start_line,omitempty"`
	EndLine       int                   `json:"end_line,omitempty"`
	Language      string                `json:"language,omitempty"`
	Details       *ragindex.NodeDetails `json:"details,omitempty"`
}

// @intent describe one directed graph edge in the Wiki force graph API.
type graphEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
	File   string `json:"file_path,omitempty"`
	Line   int    `json:"line,omitempty"`
}

// @intent return bounded graph data and truncation metadata to the Wiki UI.
type graphResponse struct {
	Namespace string      `json:"namespace"`
	Limit     int         `json:"limit"`
	Truncated bool        `json:"truncated"`
	Nodes     []graphNode `json:"nodes"`
	Edges     []graphEdge `json:"edges"`
}

// @intent convert persisted graph node metadata into a browser graph payload.
func graphNodeFromModel(node model.Node) graphNode {
	label := node.Name
	if strings.TrimSpace(label) == "" {
		label = node.QualifiedName
	}
	if strings.TrimSpace(label) == "" {
		label = node.FilePath
	}
	out := graphNode{
		ID:            strconv.FormatUint(uint64(node.ID), 10),
		Label:         label,
		Kind:          string(node.Kind),
		QualifiedName: node.QualifiedName,
		FilePath:      node.FilePath,
		StartLine:     node.StartLine,
		EndLine:       node.EndLine,
		Language:      node.Language,
	}
	if node.Kind == model.NodeKindFile {
		out.DocPath = docPathForSource(node.FilePath)
	} else {
		out.Details = &ragindex.NodeDetails{
			QualifiedName: node.QualifiedName,
			FilePath:      node.FilePath,
			StartLine:     node.StartLine,
			EndLine:       node.EndLine,
			Language:      node.Language,
		}
	}
	return out
}

// @intent convert persisted edge metadata into a stable browser graph edge payload.
func graphEdgeFromModel(edge model.Edge) graphEdge {
	return graphEdge{
		ID:     fmt.Sprintf("%d:%d:%s:%d", edge.FromNodeID, edge.ToNodeID, edge.Kind, edge.ID),
		Source: strconv.FormatUint(uint64(edge.FromNodeID), 10),
		Target: strconv.FormatUint(uint64(edge.ToNodeID), 10),
		Kind:   string(edge.Kind),
		File:   edge.FilePath,
		Line:   edge.Line,
	}
}

// @intent convert repository-relative source paths to their generated Markdown doc path.
func docPathForSource(filePath string) string {
	rel := filePath
	if filepath.IsAbs(filePath) {
		rel = strings.TrimPrefix(filePath, string(filepath.Separator))
	}
	return filepath.Join("docs", rel+".md")
}

// @intent load the namespace-specific wiki-index from disk.
func (s *Server) loadIndex(namespace string) (*ragindex.Index, error) {
	return ragindex.LoadIndex(s.indexPath(namespace))
}

// @intent load the namespace-specific doc-index used by PageIndex retrieval.
func (s *Server) loadRAGIndex(namespace string) (*ragindex.Index, error) {
	if ctxns.Normalize(namespace) == ctxns.DefaultNamespace {
		return ragindex.LoadIndex(filepath.Join(s.ragIndexDir, "doc-index.json"))
	}
	return ragindex.LoadIndex(filepath.Join(s.ragIndexDir, namespace, "doc-index.json"))
}

// @intent map default and named namespaces to their wiki-index.json locations.
func (s *Server) indexPath(namespace string) string {
	if ctxns.Normalize(namespace) == ctxns.DefaultNamespace {
		return filepath.Join(s.ragIndexDir, "wiki-index.json")
	}
	return filepath.Join(s.ragIndexDir, namespace, "wiki-index.json")
}

// @intent discover namespaces that have generated wiki-index files even before graph rows exist.
func (s *Server) indexNamespaces() []string {
	var out []string
	if _, err := os.Stat(filepath.Join(s.ragIndexDir, "wiki-index.json")); err == nil {
		out = append(out, ctxns.DefaultNamespace)
	}
	entries, err := os.ReadDir(s.ragIndexDir)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		ns := entry.Name()
		if err := validateNamespace(ns); err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.ragIndexDir, ns, "wiki-index.json")); err == nil {
			out = append(out, ns)
		}
	}
	return out
}

// @intent enforce doc size limits before returning generated Markdown content.
func (s *Server) readDoc(namespace, docPath string) (string, string, error) {
	resolved, err := s.resolveDocPath(namespace, docPath)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", "", err
	}
	if info.IsDir() {
		return "", "", fs.ErrInvalid
	}
	if info.Size() > maxDocContentBytes {
		return "", "", fmt.Errorf("file exceeds 1 MB size limit")
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return "", "", err
	}
	return string(content), resolved, nil
}

// @intent resolve a generated doc path under approved docs, RAG, or namespace roots.
func (s *Server) resolveDocPath(namespace, docPath string) (string, error) {
	clean := filepath.Clean(docPath)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid path: path traversal not allowed")
	}
	var roots []string
	if namespace != ctxns.DefaultNamespace {
		roots = append(roots, filepath.Join(s.namespaceRoot, namespace))
	}
	roots = append(roots, ".", s.ragIndexDir, s.namespaceRoot)
	if filepath.IsAbs(clean) {
		for _, root := range roots {
			target, err := safeAbsolutePath(root, clean)
			if err != nil {
				continue
			}
			if _, err := os.Stat(target); err == nil {
				return target, nil
			}
		}
		return "", fs.ErrNotExist
	}
	for _, root := range roots {
		target, err := safePath(root, clean)
		if err != nil {
			continue
		}
		if _, err := os.Stat(target); err == nil {
			return target, nil
		}
	}
	return "", fs.ErrNotExist
}

// @intent find a tree node by its generated doc_path value.
func findDocPath(root *ragindex.TreeNode, docPath string) *ragindex.TreeNode {
	if root == nil {
		return nil
	}
	if root.DocPath == docPath {
		return root
	}
	for _, child := range root.Children {
		if found := findDocPath(child, docPath); found != nil {
			return found
		}
	}
	return nil
}

// @intent render a Wiki tree node as fallback Markdown when no generated file exists.
func nodeMarkdown(node *ragindex.TreeNode) string {
	parts := []string{fmt.Sprintf("# %s", node.Label)}
	if strings.TrimSpace(node.Summary) != "" {
		parts = append(parts, strings.TrimSpace(node.Summary))
	}
	if len(node.Children) > 0 {
		var children []string
		for _, child := range node.Children {
			children = append(children, "- "+child.Label)
		}
		parts = append(parts, strings.Join(children, "\n"))
	}
	return strings.Join(parts, "\n\n")
}

// @intent normalize and validate the namespace query parameter shared by Wiki API endpoints.
func namespaceParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	ns := ctxns.Normalize(strings.TrimSpace(r.URL.Query().Get("namespace")))
	if err := validateNamespace(ns); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), nil)
		return "", false
	}
	return ns, true
}

// @intent keep namespace path validation aligned with workspace filesystem rules.
func validateNamespace(namespace string) error {
	if namespace == ctxns.DefaultNamespace {
		return nil
	}
	return wsvc.ValidatePath(namespace, "")
}

// @intent parse the optional edge_kinds filter for the Wiki graph API.
func graphEdgeKindsParam(r *http.Request) ([]model.EdgeKind, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("edge_kinds"))
	if raw == "" {
		return nil, nil
	}
	allowed := map[model.EdgeKind]struct{}{
		model.EdgeKindCalls:         {},
		model.EdgeKindFallbackCalls: {},
		model.EdgeKindImportsFrom:   {},
		model.EdgeKindInherits:      {},
		model.EdgeKindImplements:    {},
		model.EdgeKindContains:      {},
		model.EdgeKindTestedBy:      {},
		model.EdgeKindDependsOn:     {},
		model.EdgeKindReferences:    {},
	}
	var out []model.EdgeKind
	seen := map[model.EdgeKind]struct{}{}
	for _, item := range strings.Split(raw, ",") {
		kind := model.EdgeKind(strings.TrimSpace(item))
		if kind == "" {
			continue
		}
		if _, ok := allowed[kind]; !ok {
			return nil, fmt.Errorf("unsupported edge kind %q", kind)
		}
		if _, ok := seen[kind]; ok {
			continue
		}
		seen[kind] = struct{}{}
		out = append(out, kind)
	}
	return out, nil
}

// @intent parse bounded integer query parameters for lightweight API pagination and tree depth.
func boundedIntParam(r *http.Request, name string, fallback, minValue, maxValue int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	if value < minValue || value > maxValue {
		return 0, fmt.Errorf("%s must be between %d and %d", name, minValue, maxValue)
	}
	return value, nil
}

// @intent reject unsupported HTTP methods with a consistent status code.
func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

// @intent write a JSON response with a stable content type.
func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

// @intent write a compact JSON error payload for browser API callers.
func writeError(w http.ResponseWriter, code int, message string, err error) {
	resp := map[string]any{"error": message}
	if err != nil {
		resp["detail"] = err.Error()
	}
	writeJSON(w, code, resp)
}

// @intent map filesystem and validation failures to browser-appropriate HTTP status codes.
func statusForReadErr(err error) int {
	if strings.Contains(err.Error(), "path traversal") || strings.Contains(err.Error(), "outside root") {
		return http.StatusBadRequest
	}
	if errors.Is(err, fs.ErrNotExist) {
		return http.StatusNotFound
	}
	if errors.Is(err, fs.ErrPermission) {
		return http.StatusForbidden
	}
	return http.StatusInternalServerError
}

// @intent resolve a relative path under one root while rejecting traversal and symlink escapes.
func safePath(root, relPath string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	realRoot := filepath.Clean(absRoot)
	if stat, err := os.Stat(absRoot); err == nil && stat.IsDir() {
		if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
			realRoot = filepath.Clean(resolved)
		}
	}
	target := filepath.Join(realRoot, filepath.Clean(relPath))
	target = filepath.Clean(target)
	if target != realRoot && !strings.HasPrefix(target, realRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("path is outside root")
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = filepath.Clean(resolved)
		if target != realRoot && !strings.HasPrefix(target, realRoot+string(os.PathSeparator)) {
			return "", fmt.Errorf("path is outside root")
		}
	}
	return target, nil
}

// @intent validate an absolute wiki-index path against one approved root.
func safeAbsolutePath(root, absPath string) (string, error) {
	if !filepath.IsAbs(absPath) {
		return "", fmt.Errorf("path is not absolute")
	}
	realRoot, err := realPathRoot(root)
	if err != nil {
		return "", err
	}
	target := filepath.Clean(absPath)
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = filepath.Clean(resolved)
	}
	if target != realRoot && !strings.HasPrefix(target, realRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("path is outside root")
	}
	return target, nil
}

// @intent resolve an allowed root to an absolute symlink-aware path for containment checks.
func realPathRoot(root string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	realRoot := filepath.Clean(absRoot)
	if stat, err := os.Stat(absRoot); err == nil && stat.IsDir() {
		if resolved, err := filepath.EvalSymlinks(absRoot); err == nil {
			realRoot = filepath.Clean(resolved)
		}
	}
	return realRoot, nil
}

// @intent resolve and validate an existing static asset directory.
func resolveExistingDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", path)
	}
	return filepath.Clean(real), nil
}
