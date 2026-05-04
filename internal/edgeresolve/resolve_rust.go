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
	trait, _, _, ok := rustTraitMethodSelector(callee)
	if !ok {
		return nil
	}
	return []string{trait}
}

// EnsureDispatchTargets returns Rust-specific candidate qualified names to prefetch.
// @intent preload possible impl methods for trait method dispatch before resolution.
func (rustLanguageDispatch) EnsureDispatchTargets(caller *model.Node, callee string, st *resolveState) []string {
	_ = caller
	trait, method, concrete, ok := rustTraitMethodSelector(callee)
	if !ok {
		return nil
	}
	var names []string
	for _, impl := range rustExactImplementers(st, trait, concrete) {
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
	trait, method, concrete, ok := rustTraitMethodSelector(callee)
	if !ok {
		return nil
	}
	impls := rustExactImplementers(st, trait, concrete)
	if len(impls) != 1 {
		return nil
	}
	var candidates []model.Node
	for _, impl := range impls {
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

func rustTraitMethodSelector(callee string) (trait string, method string, concrete string, ok bool) {
	callee = strings.TrimSpace(callee)
	if concrete, trait, method, ok = rustUFCSTraitMethodSelector(callee); ok {
		return trait, method, concrete, true
	}
	trait, method, ok = rustQualifiedTraitMethodSelector(callee)
	if !ok {
		return "", "", "", false
	}
	return trait, method, "", true
}

func rustQualifiedTraitMethodSelector(callee string) (string, string, bool) {
	parts := strings.Split(callee, "::")
	if len(parts) < 2 {
		return "", "", false
	}
	trait := strings.TrimSpace(strings.Join(parts[:len(parts)-1], "::"))
	method := strings.TrimSpace(parts[len(parts)-1])
	if trait == "" || method == "" {
		return "", "", false
	}
	return trait, method, true
}

func rustUFCSTraitMethodSelector(callee string) (concrete string, trait string, method string, ok bool) {
	callee = strings.TrimSpace(callee)
	if !strings.HasPrefix(callee, "<") {
		return "", "", "", false
	}
	close := rustMatchingAngle(callee, 0)
	if close < 0 || close+2 >= len(callee) || callee[close+1] != ':' || callee[close+2] != ':' {
		return "", "", "", false
	}
	method = strings.TrimSpace(callee[close+3:])
	inner := strings.TrimSpace(callee[1:close])
	idx := rustTopLevelAsIndex(inner)
	if idx < 0 {
		return "", "", "", false
	}
	concrete = strings.TrimSpace(inner[:idx])
	trait = strings.TrimSpace(inner[idx+len(" as "):])
	if concrete == "" || trait == "" || method == "" {
		return "", "", "", false
	}
	return concrete, trait, method, true
}

func rustExactImplementers(st *resolveState, trait string, concrete string) []model.Node {
	impls := uniqueNodes(st.implementsBy[trait])
	if concrete == "" {
		return impls
	}
	var filtered []model.Node
	for _, impl := range impls {
		if impl.QualifiedName == concrete {
			filtered = append(filtered, impl)
		}
	}
	return filtered
}

func rustMatchingAngle(raw string, open int) int {
	depth := 0
	for i := open; i < len(raw); i++ {
		switch raw[i] {
		case '<':
			depth++
		case '>':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func rustTopLevelAsIndex(raw string) int {
	depthAngle := 0
	depthParen := 0
	depthBracket := 0
	for i := 0; i+4 <= len(raw); i++ {
		switch raw[i] {
		case '<':
			depthAngle++
		case '>':
			if depthAngle > 0 {
				depthAngle--
			}
		case '(':
			depthParen++
		case ')':
			if depthParen > 0 {
				depthParen--
			}
		case '[':
			depthBracket++
		case ']':
			if depthBracket > 0 {
				depthBracket--
			}
		}
		if depthAngle == 0 && depthParen == 0 && depthBracket == 0 && strings.HasPrefix(raw[i:], " as ") {
			return i
		}
	}
	return -1
}
