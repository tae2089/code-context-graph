package parse

import (
	"github.com/tae2089/code-context-graph/internal/annotation"
	"github.com/tae2089/code-context-graph/internal/model"
)

// CommentBlock records a contiguous source comment range for later binding.
// @intent preserve comment text with source line bounds during parse-to-annotation binding
type CommentBlock struct {
	StartLine int
	EndLine   int
	Text      string
}

// Binding connects a parsed node with its resolved annotation payload.
// @intent represent the result of associating one comment block with one graph node
type Binding struct {
	Node       model.Node
	Annotation *model.Annotation
}

// Binder matches nearby comments to parsed graph nodes.
// @intent attach normalized and parsed annotations to nodes based on source proximity
type Binder struct {
	normalizer *annotation.Normalizer
	parser     *annotation.Parser
}

// NewBinder creates a Binder.
// @intent compose the normalizer and parser used during comment-to-node binding
func NewBinder() *Binder {
	return &Binder{
		normalizer: annotation.NewNormalizer(),
		parser:     annotation.NewParser(),
	}
}

const maxGap = 2

// Bind associates comment blocks with nodes when they appear immediately above declarations.
// @intent build node-to-annotation bindings from parsed comments and node positions
// @domainRule only comments within maxGap lines above a declaration are attached
// @ensures file nodes bind only the first leading comment block when present
// @see parse.hasContent
func (b *Binder) Bind(comments []CommentBlock, nodes []model.Node, language string) []Binding {
	var bindings []Binding

	for _, node := range nodes {
		// File 노드: package 선언 직전(첫 번째) 주석을 바인딩
		if node.Kind == model.NodeKindFile {
			if len(comments) > 0 {
				first := comments[0]
				normalized := b.normalizer.Normalize(first.Text, language)
				ann, _ := b.parser.Parse(normalized)
				if hasContent(ann) {
					bindings = append(bindings, Binding{
						Node:       node,
						Annotation: ann,
					})
				}
			}
			continue
		}

		for _, comment := range comments {
			gap := node.StartLine - comment.EndLine
			if gap < 1 || gap > maxGap {
				continue
			}

			normalized := b.normalizer.Normalize(comment.Text, language)
			ann, _ := b.parser.Parse(normalized)

			bindings = append(bindings, Binding{
				Node:       node,
				Annotation: ann,
			})
			break
		}
	}

	return bindings
}

// hasContent reports whether an annotation contains any indexable text or tags.
// @intent skip empty annotation payloads before they are bound to nodes
func hasContent(ann *model.Annotation) bool {
	return ann.Summary != "" || ann.Context != "" || len(ann.Tags) > 0
}
