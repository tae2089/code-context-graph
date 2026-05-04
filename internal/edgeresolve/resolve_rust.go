package edgeresolve

import (
	"strings"

	"github.com/tae2089/code-context-graph/internal/model"
)

// rustLanguageDispatch encapsulates Rust-specific trait dispatch behavior.
// @intent extend interface-like dispatch beyond Go without broadening the generic resolver flow.
type rustLanguageDispatch struct{}

// Language identifies the language handled by this dispatch strategy.
// @intent support registry-based lookup for language-specific resolution.
func (rustLanguageDispatch) Language() string {
	return "rust"
}

// CollectQualifiedCallCandidates returns Rust-specific prefetch candidates derivable without state.
// @intent preload trait nodes that can participate in trait-method dispatch.
func (rustLanguageDispatch) CollectQualifiedCallCandidates(caller model.Node, callee string) []string {
	trait, _, ok := rustTraitMethodSelector(callee)
	if !ok {
		return nil
	}
	return []string{trait}
}

// EnsureDispatchTargets returns Rust-specific candidate qualified names to prefetch.
// @intent preload possible impl methods for trait method dispatch before resolution.
func (rustLanguageDispatch) EnsureDispatchTargets(caller *model.Node, callee string, st *resolveState) []string {
	_ = caller
	trait, method, ok := rustTraitMethodSelector(callee)
	if !ok {
		return nil
	}
	var names []string
	for _, impl := range st.implementsBy[trait] {
		names = append(names, impl.QualifiedName+"."+method)
	}
	return names
}

// ResolveSameReceiverCall does not add Rust-specific same-receiver handling yet.
// @intent rely on the generic same-file fallback until Rust receiver-aware rewrites are needed.
func (rustLanguageDispatch) ResolveSameReceiverCall(caller *model.Node, callee string, st *resolveState) *model.Node {
	_ = caller
	_ = callee
	_ = st
	return nil
}

// ResolveInterfaceDispatch attempts to resolve Rust trait-style calls through known implementers.
// @intent support non-Go trait dispatch when call rewriting produces Trait::method selectors.
func (rustLanguageDispatch) ResolveInterfaceDispatch(caller *model.Node, callee string, st *resolveState) *model.Node {
	_ = caller
	trait, method, ok := rustTraitMethodSelector(callee)
	if !ok {
		return nil
	}
	var candidates []model.Node
	for _, impl := range uniqueNodes(st.implementsBy[trait]) {
		if target := uniqueCallable(st.qnIndex[impl.QualifiedName+"."+method]); target != nil {
			candidates = append(candidates, *target)
		}
	}
	return uniqueCallable(candidates)
}

// PackagePrefix extracts the Rust module/type prefix from a qualified name.
// @intent keep Rust naming rules localized even though current resolver use is minimal.
func (rustLanguageDispatch) PackagePrefix(node model.Node) string {
	_ = node
	return ""
}

func rustTraitMethodSelector(callee string) (string, string, bool) {
	parts := strings.Split(callee, "::")
	if len(parts) != 2 {
		return "", "", false
	}
	trait := strings.TrimSpace(parts[len(parts)-2])
	method := strings.TrimSpace(parts[len(parts)-1])
	if trait == "" || method == "" {
		return "", "", false
	}
	return trait, method, true
}
