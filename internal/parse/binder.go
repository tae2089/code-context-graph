package parse

import (
	"strings"

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

// defaultMaxGap is the fallback maximum gap when no config is provided.
// 3 accommodates up to two decorator/attribute lines (common in Python, Java, Rust)
// without creating false bindings for comments that are more than three lines above a symbol.
const defaultMaxGap = 3

// Binder matches nearby comments to parsed graph nodes.
// @intent attach normalized and parsed annotations to nodes based on source proximity
type Binder struct {
	normalizer *annotation.Normalizer
	parser     *annotation.Parser
	MaxGap     int
}

// NewBinder creates a Binder with the default max gap.
// @intent compose the normalizer and parser used during comment-to-node binding
func NewBinder() *Binder {
	return NewBinderFromConfig(defaultMaxGap)
}

// NewBinderFromConfig creates a Binder with a caller-supplied max gap value.
// @intent allow per-project binder configuration via .ccg.yaml
// @param maxGap maximum line gap between comment end and declaration start
func NewBinderFromConfig(maxGap int) *Binder {
	if maxGap <= 0 {
		maxGap = defaultMaxGap
	}
	return &Binder{
		normalizer: annotation.NewNormalizer(),
		parser:     annotation.NewParser(),
		MaxGap:     maxGap,
	}
}

// Bind associates comment blocks with nodes when they appear immediately above declarations.
// @intent build node-to-annotation bindings from parsed comments and node positions
// @domainRule gap=1 always binds; gap>1 binds only if all lines between are blank (Look-Between)
// @ensures file nodes bind only the first leading comment block when present
// @see parse.hasCodeBetween
func (b *Binder) Bind(comments []CommentBlock, nodes []model.Node, language string, sourceLines []string) []Binding {
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
			// 일반 comment: Look-Between 동적 바인딩
			gap := node.StartLine - comment.EndLine
			if gap < 1 {
				continue
			}
			if gap > 1 {
				// sourceLines nil → 보수적 폴백: gap=1만 허용
				if sourceLines == nil {
					continue
				}
				// 사이 구간에 비-공백 라인이 있으면 바인딩 거부
				if hasCodeBetween(sourceLines, comment.EndLine, node.StartLine) {
					continue
				}
			}
			normalized := b.normalizer.Normalize(comment.Text, language)
			ann, _ := b.parser.Parse(normalized)
			bindings = append(bindings, Binding{Node: node, Annotation: ann})
			break
		}
	}

	return bindings
}

// isPassthroughLine reports whether a source line should be ignored during
// Look-Between binding — blank lines, comments, decorators, and attributes
// are all considered passthrough and do not block comment-to-node binding.
// @intent classify a single source line as non-code (passthrough) for binding logic
func isPassthroughLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	// Comments: //, /*, *, */, #, --
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
		strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") ||
		strings.HasPrefix(trimmed, "*/") || strings.HasPrefix(trimmed, "--") {
		return true
	}
	// Decorators/annotations: @something (Python, Java, Kotlin, TS)
	if strings.HasPrefix(trimmed, "@") {
		return true
	}
	// C/C++ attributes: __attribute__((...)), [[...]]
	if strings.HasPrefix(trimmed, "__attribute__") || strings.HasPrefix(trimmed, "[[") {
		return true
	}
	return false
}

// hasCodeBetween checks whether any line between commentEndLine and nodeStartLine
// contains actual code (not passthrough). Lines are 1-indexed; sourceLines is 0-indexed.
// @intent determine if real code exists between a comment and declaration for Look-Between binding
func hasCodeBetween(sourceLines []string, commentEndLine, nodeStartLine int) bool {
	for lineNum := commentEndLine + 1; lineNum < nodeStartLine; lineNum++ {
		idx := lineNum - 1
		if idx < 0 || idx >= len(sourceLines) {
			continue
		}
		if !isPassthroughLine(sourceLines[idx]) {
			return true
		}
	}
	return false
}

// hasContent reports whether an annotation contains any indexable text or tags.
// @intent skip empty annotation payloads before they are bound to nodes
func hasContent(ann *model.Annotation) bool {
	return ann.Summary != "" || ann.Context != "" || len(ann.Tags) > 0
}
