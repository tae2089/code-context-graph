// P1 언어(TypeScript/Kotlin/PHP/C++/Go) binding-gap 실측 테스트.
//
// 목적: 데코레이터/어노테이션/속성/빌드 디렉티브가 심볼 위에 끼어 있을 때
// @intent 바인딩이 성공하는지 현재 상태를 측정한다.
//
// 원칙 (Data-Driven):
//   - 각 언어의 실제 동작을 t.Logf로 기록
//   - 실패 케이스만 t.Errorf로 표시 (Red 테스트)
//   - 언어별 tree-sitter 문법이 메타 표식을 심볼 노드에 포함하는지 확인
//
// 검증 대상 4개 분류:
//   1. 심볼 StartLine이 메타 표식 줄을 포함하는가 (Java/C처럼 wrapper가 흡수)
//   2. @intent 태그가 정상 파싱되는가 (normalizer 이슈 없는가)
//   3. gap이 maxGap(=2) 이내인가
//   4. 최종적으로 Binder가 @intent를 바인딩하는가
package treesitter

import (
	"context"
	"strconv"
	"testing"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse"
)

// p1Case 는 P1 각 언어 실측 케이스 정의다.
type p1Case struct {
	label    string
	spec     *LangSpec
	lang     string
	filename string
	symName  string
	symKind  model.NodeKind
	metaKind string // "decorator" | "annotation" | "attribute" | "directive"
}

// runP1Measurement 는 단일 케이스에 대해 StartLine/gap/binding을 측정한다.
//
// 반환값: intentBound (true이면 현재 바인딩 성공).
func runP1Measurement(t *testing.T, tc p1Case) bool {
	t.Helper()

	content := readFixture(t, tc.lang, tc.filename)
	t.Logf("─── [%s] fixture: %s ───", tc.label, tc.filename)
	t.Logf("내용:\n%s", string(content))

	w := NewWalker(tc.spec)
	nodes, _, walkerComments, err := w.ParseWithComments(context.Background(), tc.filename, content)
	if err != nil {
		t.Fatalf("[%s] 파싱 실패: %v", tc.label, err)
	}

	t.Log("--- 파싱된 노드 ---")
	logNodeInfo(t, nodes)
	t.Log("--- 파싱된 CommentBlock ---")
	logCommentInfo(t, walkerComments)

	// 대상 심볼 찾기
	var target *model.Node
	for i := range nodes {
		if nodes[i].Name == tc.symName && nodes[i].Kind == tc.symKind {
			target = &nodes[i]
			break
		}
	}
	if target == nil {
		t.Errorf("[%s] 대상 심볼 %s(%s) 노드 미발견", tc.label, tc.symName, tc.symKind)
		return false
	}
	t.Logf("[%s] %s.StartLine=%d (메타 종류=%s)", tc.label, tc.symName, target.StartLine, tc.metaKind)

	// gap 계산
	for _, c := range walkerComments {
		gap := target.StartLine - c.EndLine
		status := "OK"
		if gap < 1 || gap > 2 {
			status = "FAIL(gap 범위 밖)"
		}
		t.Logf("[%s] 주석 EndLine=%d → gap=%d (%s)", tc.label, c.EndLine, gap, status)
	}

	// Binder 결과
	b := parse.NewBinder()
	bindings := b.Bind(binderFromWalkerComments(walkerComments), nodes, tc.lang)

	intentBound := false
	for _, binding := range bindings {
		if binding.Node.Name != tc.symName || binding.Node.Kind != tc.symKind {
			continue
		}
		for _, tag := range binding.Annotation.Tags {
			if tag.Kind == "intent" {
				intentBound = true
				t.Logf("[%s] @intent 바인딩 성공 value=%q", tc.label, tag.Value)
				break
			}
		}
	}
	if !intentBound {
		t.Logf("[%s] @intent 바인딩 실패 (현재 상태)", tc.label)
	}
	return intentBound
}

// TestWalkerBinder_TypeScript_Decorators_P1Measurement 는
// TypeScript 데코레이터(@Injectable, @Component) 뒤 class의 바인딩을 측정한다.
//
// 가설: tree-sitter-typescript가 class_declaration에 decorator를 자식으로 포함하는가?
// Java처럼 포함한다면 StartLine이 첫 데코레이터 줄로 잡혀 gap=1 → 성공 예상.
func TestWalkerBinder_TypeScript_Decorators_P1Measurement(t *testing.T) {
	tc := p1Case{
		label:    "TypeScript/decorator",
		spec:     TypeScriptSpec,
		lang:     "typescript",
		filename: "decorator_gap.ts",
		symName:  "UserService",
		symKind:  model.NodeKindClass,
		metaKind: "decorator",
	}
	bound := runP1Measurement(t, tc)
	if !bound {
		t.Errorf("[Red] TypeScript 데코레이터 gap: UserService @intent 바인딩 없음 — 원인 실측 필요")
	}
}

// TestWalkerBinder_Kotlin_Annotations_P1Measurement 는
// Kotlin @Composable + @JvmStatic 뒤 fun의 바인딩을 측정한다.
func TestWalkerBinder_Kotlin_Annotations_P1Measurement(t *testing.T) {
	tc := p1Case{
		label:    "Kotlin/annotation",
		spec:     KotlinSpec,
		lang:     "kotlin",
		filename: "AnnotationGap.kt",
		symName:  "Greeting",
		symKind:  model.NodeKindFunction,
		metaKind: "annotation",
	}
	bound := runP1Measurement(t, tc)
	if !bound {
		t.Errorf("[Red] Kotlin 어노테이션 gap: Greeting @intent 바인딩 없음 — 원인 실측 필요")
	}
}

// TestWalkerBinder_PHP_Attributes_P1Measurement 는
// PHP 8 #[...] 속성 뒤 function의 바인딩을 측정한다.
func TestWalkerBinder_PHP_Attributes_P1Measurement(t *testing.T) {
	tc := p1Case{
		label:    "PHP/attribute",
		spec:     PHPSpec,
		lang:     "php",
		filename: "attribute_gap.php",
		symName:  "getUser",
		symKind:  model.NodeKindFunction,
		metaKind: "attribute",
	}
	bound := runP1Measurement(t, tc)
	if !bound {
		t.Errorf("[Red] PHP 속성 gap: getUser @intent 바인딩 없음 — 원인 실측 필요")
	}
}

// TestWalkerBinder_Cpp_Attributes_P1Measurement 는
// C++ [[nodiscard]] [[deprecated]] 뒤 function의 바인딩을 측정한다.
//
// 가설: C와 달리 C++ [[...]] 속성은 function_definition 자식에 포함되는가?
func TestWalkerBinder_Cpp_Attributes_P1Measurement(t *testing.T) {
	tc := p1Case{
		label:    "C++/attribute",
		spec:     CppSpec,
		lang:     "cpp",
		filename: "attribute_gap.cpp",
		symName:  "divide",
		symKind:  model.NodeKindFunction,
		metaKind: "attribute",
	}
	bound := runP1Measurement(t, tc)
	if !bound {
		t.Errorf("[Red] C++ 속성 gap: divide @intent 바인딩 없음 — 원인 실측 필요")
	}
}

// TestWalkerBinder_Go_BuildDirective_P1Measurement 는
// Go의 //go:generate 디렉티브가 @intent 주석과 심볼 사이에 끼어있을 때
// 바인딩이 깨지는지 측정한다.
//
// 가설: //go:generate도 comment 노드로 수집되므로 @intent가 있는 CommentBlock과
// type 선언 사이에 끼어 gap을 늘릴 수 있다.
func TestWalkerBinder_Go_BuildDirective_P1Measurement(t *testing.T) {
	tc := p1Case{
		label:    "Go/directive",
		spec:     GoSpec,
		lang:     "go",
		filename: "directive_gap.go",
		symName:  "Pill",
		symKind:  model.NodeKindType,
		metaKind: "directive",
	}
	bound := runP1Measurement(t, tc)
	if !bound {
		t.Errorf("[Red] Go go:generate gap: Pill @intent 바인딩 없음 — 원인 실측 필요")
	}
}

// TestWalkerBinder_P1_AllLanguages_Summary 는 5개 P1 언어를 한 테이블로 집계한다.
// 이 테스트 자체는 항상 Pass — 진단 전용.
func TestWalkerBinder_P1_AllLanguages_Summary(t *testing.T) {
	cases := []p1Case{
		{"TypeScript/decorator", TypeScriptSpec, "typescript", "decorator_gap.ts", "UserService", model.NodeKindClass, "decorator"},
		{"Kotlin/annotation", KotlinSpec, "kotlin", "AnnotationGap.kt", "Greeting", model.NodeKindFunction, "annotation"},
		{"PHP/attribute", PHPSpec, "php", "attribute_gap.php", "getUser", model.NodeKindFunction, "attribute"},
		{"C++/attribute", CppSpec, "cpp", "attribute_gap.cpp", "divide", model.NodeKindFunction, "attribute"},
		{"Go/directive", GoSpec, "go", "directive_gap.go", "Pill", model.NodeKindType, "directive"},
	}

	type row struct {
		label        string
		startLine    int
		intentBound  bool
		minGap       int
		found        bool
	}
	var rows []row

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			content := readFixture(t, tc.lang, tc.filename)
			w := NewWalker(tc.spec)
			nodes, _, walkerComments, err := w.ParseWithComments(context.Background(), tc.filename, content)
			if err != nil {
				t.Logf("[%s] 파싱 실패: %v", tc.label, err)
				rows = append(rows, row{label: tc.label})
				return
			}

			r := row{label: tc.label}
			for i := range nodes {
				if nodes[i].Name == tc.symName && nodes[i].Kind == tc.symKind {
					r.startLine = nodes[i].StartLine
					r.found = true
					break
				}
			}

			// 최소 gap 계산
			r.minGap = 9999
			for _, c := range walkerComments {
				gap := r.startLine - c.EndLine
				if gap >= 1 && gap < r.minGap {
					r.minGap = gap
				}
			}

			// 바인딩 체크
			b := parse.NewBinder()
			bindings := b.Bind(binderFromWalkerComments(walkerComments), nodes, tc.lang)
			for _, binding := range bindings {
				if binding.Node.Name != tc.symName || binding.Node.Kind != tc.symKind {
					continue
				}
				for _, tag := range binding.Annotation.Tags {
					if tag.Kind == "intent" {
						r.intentBound = true
						break
					}
				}
			}
			rows = append(rows, r)
		})
	}

	t.Log("====================================================================")
	t.Log("P1 언어 binding-gap 실측 요약")
	t.Log("label                   | StartLine | found | minGap | intentBound")
	t.Log("--------------------------------------------------------------------")
	for _, r := range rows {
		gap := "-"
		if r.found && r.minGap != 9999 {
			gap = strconv.Itoa(r.minGap)
		}
		t.Logf("%-23s | %-9d | %-5v | %-6s | %v", r.label, r.startLine, r.found, gap, r.intentBound)
	}
	t.Log("====================================================================")
}
