package parse

import (
	"github.com/tae2089/code-context-graph/internal/annotation"
	"github.com/tae2089/code-context-graph/internal/model"
)

// CommentBlock records a contiguous source comment range for later binding.
// @intent preserve comment text with source line bounds during parse-to-annotation binding
type CommentBlock struct {
	StartLine      int
	EndLine        int
	Text           string
	IsDocstring    bool // Python docstring 여부 (true이면 OwnerStartLine으로 바인딩)
	OwnerStartLine int  // docstring이 귀속된 심볼의 StartLine (모듈 docstring은 0)
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
		// File 노드: 모듈 docstring 또는 첫 번째 leading comment 바인딩
		if node.Kind == model.NodeKindFile {
			for _, cb := range comments {
				// Python 모듈 docstring: IsDocstring=true && OwnerStartLine==0
				if cb.IsDocstring && cb.OwnerStartLine == 0 {
					normalized := b.normalizer.Normalize(cb.Text, language)
					ann, _ := b.parser.Parse(normalized)
					if hasContent(ann) {
						bindings = append(bindings, Binding{Node: node, Annotation: ann})
					}
					break
				}
				// 비-docstring 첫 번째 comment: 기존 동작 (첫 leading comment만)
				if !cb.IsDocstring {
					normalized := b.normalizer.Normalize(cb.Text, language)
					ann, _ := b.parser.Parse(normalized)
					if hasContent(ann) {
						bindings = append(bindings, Binding{Node: node, Annotation: ann})
					}
					break
				}
			}
			continue
		}

		// 일반 심볼 (함수/클래스/메서드 등)
		for _, comment := range comments {
			// Python docstring: OwnerStartLine 일치로 바인딩
			if comment.IsDocstring {
				if comment.OwnerStartLine != node.StartLine {
					continue
				}
				normalized := b.normalizer.Normalize(comment.Text, language)
				ann, _ := b.parser.Parse(normalized)
				bindings = append(bindings, Binding{Node: node, Annotation: ann})
				break
			}
			// 일반 comment: gap 기반 바인딩
			gap := node.StartLine - comment.EndLine
			if gap < 1 || gap > maxGap {
				continue
			}
			normalized := b.normalizer.Normalize(comment.Text, language)
			ann, _ := b.parser.Parse(normalized)
			bindings = append(bindings, Binding{Node: node, Annotation: ann})
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
