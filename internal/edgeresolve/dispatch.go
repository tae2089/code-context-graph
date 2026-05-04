package edgeresolve

import "github.com/tae2089/code-context-graph/internal/model"

// languageDispatch defines optional language-specific call resolution hooks.
// @intent keep Resolve generic while allowing languages to customize dispatch semantics.
type languageDispatch interface {
	Language() string
	CollectQualifiedCallCandidates(caller model.Node, callee string) []string
	EnsureDispatchTargets(caller *model.Node, callee string, st *resolveState) []string
	ResolveSameReceiverCall(caller *model.Node, callee string, st *resolveState) *model.Node
	ResolveInterfaceDispatch(caller *model.Node, callee string, st *resolveState) *model.Node
	PackagePrefix(node model.Node) string
}

// languageDispatchRegistry only includes languages with proven call-dispatch resolution support.
// @intent distinguish optional dynamic/interface call resolution from generic hierarchy-edge parsing available elsewhere.
// 현재는 Go와 Rust만 언어별 dispatch 확장 경로를 사용하고, 다른 언어는 generic resolver에 남겨둔다.
var languageDispatchRegistry = map[string]languageDispatch{
	"go":   goLanguageDispatch{},
	"rust": rustLanguageDispatch{},
}

// dispatchForLanguage returns the registered dispatch strategy for a language.
// @intent centralize language-specific resolver lookup behind one internal seam.
func dispatchForLanguage(language string) languageDispatch {
	if language == "" {
		return nil
	}
	return languageDispatchRegistry[language]
}
