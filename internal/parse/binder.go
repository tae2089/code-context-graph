package parse

import (
	"github.com/imtaebin/code-context-graph/internal/annotation"
	"github.com/imtaebin/code-context-graph/internal/model"
)

type CommentBlock struct {
	StartLine int
	EndLine   int
	Text      string
}

type Binding struct {
	Node       model.Node
	Annotation *model.Annotation
}

type Binder struct {
	normalizer *annotation.Normalizer
	parser     *annotation.Parser
}

func NewBinder() *Binder {
	return &Binder{
		normalizer: annotation.NewNormalizer(),
		parser:     annotation.NewParser(),
	}
}

const maxGap = 1

func (b *Binder) Bind(comments []CommentBlock, nodes []model.Node, language string) []Binding {
	var bindings []Binding

	for _, node := range nodes {
		// File 노드: package 선언 직전(첫 번째) 주석을 바인딩
		if node.Kind == model.NodeKindFile {
			if len(comments) > 0 {
				first := comments[0]
				normalized := b.normalizer.Normalize(first.Text, language)
				ann, err := b.parser.Parse(normalized)
				if err == nil && hasContent(ann) {
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
			ann, err := b.parser.Parse(normalized)
			if err != nil {
				continue
			}

			bindings = append(bindings, Binding{
				Node:       node,
				Annotation: ann,
			})
			break
		}
	}

	return bindings
}

func hasContent(ann *model.Annotation) bool {
	return ann.Summary != "" || ann.Context != "" || len(ann.Tags) > 0
}
