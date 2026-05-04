package edgeresolve

import (
	"strings"

	"github.com/tae2089/code-context-graph/internal/model"
)

// explicitOwnerLanguageDispatch resolves calls only when the callee encodes a fully qualified owner type.
// @intent add conservative interface-like dispatch for languages that lack receiver-type inference.
type explicitOwnerLanguageDispatch struct {
	language string
}

func (d explicitOwnerLanguageDispatch) Language() string {
	return d.language
}

func (d explicitOwnerLanguageDispatch) CollectQualifiedCallCandidates(caller model.Node, callee string) []string {
	owner, _, ok := explicitOwnerMethodSelector(callee)
	if !ok {
		return nil
	}
	if strings.Contains(owner, ".") {
		return []string{owner}
	}
	return explicitOwnerShortNameCandidates(caller, owner)
}

func (d explicitOwnerLanguageDispatch) EnsureDispatchTargets(caller *model.Node, callee string, st *resolveState) []string {
	owner, method, ok := explicitOwnerMethodSelector(callee)
	if !ok {
		return nil
	}
	ownerNode := explicitOwnerTarget(st, owner)
	if ownerNode == nil {
		return nil
	}
	names := []string{ownerNode.QualifiedName + "." + method}
	impls := explicitOwnerImplementers(st, owner)
	if len(impls) == 1 {
		names = append(names, impls[0].QualifiedName+"."+method)
	}
	return names
}

func (d explicitOwnerLanguageDispatch) ResolveSameReceiverCall(caller *model.Node, callee string, st *resolveState) *model.Node {
	_ = caller
	_ = callee
	_ = st
	return nil
}

func (d explicitOwnerLanguageDispatch) ResolveInterfaceDispatch(caller *model.Node, callee string, st *resolveState) *model.Node {
	_ = caller
	owner, method, ok := explicitOwnerMethodSelector(callee)
	if !ok {
		return nil
	}
	impls := explicitOwnerImplementers(st, owner)
	if len(impls) == 1 {
		if target := uniqueCallable(st.qnIndex[impls[0].QualifiedName+"."+method]); target != nil {
			return target
		}
	}
	ownerNode := explicitOwnerTarget(st, owner)
	if ownerNode == nil {
		return nil
	}
	return uniqueCallable(st.qnIndex[ownerNode.QualifiedName+"."+method])
}

func (d explicitOwnerLanguageDispatch) PackagePrefix(node model.Node) string {
	suffix := "." + node.Name
	if strings.HasSuffix(node.QualifiedName, suffix) {
		return strings.TrimSuffix(node.QualifiedName, suffix)
	}
	return ""
}

func explicitOwnerMethodSelector(callee string) (string, string, bool) {
	idx := strings.LastIndex(callee, ".")
	if idx <= 0 || idx == len(callee)-1 {
		return "", "", false
	}
	owner := callee[:idx]
	method := callee[idx+1:]
	if owner == "" || method == "" {
		return "", "", false
	}
	if !strings.Contains(owner, ".") && !isExportedName(owner) {
		return "", "", false
	}
	return owner, method, true
}

func explicitOwnerImplementers(st *resolveState, owner string) []model.Node {
	if st == nil {
		return nil
	}
	target := explicitOwnerTarget(st, owner)
	if target == nil {
		return nil
	}
	return uniqueNodes(st.implementsBy[target.QualifiedName])
}

func explicitOwnerTarget(st *resolveState, owner string) *model.Node {
	if st == nil || owner == "" {
		return nil
	}
	if strings.Contains(owner, ".") {
		return uniqueTypeNode(st.qnIndex[owner])
	}
	var candidates []model.Node
	for _, nodes := range st.qnIndex {
		for _, node := range nodes {
			if (node.Kind == model.NodeKindType || node.Kind == model.NodeKindClass) && node.Name == owner {
				candidates = append(candidates, node)
			}
		}
	}
	return uniqueTypeNode(candidates)
}

func explicitOwnerShortNameCandidates(caller model.Node, owner string) []string {
	if owner == "" {
		return nil
	}
	base := packagePrefix(caller)
	if base == "" {
		return nil
	}
	parts := strings.Split(base, ".")
	seen := make(map[string]bool)
	var candidates []string
	for i := len(parts); i > 0; i-- {
		candidate := strings.Join(parts[:i], ".") + "." + owner
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		candidates = append(candidates, candidate)
	}
	return candidates
}
