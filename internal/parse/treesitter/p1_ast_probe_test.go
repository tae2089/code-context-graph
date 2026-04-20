// P1 언어별 주석 노드 타입 AST 탐사 테스트.
// 목적: Kotlin/PHP/TypeScript의 실제 comment 노드 이름을 확인한다.
// 이 테스트는 항상 PASS (진단 전용).
package treesitter

import (
	"context"
	"strings"
	"testing"

	sitter "github.com/smacker/go-tree-sitter"
)

func dumpASTTypes(t *testing.T, node *sitter.Node, depth, maxDepth int, seen map[string]int) {
	if node == nil || depth > maxDepth {
		return
	}
	seen[node.Type()]++
	for i := 0; i < int(node.ChildCount()); i++ {
		ch := node.Child(i)
		if ch != nil {
			dumpASTTypes(t, ch, depth+1, maxDepth, seen)
		}
	}
}

func dumpTopLevelAST(t *testing.T, root *sitter.Node, content []byte) {
	t.Helper()
	for i := 0; i < int(root.ChildCount()); i++ {
		ch := root.Child(i)
		if ch == nil {
			continue
		}
		text := ch.Content(content)
		if len(text) > 60 {
			text = text[:60] + "..."
		}
		text = strings.ReplaceAll(text, "\n", "\\n")
		t.Logf("  [root child] type=%-30s line=%d-%d  %q",
			ch.Type(), ch.StartPoint().Row+1, ch.EndPoint().Row+1, text)
		// 1-depth 자식도 출력
		for j := 0; j < int(ch.ChildCount()); j++ {
			g := ch.Child(j)
			if g == nil {
				continue
			}
			gt := g.Content(content)
			if len(gt) > 40 {
				gt = gt[:40] + "..."
			}
			gt = strings.ReplaceAll(gt, "\n", "\\n")
			t.Logf("      └ type=%-28s line=%d-%d  %q",
				g.Type(), g.StartPoint().Row+1, g.EndPoint().Row+1, gt)
		}
	}
}

func TestP1_AST_NodeTypes(t *testing.T) {
	type probe struct {
		label    string
		spec     *LangSpec
		lang     string
		filename string
	}
	cases := []probe{
		{"Kotlin", KotlinSpec, "kotlin", "AnnotationGap.kt"},
		{"PHP", PHPSpec, "php", "attribute_gap.php"},
		{"TypeScript", TypeScriptSpec, "typescript", "decorator_gap.ts"},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			content := readFixture(t, tc.lang, tc.filename)
			w := NewWalker(tc.spec)
			tree, err := w.parser.ParseCtx(context.Background(), nil, content)
			if err != nil {
				t.Fatalf("[%s] 파싱 실패: %v", tc.label, err)
			}
			defer tree.Close()
			root := tree.RootNode()

			t.Logf("=== [%s] root.Type()=%s ChildCount=%d ===", tc.label, root.Type(), root.ChildCount())
			dumpTopLevelAST(t, root, content)

			seen := make(map[string]int)
			dumpASTTypes(t, root, 0, 10, seen)
			t.Logf("[%s] 발견된 모든 노드 타입:", tc.label)
			for k, v := range seen {
				if strings.Contains(strings.ToLower(k), "comment") ||
					strings.Contains(strings.ToLower(k), "annot") ||
					strings.Contains(strings.ToLower(k), "decor") ||
					strings.Contains(strings.ToLower(k), "attrib") {
					t.Logf("  [%s] %s × %d", tc.label, k, v)
				}
			}
		})
	}
}
