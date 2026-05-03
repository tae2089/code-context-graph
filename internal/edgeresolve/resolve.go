// @index Edge resolution helpers that attach parsed relationships to stored graph node IDs.
package edgeresolve

import (
	"context"
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

type edgeLookup interface {
	GetEdgesToNodes(ctx context.Context, nodeIDs []uint) ([]model.Edge, error)
}

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
		for qn, ns := range queried {
			st.qnIndex[qn] = append(st.qnIndex[qn], ns...)
			for _, n := range ns {
				st.nodeByID[n.ID] = n
			}
		}
	}

	out := append([]model.Edge(nil), edges...)
	for i := range out {
		switch out[i].Kind {
		case model.EdgeKindContains:
			resolveContains(&out[i], st)
		case model.EdgeKindImplements:
			resolveImplements(&out[i], st)
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

func flattenNodes(nodesByFile map[string][]model.Node) []model.Node {
	var nodes []model.Node
	for _, ns := range nodesByFile {
		nodes = append(nodes, ns...)
	}
	return nodes
}

func indexByQualifiedName(nodes []model.Node) map[string][]model.Node {
	index := make(map[string][]model.Node)
	for _, n := range nodes {
		index[n.QualifiedName] = append(index[n.QualifiedName], n)
	}
	return index
}

func indexByID(nodes []model.Node) map[uint]model.Node {
	index := make(map[uint]model.Node)
	for _, n := range nodes {
		if n.ID != 0 {
			index[n.ID] = n
		}
	}
	return index
}

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

func indexFileNodes(nodes []model.Node) map[string]model.Node {
	index := make(map[string]model.Node)
	for _, n := range nodes {
		if n.Kind == model.NodeKindFile {
			index[n.FilePath] = n
		}
	}
	return index
}

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
				if caller.Language == "go" {
					if iface, _, ok := interfaceMethodSelector(callee); ok && pkg != "" {
						addName(&names, seen, pkg+"."+iface)
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
		}
	}
	return names
}

func addName(names *[]string, seen map[string]bool, name string) {
	if name == "" || seen[name] {
		return
	}
	seen[name] = true
	*names = append(*names, name)
}

func addEndpointCandidates(names *[]string, seen map[string]bool, st *resolveState, filePath, endpoint string) {
	addName(names, seen, endpoint)
	if strings.Contains(endpoint, ".") {
		return
	}
	if pkg := packageForFile(st.nodesByFile[filePath]); pkg != "" {
		addName(names, seen, pkg+"."+endpoint)
	}
}

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
	if target := resolveGoInterfaceDispatch(caller, callee, st); target != nil {
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
		if caller == nil || caller.Language != "go" {
			continue
		}
		if receiver := receiverPrefix(*caller); receiver != "" {
			addName(&names, seen, receiver+"."+lastSegment(callee))
		}
		if iface, method, ok := interfaceMethodSelector(callee); ok {
			for _, impl := range st.goImplementersFor(caller, iface) {
				addName(&names, seen, impl.QualifiedName+"."+method)
			}
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

func (st *resolveState) addNodes(nodes []model.Node) {
	for _, n := range nodes {
		st.qnIndex[n.QualifiedName] = append(st.qnIndex[n.QualifiedName], n)
		if n.ID != 0 {
			st.nodeByID[n.ID] = n
		}
		if st.nodesByFile[n.FilePath] == nil {
			st.nodesByFile[n.FilePath] = []model.Node{n}
		}
	}
}

func (st *resolveState) addImplementer(iface model.Node, concrete model.Node) {
	st.implementsBy[iface.QualifiedName] = appendUniqueNode(st.implementsBy[iface.QualifiedName], concrete)
	st.implementsBy[iface.Name] = appendUniqueNode(st.implementsBy[iface.Name], concrete)
}

func (st *resolveState) goImplementersFor(caller *model.Node, iface string) []model.Node {
	if caller == nil || caller.Language != "go" {
		return nil
	}
	var impls []model.Node
	if pkg := packagePrefix(*caller); pkg != "" {
		impls = append(impls, st.implementsBy[pkg+"."+iface]...)
	}
	impls = append(impls, st.implementsBy[iface]...)
	return uniqueNodes(impls)
}

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

func resolveSameReceiverCall(caller *model.Node, callee string, st *resolveState) *model.Node {
	if caller == nil || caller.Language != "go" {
		return nil
	}
	receiver := receiverPrefix(*caller)
	if receiver == "" {
		return nil
	}
	return uniqueCallable(st.qnIndex[receiver+"."+lastSegment(callee)])
}

func resolveGoInterfaceDispatch(caller *model.Node, callee string, st *resolveState) *model.Node {
	if caller == nil || caller.Language != "go" {
		return nil
	}
	iface, method, ok := interfaceMethodSelector(callee)
	if !ok {
		return nil
	}
	var candidates []model.Node
	for _, impl := range st.goImplementersFor(caller, iface) {
		if target := uniqueCallable(st.qnIndex[impl.QualifiedName+"."+method]); target != nil {
			candidates = append(candidates, *target)
		}
	}
	return uniqueCallable(candidates)
}

func implementsEndpoints(edge model.Edge) (string, string, bool) {
	prefix := "implements:" + edge.FilePath + ":"
	if !strings.HasPrefix(edge.Fingerprint, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(edge.Fingerprint, prefix)
	idx := strings.LastIndex(rest, ":")
	if idx < 0 {
		return "", "", false
	}
	impl := rest[:idx]
	iface := rest[idx+1:]
	return impl, iface, impl != "" && iface != ""
}

func interfaceMethodSelector(callee string) (string, string, bool) {
	parts := strings.Split(callee, ".")
	if len(parts) < 2 {
		return "", "", false
	}
	iface := parts[len(parts)-2]
	method := parts[len(parts)-1]
	if iface == "" || method == "" {
		return "", "", false
	}
	if !isExportedName(iface) || !isExportedName(method) {
		return "", "", false
	}
	return iface, method, true
}

func receiverPrefix(node model.Node) string {
	if node.Language != "go" {
		return ""
	}
	parts := strings.Split(node.QualifiedName, ".")
	if len(parts) < 3 {
		return ""
	}
	return strings.Join(parts[:len(parts)-1], ".")
}

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

func isExportedName(name string) bool {
	if name == "" {
		return false
	}
	r := rune(name[0])
	return r >= 'A' && r <= 'Z'
}

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

func span(n model.Node) int {
	return n.EndLine - n.StartLine
}

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

func containsTarget(edge model.Edge) (string, bool) {
	prefix := "contains:" + edge.FilePath + ":"
	if !strings.HasPrefix(edge.Fingerprint, prefix) {
		return "", false
	}
	qn := strings.TrimPrefix(edge.Fingerprint, prefix)
	return qn, qn != ""
}

func packagePrefix(node model.Node) string {
	if node.Language == "go" {
		if idx := strings.Index(node.QualifiedName, "."); idx > 0 {
			return node.QualifiedName[:idx]
		}
		return ""
	}
	suffix := "." + node.Name
	if strings.HasSuffix(node.QualifiedName, suffix) {
		return strings.TrimSuffix(node.QualifiedName, suffix)
	}
	return ""
}

func lastSegment(name string) string {
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

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

func uniqueTypeNodeByName(nodes []model.Node, name string) *model.Node {
	var candidates []model.Node
	for _, n := range nodes {
		if n.Name == name {
			candidates = append(candidates, n)
		}
	}
	return uniqueTypeNode(candidates)
}

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

func uniqueNodes(nodes []model.Node) []model.Node {
	var out []model.Node
	for _, n := range nodes {
		out = appendUniqueNode(out, n)
	}
	return out
}
