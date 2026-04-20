// Go `//go:*` 디렉티브가 @intent 주석과 심볼 사이에 끼어 있을 때
// @intent 태그 Value가 오염되지 않는지 검증하는 테이블 테스트.
//
// P1 실측(TestWalkerBinder_Go_BuildDirective_P1Measurement)에서
// `//go:generate` 줄이 @intent CommentBlock과 병합되어 태그 Value에
// "go:generate stringer -type=Pill" 같은 디렉티브 본문이 섞여 들어가는
// 오염을 관찰했다. 이 테스트는 2가지 흔한 디렉티브 (generate, noinline)
// 각각에 대해 Value가 깨끗한 의도 문자열과 일치해야 한다고 주장한다.
// (Go tags.scm이 var_declaration을 캡처하지 않아 go:embed 케이스는 제외.)
//
// TDD 상태: Red — 현재 구현은 디렉티브 본문을 Value에 포함시키므로 모두 실패한다.
// P2-1 normalizer(Go 분기에서 `go:<word>` 줄 제거) 적용 시 Green 전환 예정.
package treesitter

import (
	"context"
	"strings"
	"testing"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse"
)

// goDirectiveCase 는 Go 디렉티브별 오염 테스트 케이스.
type goDirectiveCase struct {
	label        string
	filename     string
	symName      string
	symKind      model.NodeKind
	wantIntent   string
	directiveTag string // Value에 섞이면 안 되는 디렉티브 본문 조각
}

// TestWalkerBinder_Go_DirectivePollution 은 @intent 값이 `//go:*`
// 디렉티브 본문에 오염되지 않는지 디렉티브 종류별로 검증한다.
func TestWalkerBinder_Go_DirectivePollution(t *testing.T) {
	cases := []goDirectiveCase{
		{
			label:        "go:generate / type",
			filename:     "directive_gap.go",
			symName:      "Pill",
			symKind:      model.NodeKindType,
			wantIntent:   "약 타입 이넘",
			directiveTag: "go:generate",
		},
		{
			label:        "go:noinline / func",
			filename:     "directive_noinline.go",
			symName:      "HotPath",
			symKind:      model.NodeKindFunction,
			wantIntent:   "인라인 금지 핫 패스",
			directiveTag: "go:noinline",
		},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			content := readFixture(t, "go", tc.filename)
			w := NewWalker(GoSpec)
			nodes, _, walkerComments, err := w.ParseWithComments(context.Background(), tc.filename, content)
			if err != nil {
				t.Fatalf("[%s] 파싱 실패: %v", tc.label, err)
			}

			t.Logf("[%s] fixture:\n%s", tc.label, string(content))
			t.Log("--- 파싱된 노드 ---")
			logNodeInfo(t, nodes)
			t.Log("--- 파싱된 CommentBlock ---")
			logCommentInfo(t, walkerComments)

			b := parse.NewBinder()
			bindings := b.Bind(binderFromWalkerComments(walkerComments), nodes, "go")

			var intentValue string
			var bound bool
			for _, binding := range bindings {
				if binding.Node.Name != tc.symName || binding.Node.Kind != tc.symKind {
					continue
				}
				for _, tag := range binding.Annotation.Tags {
					if tag.Kind == "intent" {
						intentValue = tag.Value
						bound = true
						break
					}
				}
			}

			if !bound {
				t.Fatalf("[%s] %s(%s)에 @intent 미바인딩", tc.label, tc.symName, tc.symKind)
			}

			t.Logf("[%s] 실측 @intent Value=%q", tc.label, intentValue)

			if intentValue != tc.wantIntent {
				t.Errorf("[Red][%s] @intent Value 오염\n  got : %q\n  want: %q",
					tc.label, intentValue, tc.wantIntent)
			}
			if tc.directiveTag != "" && strings.Contains(intentValue, tc.directiveTag) {
				t.Errorf("[Red][%s] @intent Value에 디렉티브 본문 %q 포함됨: %q",
					tc.label, tc.directiveTag, intentValue)
			}
		})
	}
}
