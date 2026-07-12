package resolve

import (
	"strings"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// explicitOwnerLanguageDispatch resolves calls only when the callee encodes a fully qualified owner type.
// @intent add conservative interface-like dispatch for languages that lack receiver-type inference.
type explicitOwnerLanguageDispatch struct {
	language string
}

// Language identifies the language handled by this dispatch strategy.
// @intent support registry-based lookup for explicit-owner language dispatch.
func (d explicitOwnerLanguageDispatch) Language() string {
	return d.language
}

// CollectQualifiedCallCandidates returns explicit-owner candidates derivable without resolver state.
// @intent preload fully qualified owner types before polymorphic dispatch resolution runs.
func (d explicitOwnerLanguageDispatch) CollectQualifiedCallCandidates(caller graph.Node, callee string) []string {
	owner, _, ok := explicitOwnerMethodSelector(callee)
	if !ok {
		return nil
	}
	if strings.Contains(owner, ".") {
		return []string{owner}
	}
	return explicitOwnerShortNameCandidates(caller, owner)
}

// EnsureDispatchTargets returns explicit-owner dispatch targets to prefetch for later resolution.
// @intent preload owner and unique implementer methods when a conservative explicit-owner selector is available.
func (d explicitOwnerLanguageDispatch) EnsureDispatchTargets(caller *graph.Node, callee string, st *resolveState) []string {
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

// ResolveSameReceiverCall leaves same-receiver handling to generic resolution for explicit-owner languages.
// @intent avoid inventing receiver inference when the call only proves an owner-qualified selector.
func (d explicitOwnerLanguageDispatch) ResolveSameReceiverCall(caller *graph.Node, callee string, st *resolveState) *graph.Node {
	_ = caller
	_ = callee
	_ = st
	return nil
}

// ResolveInterfaceDispatch resolves explicit-owner selectors through a unique implementer when possible.
// @intent preserve conservative interface-style dispatch for JVM/TypeScript selectors without broad receiver inference.
func (d explicitOwnerLanguageDispatch) ResolveInterfaceDispatch(caller *graph.Node, callee string, st *resolveState) *graph.Node {
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

// PackagePrefix extracts the namespace-like prefix from explicit-owner qualified names.
// @intent reuse existing qualified-name prefixes when expanding short owner candidates.
func (d explicitOwnerLanguageDispatch) PackagePrefix(node graph.Node) string {
	suffix := "." + node.Name
	if strings.HasSuffix(node.QualifiedName, suffix) {
		return strings.TrimSuffix(node.QualifiedName, suffix)
	}
	return ""
}

// explicitOwnerMethodSelector splits a callee into owner and method segments when it is safe to do so.
// @intent gate explicit-owner dispatch behind syntactic selectors that look like type-owned method calls.
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

// explicitOwnerImplementers returns known concrete implementers for an explicit owner type.
// @intent reuse implements edges to prefer concrete dispatch targets over abstract owner nodes when unique.
func explicitOwnerImplementers(st *resolveState, owner string) []graph.Node {
	if st == nil {
		return nil
	}
	target := explicitOwnerTarget(st, owner)
	if target == nil {
		return nil
	}
	return uniqueNodes(st.implementsBy[target.QualifiedName])
}

// explicitOwnerTarget resolves an explicit owner string to a unique type or class node.
// @intent normalize short and fully qualified owner names into one dispatch anchor before method lookup.
func explicitOwnerTarget(st *resolveState, owner string) *graph.Node {
	if st == nil || owner == "" {
		return nil
	}
	if strings.Contains(owner, ".") {
		return uniqueTypeNode(st.qnIndex[owner])
	}
	var candidates []graph.Node
	for _, nodes := range st.qnIndex {
		for _, node := range nodes {
			if (node.Kind == graph.NodeKindType || node.Kind == graph.NodeKindClass) && node.Name == owner {
				candidates = append(candidates, node)
			}
		}
	}
	return uniqueTypeNode(candidates)
}

// explicitOwnerShortNameCandidates expands a short owner name against the caller package prefix.
// @intent preserve short-owner support without searching unrelated packages outside the caller namespace.
func explicitOwnerShortNameCandidates(caller graph.Node, owner string) []string {
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
