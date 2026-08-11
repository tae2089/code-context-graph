// Package offsetrule holds one rule about a page position and the one sentence
// every surface turns a bad position away with.
//
// It is a leaf on purpose. Change analysis pages its results too, so it needs
// the same rule search does; reaching the rule through the search service
// package would have pulled the whole search pipeline — reranking, evidence,
// intent — into change analysis to reuse four lines.
package offsetrule

import "github.com/tae2089/trace"

// MustNotBeNegative is the sentence every paged surface turns a negative page
// position away with. It is exported because the MCP boundary cannot pass the
// error through unchanged — it has to restate the sentence as a tool result the
// caller can read — and restating it from a copied literal is exactly how two
// surfaces would drift apart.
const MustNotBeNegative = "offset must not be negative"

// Validate turns away the one page position no answer exists for, in the words
// every surface says it in.
//
// MCP and the CLI both call it before a search runs, and change analysis calls
// it for its own page. The sentence lives here rather than at each entry point
// so they cannot end up describing the same rejected request differently — a
// reader who learns what the tool says has also learned what the flag says.
//
// @intent keep every paged entry point agreeing about which requests are askable.
func Validate(offset int) error {
	if offset < 0 {
		return trace.New(MustNotBeNegative)
	}
	return nil
}
