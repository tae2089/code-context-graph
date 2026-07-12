package resolve

import (
	"strings"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// goLanguageDispatch encapsulates Go-specific call dispatch behavior.
// @intent isolate Go interface and receiver dispatch from the generic resolver flow.
type goLanguageDispatch struct{}

// Language identifies the language handled by this dispatch strategy.
// @intent support registry-based lookup for language-specific resolution.
func (goLanguageDispatch) Language() string {
	return "go"
}

// CollectQualifiedCallCandidates returns Go-specific prefetch candidates derivable without state.
// @intent preserve Go interface candidate prefetch in the generic batch lookup stage.
func (goLanguageDispatch) CollectQualifiedCallCandidates(caller graph.Node, callee string) []string {
	pkg := packagePrefix(caller)
	if pkg == "" {
		return nil
	}
	if iface, _, ok := interfaceMethodSelector(callee); ok {
		return []string{pkg + "." + iface}
	}
	return nil
}

// EnsureDispatchTargets returns Go-specific candidate qualified names to prefetch.
// @intent preload potential interface implementer methods before call resolution.
func (goLanguageDispatch) EnsureDispatchTargets(caller *graph.Node, callee string, st *resolveState) []string {
	if caller == nil {
		return nil
	}
	var names []string
	if receiver := receiverPrefix(*caller); receiver != "" {
		names = append(names, receiver+"."+lastSegment(callee))
	}
	if iface, method, ok := interfaceMethodSelector(callee); ok {
		for _, impl := range st.goImplementersFor(caller, iface) {
			names = append(names, impl.QualifiedName+"."+method)
		}
	}
	return names
}

// ResolveSameReceiverCall attempts to resolve method calls within the same Go receiver type.
// @intent preserve Go method-call resolution without hardcoding language checks in Resolve.
func (goLanguageDispatch) ResolveSameReceiverCall(caller *graph.Node, callee string, st *resolveState) *graph.Node {
	if caller == nil {
		return nil
	}
	receiver := receiverPrefix(*caller)
	if receiver == "" {
		return nil
	}
	return uniqueCallable(st.qnIndex[receiver+"."+lastSegment(callee)])
}

// ResolveInterfaceDispatch attempts to resolve Go calls through interfaces.
// @intent preserve best-effort Go polymorphic dispatch behind the language seam.
func (goLanguageDispatch) ResolveInterfaceDispatch(caller *graph.Node, callee string, st *resolveState) *graph.Node {
	if caller == nil {
		return nil
	}
	iface, method, ok := interfaceMethodSelector(callee)
	if !ok {
		return nil
	}
	callerPkg := packagePrefix(*caller)
	requireSamePkg := !isExportedName(iface) || !isExportedName(method)
	if requireSamePkg && callerPkg == "" {
		return nil
	}
	var candidates []graph.Node
	for _, impl := range st.goImplementersFor(caller, iface) {
		if requireSamePkg && packagePrefix(impl) != callerPkg {
			continue
		}
		if target := uniqueCallable(st.qnIndex[impl.QualifiedName+"."+method]); target != nil {
			candidates = append(candidates, *target)
		}
	}
	return uniqueCallable(candidates)
}

// PackagePrefix extracts the Go package prefix from a qualified name.
// @intent keep Go package naming rules in the Go dispatch strategy.
func (goLanguageDispatch) PackagePrefix(node graph.Node) string {
	if idx := strings.Index(node.QualifiedName, "."); idx > 0 {
		return node.QualifiedName[:idx]
	}
	return ""
}

// goImplementersFor finds known implementers of an interface for a given caller context.
// @intent support Go interface method dispatch by finding candidate concrete types.
func (st *resolveState) goImplementersFor(caller *graph.Node, iface string) []graph.Node {
	if caller == nil {
		return nil
	}
	var impls []graph.Node
	if pkg := packagePrefix(*caller); pkg != "" {
		impls = append(impls, st.implementsBy[pkg+"."+iface]...)
	}
	if isExportedName(iface) {
		impls = append(impls, st.implementsBy[iface]...)
	}
	return uniqueNodes(impls)
}

// interfaceMethodSelector parses a callee string into interface and method parts.
// @intent identify polymorphic call targets in Go selector expressions.
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
	return iface, method, true
}

// receiverPrefix extracts the receiver type QN from a method's QN.
// @intent identify the type a Go method belongs to.
func receiverPrefix(node graph.Node) string {
	parts := strings.Split(node.QualifiedName, ".")
	if len(parts) < 3 {
		return ""
	}
	return strings.Join(parts[:len(parts)-1], ".")
}
