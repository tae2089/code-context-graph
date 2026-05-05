// @index Language-specific dispatch hooks used by call edge resolution.
package edgeresolve

import "github.com/tae2089/code-context-graph/internal/model"

// languageDispatch defines optional language-specific call resolution hooks.
// @intent keep Resolve generic while allowing languages to customize dispatch semantics.
// @domainRule only languages with proven dispatch-specific behavior should implement this contract.
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
// @domainRule languages absent from this registry must fall back to generic resolver behavior.
var languageDispatchRegistry = map[string]languageDispatch{
	"go":         goLanguageDispatch{},
	"rust":       rustLanguageDispatch{},
	"typescript": explicitOwnerLanguageDispatch{language: "typescript"},
	"java":       explicitOwnerLanguageDispatch{language: "java"},
	"kotlin":     explicitOwnerLanguageDispatch{language: "kotlin"},
}

// dispatchForLanguage returns the registered dispatch strategy for a language.
// @intent centralize language-specific resolver lookup behind one internal seam.
// @ensures returns nil when no specialized dispatch strategy is registered for the language.
func dispatchForLanguage(language string) languageDispatch {
	if language == "" {
		return nil
	}
	return languageDispatchRegistry[language]
}
