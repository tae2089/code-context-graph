// @index Edge resolution helpers that attach parsed relationships to stored graph node IDs.
package edgeresolve

import (
	"context"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/tae2089/code-context-graph/internal/model"
)

// NodeLookup provides the node reads needed to resolve parsed edge endpoints.
// @intent keep edge endpoint resolution independent of the concrete graph store.
type NodeLookup interface {
	GetNodesByIDs(ctx context.Context, ids []uint) ([]model.Node, error)
	GetNodesByFiles(ctx context.Context, filePaths []string) (map[string][]model.Node, error)
	GetNodesByQualifiedNames(ctx context.Context, names []string) (map[string][]model.Node, error)
}

// edgeLookup provides methods to find existing edges in the graph.
// @intent abstract edge retrieval for resolution state management.
type edgeLookup interface {
	GetEdgesToNodes(ctx context.Context, nodeIDs []uint) ([]model.Edge, error)
}

// filePrefixLookup provides methods to find file nodes by their path suffix.
// @intent support resolving imports when only partial path information is available.
type filePrefixLookup interface {
	GetFileNodesByPathSuffix(ctx context.Context, suffix string) ([]model.Node, error)
}

// resolveState holds indexed node data to facilitate efficient edge endpoint resolution.
// @intent cache and index nodes by various keys (file, name, QN) during a single Resolve pass.
type resolveState struct {
	nodesByFile    map[string][]model.Node
	qnIndex        map[string][]model.Node
	nameByFile     map[string]map[string][]model.Node
	fileNodeByPath map[string]model.Node
	nodeByID       map[uint]model.Node
	implementsBy   map[string][]model.Node
}

// Resolve fills FromNodeID and ToNodeID for parsed edges when a unique local
// endpoint can be inferred from stored node positions and names.
// @intent convert syntax-level edge fingerprints into traversable graph edges.
func Resolve(ctx context.Context, lookup NodeLookup, edges []model.Edge) ([]model.Edge, error) {
	if len(edges) == 0 {
		return edges, nil
	}

	files := edgeFiles(edges)
	nodesByFile, err := lookup.GetNodesByFiles(ctx, files)
	if err != nil {
		return nil, err
	}
	nodes := flattenNodes(nodesByFile)
	st := &resolveState{
		nodesByFile:    nodesByFile,
		qnIndex:        indexByQualifiedName(nodes),
		nameByFile:     indexByNameByFile(nodes),
		fileNodeByPath: indexFileNodes(nodes),
		nodeByID:       indexByID(nodes),
		implementsBy:   make(map[string][]model.Node),
	}

	qnCandidates := collectQualifiedCandidates(edges, st)
	if len(qnCandidates) > 0 {
		queried, err := lookup.GetNodesByQualifiedNames(ctx, qnCandidates)
		if err != nil {
			return nil, err
		}
		for _, ns := range queried {
			st.addNodes(ns)
		}
	}

	out := append([]model.Edge(nil), edges...)
	for i := range out {
		switch out[i].Kind {
		case model.EdgeKindContains:
			resolveContains(&out[i], st)
		case model.EdgeKindImplements:
			resolveImplements(&out[i], st)
		case model.EdgeKindImportsFrom:
			resolveImportsFrom(ctx, lookup, &out[i], st)
		case model.EdgeKindInherits:
			resolveInherits(&out[i], st)
		case model.EdgeKindTestedBy:
			resolveTestedBy(&out[i], st)
		}
	}
	if err := st.loadExistingImplements(ctx, lookup); err != nil {
		return nil, err
	}
	if err := st.ensureDispatchTargets(ctx, lookup, out); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Kind == model.EdgeKindCalls {
			resolveCall(&out[i], st)
		}
	}
	return out, nil
}

// FilterResolved returns only edges that have both persisted endpoints.
// @intent prevent unresolved syntax candidates from occupying fingerprints before they become traversable
func FilterResolved(edges []model.Edge) []model.Edge {
	if len(edges) == 0 {
		return nil
	}
	out := make([]model.Edge, 0, len(edges))
	for _, edge := range edges {
		if edge.FromNodeID == 0 || edge.ToNodeID == 0 {
			continue
		}
		out = append(out, edge)
	}
	return out
}

// edgeFiles extracts unique file paths from a set of edges.
// @intent identify all files involved in a resolution pass to batch node lookups.
func edgeFiles(edges []model.Edge) []string {
	seen := make(map[string]bool)
	var files []string
	for _, e := range edges {
		if e.FilePath == "" || seen[e.FilePath] {
			continue
		}
		seen[e.FilePath] = true
		files = append(files, e.FilePath)
	}
	return files
}

// flattenNodes converts a file-keyed node map into a flat slice.
// @intent prepare nodes for indexing and state population.
func flattenNodes(nodesByFile map[string][]model.Node) []model.Node {
	var nodes []model.Node
	for _, ns := range nodesByFile {
		nodes = append(nodes, ns...)
	}
	return nodes
}

// indexByQualifiedName groups nodes by their fully qualified names.
// @intent enable fast lookup of symbols during endpoint resolution.
func indexByQualifiedName(nodes []model.Node) map[string][]model.Node {
	index := make(map[string][]model.Node)
	for _, n := range nodes {
		index[n.QualifiedName] = append(index[n.QualifiedName], n)
	}
	return index
}

// indexByID maps nodes by their unique persistence IDs.
// @intent allow direct node access when IDs are available from previous steps or DB.
func indexByID(nodes []model.Node) map[uint]model.Node {
	index := make(map[uint]model.Node)
	for _, n := range nodes {
		if n.ID != 0 {
			index[n.ID] = n
		}
	}
	return index
}

// indexByNameByFile indexes callable nodes (functions/tests) by name within each file.
// @intent resolve bare name references when they occur in the same file as the caller.
func indexByNameByFile(nodes []model.Node) map[string]map[string][]model.Node {
	index := make(map[string]map[string][]model.Node)
	for _, n := range nodes {
		if n.Kind != model.NodeKindFunction && n.Kind != model.NodeKindTest {
			continue
		}
		if index[n.FilePath] == nil {
			index[n.FilePath] = make(map[string][]model.Node)
		}
		index[n.FilePath][n.Name] = append(index[n.FilePath][n.Name], n)
	}
	return index
}

// indexFileNodes maps file paths to their corresponding file nodes.
// @intent provide quick access to file-level metadata during resolution.
func indexFileNodes(nodes []model.Node) map[string]model.Node {
	index := make(map[string]model.Node)
	for _, n := range nodes {
		if n.Kind == model.NodeKindFile {
			index[n.FilePath] = n
		}
	}
	return index
}

// collectQualifiedCandidates identifies potential target QNs from edges to pre-fetch.
// @intent optimize performance by batching node lookups across all edge types.
func collectQualifiedCandidates(edges []model.Edge, st *resolveState) []string {
	seen := make(map[string]bool)
	var names []string
	for _, e := range edges {
		switch e.Kind {
		case model.EdgeKindCalls:
			callee, ok := callCallee(e)
			if !ok || callee == "" {
				continue
			}
			addName(&names, seen, callee)
			if caller := enclosingCallable(st.nodesByFile[e.FilePath], e.Line); caller != nil {
				pkg := packagePrefix(*caller)
				bare := lastSegment(callee)
				if pkg != "" && bare != "" {
					addName(&names, seen, pkg+"."+bare)
				}
				if dispatch := dispatchForLanguage(caller.Language); dispatch != nil {
					for _, candidate := range dispatch.CollectQualifiedCallCandidates(*caller, callee) {
						addName(&names, seen, candidate)
					}
				}
			}
		case model.EdgeKindContains:
			if qn, ok := containsTarget(e); ok {
				addName(&names, seen, qn)
			}
		case model.EdgeKindImplements:
			if impl, iface, ok := implementsEndpoints(e); ok {
				addEndpointCandidates(&names, seen, st, e.FilePath, impl)
				addEndpointCandidates(&names, seen, st, e.FilePath, iface)
			}
		case model.EdgeKindImportsFrom:
			if path, ok := importsFromTarget(e); ok {
				addName(&names, seen, path)
			}
		case model.EdgeKindInherits:
			if child, parent, ok := inheritsEndpoints(e); ok {
				addEndpointCandidates(&names, seen, st, e.FilePath, child)
				addEndpointCandidates(&names, seen, st, e.FilePath, parent)
			}
		case model.EdgeKindTestedBy:
			if bare, testQN, ok := testedByEndpoints(e); ok {
				addEndpointCandidates(&names, seen, st, e.FilePath, bare)
				addName(&names, seen, testQN)
			}
		}
	}
	return names
}

// addName adds a name to the candidate list if not already seen.
// @intent ensure unique symbol names are collected for batch lookups.
func addName(names *[]string, seen map[string]bool, name string) {
	if name == "" || seen[name] {
		return
	}
	seen[name] = true
	*names = append(*names, name)
}

// addEndpointCandidates adds both bare and package-qualified names to candidates.
// @intent support resolving local symbols that might be referenced without full qualification.
func addEndpointCandidates(names *[]string, seen map[string]bool, st *resolveState, filePath, endpoint string) {
	addName(names, seen, endpoint)
	if strings.Contains(endpoint, ".") {
		return
	}
	if pkg := packageForFile(st.nodesByFile[filePath]); pkg != "" {
		addName(names, seen, pkg+"."+endpoint)
	}
}

// resolveCall attempts to attach node IDs to a function call edge.
// @intent find the unique caller and callee nodes for a call relationship.
func resolveCall(edge *model.Edge, st *resolveState) {
	caller := enclosingCallable(st.nodesByFile[edge.FilePath], edge.Line)
	if caller != nil {
		edge.FromNodeID = caller.ID
	}

	callee, ok := callCallee(*edge)
	if !ok || callee == "" {
		return
	}

	if target := resolveSameReceiverCall(caller, callee, st); target != nil {
		edge.ToNodeID = target.ID
		return
	}
	if target := resolveInterfaceDispatch(caller, callee, st); target != nil {
		edge.ToNodeID = target.ID
		return
	}

	if target := uniqueCallable(st.qnIndex[callee]); target != nil {
		edge.ToNodeID = target.ID
		return
	}

	bare := lastSegment(callee)
	if caller != nil {
		pkg := packagePrefix(*caller)
		if pkg != "" {
			if target := uniqueCallable(st.qnIndex[pkg+"."+bare]); target != nil {
				edge.ToNodeID = target.ID
				return
			}
		}
	}

	if target := uniqueCallable(st.nameByFile[edge.FilePath][bare]); target != nil {
		edge.ToNodeID = target.ID
	}
}

// resolveContains attaches node IDs to a containment edge (file contains symbol).
// @intent link file nodes to the top-level symbols they define.
func resolveContains(edge *model.Edge, st *resolveState) {
	if fileNode, ok := st.fileNodeByPath[edge.FilePath]; ok {
		edge.FromNodeID = fileNode.ID
	}
	qn, ok := containsTarget(*edge)
	if !ok {
		return
	}
	if target := uniqueNode(st.qnIndex[qn]); target != nil {
		edge.ToNodeID = target.ID
	}
}

// resolveImplements links a concrete type to an interface it implements.
// @intent capture implementation relationships and populate implementer cache.
func resolveImplements(edge *model.Edge, st *resolveState) {
	impl, iface, ok := implementsEndpoints(*edge)
	if !ok {
		return
	}
	if concrete := resolveTypeEndpoint(st, edge.FilePath, impl); concrete != nil {
		edge.FromNodeID = concrete.ID
	}
	if target := resolveTypeEndpoint(st, edge.FilePath, iface); target != nil {
		edge.ToNodeID = target.ID
	}
	if edge.FromNodeID != 0 && edge.ToNodeID != 0 {
		if concrete, ok := st.nodeByID[edge.FromNodeID]; ok {
			if ifaceNode, ok := st.nodeByID[edge.ToNodeID]; ok {
				st.addImplementer(ifaceNode, concrete)
			}
		}
	}
}

// resolveImportsFrom attaches node IDs to an import relationship.
// @intent link importing files to their target packages or files.
func resolveImportsFrom(ctx context.Context, lookup NodeLookup, edge *model.Edge, st *resolveState) {
	if fileNode, ok := st.fileNodeByPath[edge.FilePath]; ok {
		edge.FromNodeID = fileNode.ID
	}
	importPath, ok := importsFromTarget(*edge)
	if !ok {
		return
	}
	if target := uniquePackageNode(st.qnIndex[importPath]); target != nil {
		edge.ToNodeID = target.ID
		st.loadImportFileNodes(ctx, lookup, importPath)
		return
	}
	if target := uniqueFileNode(st.qnIndex[importPath]); target != nil {
		edge.ToNodeID = target.ID
		st.loadFileNodes(ctx, lookup, target.FilePath)
		return
	}
	if target := resolveImportFile(ctx, lookup, st, importPath); target != nil {
		edge.ToNodeID = target.ID
		st.loadFileNodes(ctx, lookup, target.FilePath)
	}
}

// loadImportFileNodes fetches file nodes for all files belonging to a package.
// @intent populate state with file nodes to support deeper resolution of imported symbols.
func (st *resolveState) loadImportFileNodes(ctx context.Context, lookup NodeLookup, importPath string) {
	prefixLookup, ok := lookup.(filePrefixLookup)
	if !ok || importPath == "" {
		return
	}
	nodes, err := prefixLookup.GetFileNodesByPathSuffix(ctx, importPath)
	if err != nil {
		return
	}
	for _, node := range uniqueFileNodes(nodes) {
		st.loadFileNodes(ctx, lookup, node.FilePath)
	}
}

// loadFileNodes fetches all symbols defined in a specific file.
// @intent ensure target file contents are available for cross-file resolution.
func (st *resolveState) loadFileNodes(ctx context.Context, lookup NodeLookup, filePath string) {
	if filePath == "" {
		return
	}
	if nodes, ok := st.nodesByFile[filePath]; ok && len(nodes) > 1 {
		return
	}
	loaded, err := lookup.GetNodesByFiles(ctx, []string{filePath})
	if err != nil {
		return
	}
	st.addNodes(loaded[filePath])
}

// resolveImportFile finds a file node matching an import path string.
// @intent map language-specific import paths to physical file nodes in the graph.
func resolveImportFile(ctx context.Context, lookup NodeLookup, st *resolveState, importPath string) *model.Node {
	if importPath == "" {
		return nil
	}
	if node, ok := st.fileNodeByPath[importPath]; ok {
		return &node
	}
	importPath = strings.Trim(path.Clean(importPath), "/")
	if target := bestImportFileMatch(st.fileNodeByPath, importPath); target != nil {
		return target
	}
	if prefixLookup, ok := lookup.(filePrefixLookup); ok {
		queried, err := prefixLookup.GetFileNodesByPathSuffix(ctx, importPath)
		if err == nil {
			return representativeImportFile(queried)
		}
	}
	return nil
}

// bestImportFileMatch finds the most likely file node for an import path by suffix matching.
// @intent handle cases where import paths don't exactly match file system paths.
func bestImportFileMatch(fileNodeByPath map[string]model.Node, importPath string) *model.Node {
	var exact []model.Node
	var candidates []model.Node
	bestDepth := -1
	for _, node := range fileNodeByPath {
		dir := strings.Trim(path.Dir(node.FilePath), "/")
		if dir == "." || dir == "" {
			continue
		}
		if importPath == dir {
			exact = append(exact, node)
			continue
		}
		if depth := commonSuffixDepth(importPath, dir); depth > 0 {
			if depth > bestDepth {
				bestDepth = depth
				candidates = []model.Node{node}
				continue
			}
			if depth == bestDepth {
				candidates = append(candidates, node)
			}
		}
	}
	if target := representativeImportFile(exact); target != nil {
		return target
	}
	if len(exact) > 0 {
		return nil
	}
	return representativeImportFile(candidates)
}

// representativeImportFile picks a stable representative file from a set of candidates.
// @intent ensure deterministic resolution when multiple files match an import path.
func representativeImportFile(nodes []model.Node) *model.Node {
	files := uniqueFileNodes(nodes)
	if len(files) == 0 {
		return nil
	}
	firstDir := strings.Trim(path.Dir(files[0].FilePath), "/")
	for _, node := range files[1:] {
		if strings.Trim(path.Dir(node.FilePath), "/") != firstDir {
			return nil
		}
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].FilePath < files[j].FilePath
	})
	return &files[0]
}

// uniquePackageNode extracts a single package node from a list.
// @intent return nil if multiple ambiguous packages match the QN.
func uniquePackageNode(nodes []model.Node) *model.Node {
	var found *model.Node
	seen := make(map[uint]bool)
	for i := range nodes {
		if nodes[i].Kind != model.NodeKindPackage {
			continue
		}
		if nodes[i].ID != 0 && seen[nodes[i].ID] {
			continue
		}
		if nodes[i].ID != 0 {
			seen[nodes[i].ID] = true
		}
		if found != nil {
			return nil
		}
		found = &nodes[i]
	}
	return found
}

// uniqueFileNodes filters a list of nodes to unique file-kind nodes.
// @intent identify distinct files in a set of result nodes.
func uniqueFileNodes(nodes []model.Node) []model.Node {
	seen := make(map[uint]bool)
	var files []model.Node
	for _, node := range nodes {
		if node.Kind != model.NodeKindFile {
			continue
		}
		if node.ID != 0 && seen[node.ID] {
			continue
		}
		if node.ID != 0 {
			seen[node.ID] = true
		}
		files = append(files, node)
	}
	return files
}

// commonSuffixDepth calculates the number of matching directory segments from the end.
// @intent score path similarity for fuzzy import resolution.
func commonSuffixDepth(a, b string) int {
	a = strings.Trim(a, "/")
	b = strings.Trim(b, "/")
	if a == "" || b == "" {
		return 0
	}
	aParts := strings.Split(a, "/")
	bParts := strings.Split(b, "/")
	depth := 0
	for i, j := len(aParts)-1, len(bParts)-1; i >= 0 && j >= 0; i, j = i-1, j-1 {
		if aParts[i] != bParts[j] {
			break
		}
		depth++
	}
	return depth
}

// resolveInherits attaches node IDs to an inheritance relationship.
// @intent link subclasses or derived types to their parents.
func resolveInherits(edge *model.Edge, st *resolveState) {
	child, parent, ok := inheritsEndpoints(*edge)
	if !ok {
		return
	}
	if from := resolveTypeEndpoint(st, edge.FilePath, child); from != nil {
		edge.FromNodeID = from.ID
	}
	if to := resolveTypeEndpoint(st, edge.FilePath, parent); to != nil {
		edge.ToNodeID = to.ID
	}
}

// resolveTestedBy links a test function to its production code counterpart.
// @intent bridge the gap between tests and the symbols they verify.
func resolveTestedBy(edge *model.Edge, st *resolveState) {
	callee, testQN, ok := testedByEndpoints(*edge)
	if !ok {
		return
	}
	if from := uniqueCallable(st.qnIndex[testQN]); from != nil {
		edge.FromNodeID = from.ID
	}
	if to := resolveProductionFunction(st, edge.FilePath, callee); to != nil {
		edge.ToNodeID = to.ID
	}
}

// resolveProductionFunction finds a production symbol matching a test callee name.
// @intent locate the tested symbol by checking qualified and bare name matches.
func resolveProductionFunction(st *resolveState, testFilePath, callee string) *model.Node {
	if target := uniqueCallable(st.qnIndex[callee]); target != nil {
		return target
	}
	bare := lastSegment(callee)
	pkg := packageForFile(st.nodesByFile[testFilePath])
	if pkg != "" {
		if target := uniqueCallable(st.qnIndex[pkg+"."+bare]); target != nil {
			return target
		}
	}
	return uniqueCallable(st.nameByFile[testFilePath][bare])
}

// uniqueFileNode extracts a single file node from a list.
// @intent return nil if multiple ambiguous files match.
func uniqueFileNode(nodes []model.Node) *model.Node {
	var found *model.Node
	seen := make(map[uint]bool)
	for i := range nodes {
		if nodes[i].Kind != model.NodeKindFile {
			continue
		}
		if nodes[i].ID != 0 && seen[nodes[i].ID] {
			continue
		}
		if nodes[i].ID != 0 {
			seen[nodes[i].ID] = true
		}
		if found != nil {
			return nil
		}
		found = &nodes[i]
	}
	return found
}

// importsFromTarget parses an import edge fingerprint to extract the target path.
// @intent retrieve the original import string from the persisted fingerprint.
func importsFromTarget(edge model.Edge) (string, bool) {
	prefix := "imports_from:" + edge.FilePath + ":"
	if !strings.HasPrefix(edge.Fingerprint, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(edge.Fingerprint, prefix)
	idx := strings.LastIndex(rest, ":")
	if idx < 0 {
		return "", false
	}
	if _, err := strconv.Atoi(rest[idx+1:]); err != nil {
		return "", false
	}
	path := rest[:idx]
	return path, path != ""
}

// inheritsEndpoints parses an inheritance edge fingerprint to extract endpoints.
// @intent retrieve subclass and parent names from the persisted fingerprint.
func inheritsEndpoints(edge model.Edge) (string, string, bool) {
	prefix := "inherits:" + edge.FilePath + ":"
	if !strings.HasPrefix(edge.Fingerprint, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(edge.Fingerprint, prefix)
	idx := strings.LastIndex(rest, ":")
	if idx < 0 {
		return "", "", false
	}
	child := rest[:idx]
	parent := rest[idx+1:]
	return child, parent, child != "" && parent != ""
}

// testedByEndpoints parses a test edge fingerprint to extract endpoints.
// @intent retrieve test and production symbol names from the persisted fingerprint.
func testedByEndpoints(edge model.Edge) (string, string, bool) {
	prefix := "tested_by:" + edge.FilePath + ":"
	if !strings.HasPrefix(edge.Fingerprint, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(edge.Fingerprint, prefix)
	idx := strings.LastIndex(rest, ":")
	if idx < 0 {
		return "", "", false
	}
	bare := rest[:idx]
	testQN := rest[idx+1:]
	return bare, testQN, bare != "" && testQN != ""
}

// loadExistingImplements populates implementer cache from existing DB edges.
// @intent enable cross-file interface resolution by loading historical data.
func (st *resolveState) loadExistingImplements(ctx context.Context, lookup NodeLookup) error {
	edgeReader, ok := lookup.(edgeLookup)
	if !ok {
		return nil
	}
	var ifaceIDs []uint
	for _, nodes := range st.qnIndex {
		for _, n := range nodes {
			if n.Kind == model.NodeKindType && n.ID != 0 {
				ifaceIDs = append(ifaceIDs, n.ID)
			}
		}
	}
	if len(ifaceIDs) == 0 {
		return nil
	}
	edges, err := edgeReader.GetEdgesToNodes(ctx, ifaceIDs)
	if err != nil {
		return err
	}
	var missingIDs []uint
	for _, e := range edges {
		if e.Kind != model.EdgeKindImplements || e.FromNodeID == 0 || e.ToNodeID == 0 {
			continue
		}
		if _, ok := st.nodeByID[e.FromNodeID]; !ok {
			missingIDs = append(missingIDs, e.FromNodeID)
		}
		if _, ok := st.nodeByID[e.ToNodeID]; !ok {
			missingIDs = append(missingIDs, e.ToNodeID)
		}
	}
	if len(missingIDs) > 0 {
		nodes, err := lookup.GetNodesByIDs(ctx, missingIDs)
		if err != nil {
			return err
		}
		st.addNodes(nodes)
	}
	for _, e := range edges {
		if e.Kind != model.EdgeKindImplements || e.FromNodeID == 0 || e.ToNodeID == 0 {
			continue
		}
		concrete, okConcrete := st.nodeByID[e.FromNodeID]
		iface, okIface := st.nodeByID[e.ToNodeID]
		if okConcrete && okIface {
			st.addImplementer(iface, concrete)
		}
	}
	return nil
}

// ensureDispatchTargets pre-fetches potential interface method implementations.
// @intent batch load nodes needed to resolve polymorphic calls.
func (st *resolveState) ensureDispatchTargets(ctx context.Context, lookup NodeLookup, edges []model.Edge) error {
	seen := make(map[string]bool)
	var names []string
	for _, e := range edges {
		if e.Kind != model.EdgeKindCalls {
			continue
		}
		callee, ok := callCallee(e)
		if !ok {
			continue
		}
		caller := enclosingCallable(st.nodesByFile[e.FilePath], e.Line)
		dispatch := dispatchForLanguage(callerLanguage(caller))
		if caller == nil || dispatch == nil {
			continue
		}
		for _, candidate := range dispatch.EnsureDispatchTargets(caller, callee, st) {
			addName(&names, seen, candidate)
		}
	}
	if len(names) == 0 {
		return nil
	}
	queried, err := lookup.GetNodesByQualifiedNames(ctx, names)
	if err != nil {
		return err
	}
	for _, ns := range queried {
		st.addNodes(ns)
	}
	return nil
}

// addNodes indexes multiple nodes into the resolution state.
// @intent batch add nodes to internal indexes.
func (st *resolveState) addNodes(nodes []model.Node) {
	for _, n := range nodes {
		st.indexNode(n)
	}
}

// indexNode adds a single node to all relevant resolution indexes.
// @intent maintain consistent node indexing by ID, QN, file, and name.
func (st *resolveState) indexNode(n model.Node) {
	st.qnIndex[n.QualifiedName] = appendUniqueNode(st.qnIndex[n.QualifiedName], n)
	if n.ID != 0 {
		st.nodeByID[n.ID] = n
	}
	st.nodesByFile[n.FilePath] = appendUniqueNode(st.nodesByFile[n.FilePath], n)
	if n.Kind == model.NodeKindFunction || n.Kind == model.NodeKindTest {
		if st.nameByFile[n.FilePath] == nil {
			st.nameByFile[n.FilePath] = make(map[string][]model.Node)
		}
		st.nameByFile[n.FilePath][n.Name] = appendUniqueNode(st.nameByFile[n.FilePath][n.Name], n)
	}
	if n.Kind == model.NodeKindFile {
		st.fileNodeByPath[n.FilePath] = n
	}
}

// addImplementer records an implementation link in the local cache.
// @intent track interface-implementer pairs for method dispatch resolution.
func (st *resolveState) addImplementer(iface model.Node, concrete model.Node) {
	st.implementsBy[iface.QualifiedName] = appendUniqueNode(st.implementsBy[iface.QualifiedName], concrete)
	st.implementsBy[iface.Name] = appendUniqueNode(st.implementsBy[iface.Name], concrete)
}

// resolveTypeEndpoint finds a type node (class/interface) by name or QN.
// @intent resolve symbol references to physical type nodes in the graph.
func resolveTypeEndpoint(st *resolveState, filePath, endpoint string) *model.Node {
	if target := uniqueTypeNode(st.qnIndex[endpoint]); target != nil {
		return target
	}
	if !strings.Contains(endpoint, ".") {
		if pkg := packageForFile(st.nodesByFile[filePath]); pkg != "" {
			if target := uniqueTypeNode(st.qnIndex[pkg+"."+endpoint]); target != nil {
				return target
			}
		}
		if target := uniqueTypeNodeByName(st.nodesByFile[filePath], endpoint); target != nil {
			return target
		}
	}
	return nil
}

// resolveSameReceiverCall attempts to resolve method calls within the same type.
// @intent optimize resolution of 'this' or same-receiver method calls in Go.
func resolveSameReceiverCall(caller *model.Node, callee string, st *resolveState) *model.Node {
	dispatch := dispatchForLanguage(callerLanguage(caller))
	if dispatch == nil {
		return nil
	}
	return dispatch.ResolveSameReceiverCall(caller, callee, st)
}

// resolveInterfaceDispatch attempts to resolve method calls through an interface.
// @intent provide best-effort resolution for polymorphic calls by checking implementations.
func resolveInterfaceDispatch(caller *model.Node, callee string, st *resolveState) *model.Node {
	dispatch := dispatchForLanguage(callerLanguage(caller))
	if dispatch == nil {
		return nil
	}
	return dispatch.ResolveInterfaceDispatch(caller, callee, st)
}

// implementsEndpoints parses an implementation edge fingerprint to extract endpoints.
// @intent retrieve concrete and interface symbol names from the persisted fingerprint.
func implementsEndpoints(edge model.Edge) (string, string, bool) {
	prefix := "implements:" + edge.FilePath + ":"
	if !strings.HasPrefix(edge.Fingerprint, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(edge.Fingerprint, prefix)
	idx := strings.Index(rest, ":")
	if idx < 0 {
		return "", "", false
	}
	impl := rest[:idx]
	iface := rest[idx+1:]
	return impl, iface, impl != "" && iface != ""
}

// packageForFile identifies the package name for a given file based on its nodes.
// @intent determine the logical package context for a physical source file.
func packageForFile(nodes []model.Node) string {
	for _, n := range nodes {
		if n.Kind == model.NodeKindFile {
			continue
		}
		if pkg := packagePrefix(n); pkg != "" {
			return pkg
		}
	}
	return ""
}

// isExportedName checks if a symbol name starts with an uppercase letter.
// @intent apply Go visibility rules during symbol resolution.
func isExportedName(name string) bool {
	if name == "" {
		return false
	}
	r := rune(name[0])
	return r >= 'A' && r <= 'Z'
}

// enclosingCallable finds the function or test spanning a specific line number.
// @intent identify the source symbol (caller) for a relationship originating on a line.
func enclosingCallable(nodes []model.Node, line int) *model.Node {
	if line <= 0 {
		return nil
	}
	var best *model.Node
	for i := range nodes {
		n := nodes[i]
		if n.Kind != model.NodeKindFunction && n.Kind != model.NodeKindTest {
			continue
		}
		if n.StartLine > line || n.EndLine < line {
			continue
		}
		if best == nil || span(n) < span(*best) {
			best = &nodes[i]
		}
	}
	return best
}

// span calculates the line count of a node's body.
// @intent assist in finding the narrowest enclosing symbol for a given line.
func span(n model.Node) int {
	return n.EndLine - n.StartLine
}

// callCallee parses a call edge fingerprint to extract the target name.
// @intent retrieve the callee symbol name from the persisted fingerprint.
func callCallee(edge model.Edge) (string, bool) {
	prefix := "calls:" + edge.FilePath + ":"
	if !strings.HasPrefix(edge.Fingerprint, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(edge.Fingerprint, prefix)
	idx := strings.LastIndex(rest, ":")
	if idx < 0 {
		return "", false
	}
	if _, err := strconv.Atoi(rest[idx+1:]); err != nil {
		return "", false
	}
	return rest[:idx], true
}

// containsTarget parses a containment edge fingerprint to extract the target QN.
// @intent retrieve the target symbol name from the persisted fingerprint.
func containsTarget(edge model.Edge) (string, bool) {
	prefix := "contains:" + edge.FilePath + ":"
	if !strings.HasPrefix(edge.Fingerprint, prefix) {
		return "", false
	}
	qn := strings.TrimPrefix(edge.Fingerprint, prefix)
	return qn, qn != ""
}

// packagePrefix extracts the package or module prefix from a node's QN.
// @intent determine the logical namespace for a symbol.
func packagePrefix(node model.Node) string {
	if dispatch := dispatchForLanguage(node.Language); dispatch != nil {
		return dispatch.PackagePrefix(node)
	}
	suffix := "." + node.Name
	if strings.HasSuffix(node.QualifiedName, suffix) {
		return strings.TrimSuffix(node.QualifiedName, suffix)
	}
	return ""
}

// callerLanguage safely returns the caller language when available.
// @intent avoid repeated nil checks before dispatch strategy lookup.
func callerLanguage(caller *model.Node) string {
	if caller == nil {
		return ""
	}
	return caller.Language
}

// lastSegment returns the final part of a dot-separated QN.
// @intent extract the bare symbol name from a fully qualified name.
func lastSegment(name string) string {
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

// uniqueCallable extracts a single callable node (function/test) from a list.
// @intent return nil if multiple ambiguous functions match the criteria.
func uniqueCallable(nodes []model.Node) *model.Node {
	var found *model.Node
	seen := make(map[uint]bool)
	for i := range nodes {
		if nodes[i].Kind != model.NodeKindFunction && nodes[i].Kind != model.NodeKindTest {
			continue
		}
		if nodes[i].ID != 0 && seen[nodes[i].ID] {
			continue
		}
		if nodes[i].ID != 0 {
			seen[nodes[i].ID] = true
		}
		if found != nil {
			return nil
		}
		found = &nodes[i]
	}
	return found
}

// uniqueTypeNode extracts a single type node (class/type) from a list.
// @intent return nil if multiple ambiguous types match the criteria.
func uniqueTypeNode(nodes []model.Node) *model.Node {
	var found *model.Node
	seen := make(map[uint]bool)
	for i := range nodes {
		if nodes[i].Kind != model.NodeKindType && nodes[i].Kind != model.NodeKindClass {
			continue
		}
		if nodes[i].ID != 0 && seen[nodes[i].ID] {
			continue
		}
		if nodes[i].ID != 0 {
			seen[nodes[i].ID] = true
		}
		if found != nil {
			return nil
		}
		found = &nodes[i]
	}
	return found
}

// uniqueTypeNodeByName finds a unique type node matching a specific bare name.
// @intent filter nodes by name before applying uniqueness check.
func uniqueTypeNodeByName(nodes []model.Node, name string) *model.Node {
	var candidates []model.Node
	for _, n := range nodes {
		if n.Name == name {
			candidates = append(candidates, n)
		}
	}
	return uniqueTypeNode(candidates)
}

// uniqueNode extracts a single node of any kind from a list.
// @intent return nil if the input list contains multiple distinct nodes.
func uniqueNode(nodes []model.Node) *model.Node {
	var found *model.Node
	seen := make(map[uint]bool)
	for i := range nodes {
		if nodes[i].ID != 0 && seen[nodes[i].ID] {
			continue
		}
		if nodes[i].ID != 0 {
			seen[nodes[i].ID] = true
		}
		if found != nil {
			return nil
		}
		found = &nodes[i]
	}
	return found
}

// appendUniqueNode adds a node to a slice only if it's not already present.
// @intent prevent duplicate nodes in resolution result sets.
func appendUniqueNode(nodes []model.Node, node model.Node) []model.Node {
	for _, n := range nodes {
		if n.ID != 0 && n.ID == node.ID {
			return nodes
		}
		if n.ID == 0 && n.QualifiedName == node.QualifiedName {
			return nodes
		}
	}
	return append(nodes, node)
}

// uniqueNodes returns a new slice containing only unique nodes from the input.
// @intent deduplicate result sets before further processing or resolution.
func uniqueNodes(nodes []model.Node) []model.Node {
	var out []model.Node
	for _, n := range nodes {
		out = appendUniqueNode(out, n)
	}
	return out
}
