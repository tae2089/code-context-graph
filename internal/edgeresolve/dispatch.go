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

var languageDispatchRegistry = map[string]languageDispatch{
	"go": goLanguageDispatch{},
}

// dispatchForLanguage returns the registered dispatch strategy for a language.
// @intent centralize language-specific resolver lookup behind one internal seam.
func dispatchForLanguage(language string) languageDispatch {
	if language == "" {
		return nil
	}
	return languageDispatchRegistry[language]
}
