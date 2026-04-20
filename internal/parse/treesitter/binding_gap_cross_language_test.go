// Cross-language @intent 바인딩 계약 테스트.
//
// 목적: ccg가 공식 지원하는 12개 언어에서 "심볼 위에 한 줄 @intent 주석이 붙어 있으면
// 해당 심볼 노드에 @intent 태그가 바인딩된다"는 계약이 동일하게 동작함을 보장한다.
//
// 범위: 데코레이터/어노테이션/속성/매크로 등 gap 요소 없이 "주석 + 심볼" 최소 케이스만 검증.
// gap 관련 동작은 binding_gap_integration_test.go / binding_gap_p1_test.go 에서 별도 검증.
//
// 테스트 정책:
//   - expectBound=true  → Green 계약. 정상 바인딩되어야 하며, 실패 시 회귀(normalizer/walker/binder).
//   - expectBound=false → Red 계약. 현재 tree-sitter grammar 특성으로 바인딩이 누락됨을
//                         "명시적으로" 고정. 구현이 개선되어 우연히 바인딩되면 테스트가 실패하므로
//                         그때 Green으로 승격하고 skipReason과 walker 보정을 함께 정리한다.
//
// Helper 함수:
//   - binderFromWalkerComments, logNodeInfo, logCommentInfo 는
//     binding_gap_integration_test.go 상단에 정의되어 있음.
package treesitter

import (
	"context"
	"testing"

	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse"
)

// crossLangIntentCase 는 언어별 "@intent 바인딩 최소 계약" 테스트 케이스다.
type crossLangIntentCase struct {
	label          string
	spec           *LangSpec
	filename       string
	source         string
	symbolName     string
	expectedKind   model.NodeKind
	expectedIntent string
	// expectBound=false 이면 현재 바인딩이 실패하는 Red 계약으로 다룬다.
	// 파싱은 성공해야 하지만 @intent 태그가 달리지 않아야 한다.
	expectBound bool
	// redReason 은 expectBound=false 케이스에서 "왜 현재 바인딩이 실패하는가"를
	// 기록한다. grammar quirk를 문서화하고, 향후 walker 보정 시 근거 자료로 쓴다.
	redReason string
}

func TestCrossLanguage_IntentBinding_MinimalContract(t *testing.T) {
	cases := []crossLangIntentCase{
		{
			label:    "Go_LineComment_Function",
			spec:     GoSpec,
			filename: "sample.go",
			source: `package sample

// @intent greet returns a friendly greeting
func Greet() string { return "hi" }
`,
			symbolName:     "Greet",
			expectedKind:   model.NodeKindFunction,
			expectedIntent: "greet returns a friendly greeting",
			expectBound:    true,
		},
		{
			label:    "Python_HashComment_Function",
			spec:     PythonSpec,
			filename: "sample.py",
			source: `# @intent greet returns a friendly greeting
def greet():
    return "hi"
`,
			symbolName:     "greet",
			expectedKind:   model.NodeKindFunction,
			expectedIntent: "greet returns a friendly greeting",
			expectBound:    true,
		},
		{
			label:    "TypeScript_LineComment_Function",
			spec:     TypeScriptSpec,
			filename: "sample.ts",
			source: `// @intent greet returns a friendly greeting
function greet(): string { return "hi"; }
`,
			symbolName:     "greet",
			expectedKind:   model.NodeKindFunction,
			expectedIntent: "greet returns a friendly greeting",
			expectBound:    true,
		},
		{
			label:    "Java_Javadoc_Method",
			spec:     JavaSpec,
			filename: "Sample.java",
			source: `public class Sample {
    /** @intent greet returns a friendly greeting */
    public String greet() { return "hi"; }
}
`,
			symbolName:     "greet",
			expectedKind:   model.NodeKindFunction,
			expectedIntent: "greet returns a friendly greeting",
			expectBound:    true,
		},
		{
			label:    "C_BlockComment_Function",
			spec:     CSpec,
			filename: "sample.c",
			source: `/** @intent greet returns a friendly greeting */
int greet(void) { return 0; }
`,
			symbolName:     "greet",
			expectedKind:   model.NodeKindFunction,
			expectedIntent: "greet returns a friendly greeting",
			expectBound:    true,
		},
		{
			// Green 케이스: `///` 뒤에 빈 줄을 두어 trailing-newline quirk를 회피.
			// walker.collectComments가 Rust `line_comment` 노드의 EndRow를 다음 줄로
			// 보고하므로, 빈 줄 없이 fn이 바로 오면 gap=0 으로 판정된다 (아래 Red 케이스 참조).
			label:    "Rust_DocComment_Function_WithBlankLine",
			spec:     RustSpec,
			filename: "sample_blank.rs",
			source: `/// @intent greet returns a friendly greeting

fn greet() -> &'static str { "hi" }
`,
			symbolName:     "greet",
			expectedKind:   model.NodeKindFunction,
			expectedIntent: "greet returns a friendly greeting",
			expectBound:    true,
		},
		{
			// Green 계약: `///` 바로 다음 줄에 fn 이 오는 자연스러운 Rust 도크 주석 패턴.
			// walker.collectComments 에서 line_comment 노드의 EndPoint.Column == 0 일 때
			// endLine-- 정규화를 적용해 gap = 1 이 되도록 보정한다.
			label:    "Rust_DocComment_Function_NoBlankLine",
			spec:     RustSpec,
			filename: "sample_noblank.rs",
			source: `/// @intent greet returns a friendly greeting
fn greet() -> &'static str { "hi" }
`,
			symbolName:     "greet",
			expectedKind:   model.NodeKindFunction,
			expectedIntent: "greet returns a friendly greeting",
			expectBound:    true,
		},
		{
			label:    "Cpp_BlockComment_Function",
			spec:     CppSpec,
			filename: "sample.cpp",
			source: `/** @intent greet returns a friendly greeting */
int greet() { return 0; }
`,
			symbolName:     "greet",
			expectedKind:   model.NodeKindFunction,
			expectedIntent: "greet returns a friendly greeting",
			expectBound:    true,
		},
		{
			label:    "JavaScript_LineComment_Function",
			spec:     JavaScriptSpec,
			filename: "sample.js",
			source: `// @intent greet returns a friendly greeting
function greet() { return "hi"; }
`,
			symbolName:     "greet",
			expectedKind:   model.NodeKindFunction,
			expectedIntent: "greet returns a friendly greeting",
			expectBound:    true,
		},
		{
			label:    "Ruby_HashComment_Method",
			spec:     RubySpec,
			filename: "sample.rb",
			source: `# @intent greet returns a friendly greeting
def greet
  "hi"
end
`,
			symbolName:     "greet",
			expectedKind:   model.NodeKindFunction,
			expectedIntent: "greet returns a friendly greeting",
			expectBound:    true,
		},
		{
			label:    "Kotlin_Javadoc_Function",
			spec:     KotlinSpec,
			filename: "Sample.kt",
			source: `/** @intent greet returns a friendly greeting */
fun greet(): String { return "hi" }
`,
			symbolName:     "greet",
			expectedKind:   model.NodeKindFunction,
			expectedIntent: "greet returns a friendly greeting",
			expectBound:    true,
		},
		{
			// PHP 소스는 반드시 `<?php` 오프닝 태그로 시작해야 tree-sitter-php 가
			// 최상위 `function_definition` 을 인식한다. 태그 없이 주석/함수만 있는 파일은
			// `text` 노드로 취급되어 심볼이 추출되지 않는다.
			label:    "PHP_LineComment_Function",
			spec:     PHPSpec,
			filename: "sample.php",
			source: `<?php
// @intent greet returns a friendly greeting
function greet() { return "hi"; }
`,
			symbolName:     "greet",
			expectedKind:   model.NodeKindFunction,
			expectedIntent: "greet returns a friendly greeting",
			expectBound:    true,
		},
		{
			// Red 계약: tree-sitter-lua 의 두 가지 range quirk가 합쳐진 결과.
			//   1) comment 노드가 선행 trailing-newline 을 포함 → EndRow가 다음 줄로 잡힘
			//   2) function_statement 가 선행 공백/주석을 흡수 → StartRow가 같은 줄로 당겨짐
			// 최종적으로 comment.EndLine == function.StartLine 이 되어 gap=0 → binder 거부.
			//
			// 후속 과제: walker 에서 Lua comment/function_statement range 를 실제 토큰 경계로
			// 보정해야 한다. Green 으로 바뀌면 이 케이스를 expectBound=true 로 승격한다.
			label:    "Lua_LineComment_Function",
			spec:     LuaSpec,
			filename: "sample.lua",
			source: `-- @intent greet returns a friendly greeting
function greet() return "hi" end
`,
			symbolName:   "greet",
			expectedKind: model.NodeKindFunction,
			expectBound:  false,
			redReason:    "tree-sitter-lua comment/function_statement range quirk → gap=0",
		},
		// --- Class / Interface / Struct 케이스 ---
		{
			label:    "Go_LineComment_Struct",
			spec:     GoSpec,
			filename: "sample_struct.go",
			source: `package sample

// @intent user entity holds profile data
type User struct {
	Name string
}
`,
			symbolName:     "User",
			expectedKind:   model.NodeKindClass,
			expectedIntent: "user entity holds profile data",
			expectBound:    true,
		},
		{
			label:    "Python_HashComment_Class",
			spec:     PythonSpec,
			filename: "sample_class.py",
			source: `# @intent user entity holds profile data
class User:
    pass
`,
			symbolName:     "User",
			expectedKind:   model.NodeKindClass,
			expectedIntent: "user entity holds profile data",
			expectBound:    true,
		},
		{
			label:    "Java_Javadoc_Class",
			spec:     JavaSpec,
			filename: "SampleClass.java",
			source: `/** @intent user entity holds profile data */
public class User {}
`,
			symbolName:     "User",
			expectedKind:   model.NodeKindClass,
			expectedIntent: "user entity holds profile data",
			expectBound:    true,
		},
		{
			label:    "Java_Javadoc_Interface",
			spec:     JavaSpec,
			filename: "SampleIface.java",
			source: `/** @intent repository contract for user persistence */
public interface UserRepository {}
`,
			symbolName:     "UserRepository",
			expectedKind:   model.NodeKindType,
			expectedIntent: "repository contract for user persistence",
			expectBound:    true,
		},
		{
			label:    "TypeScript_LineComment_Class",
			spec:     TypeScriptSpec,
			filename: "sample_class.ts",
			source: `// @intent user entity holds profile data
class User {
  name: string = "";
}
`,
			symbolName:     "User",
			expectedKind:   model.NodeKindClass,
			expectedIntent: "user entity holds profile data",
			expectBound:    true,
		},
		{
			label:    "Kotlin_Javadoc_Class",
			spec:     KotlinSpec,
			filename: "SampleClass.kt",
			source: `/** @intent user entity holds profile data */
data class User(val name: String)
`,
			symbolName:     "User",
			expectedKind:   model.NodeKindClass,
			expectedIntent: "user entity holds profile data",
			expectBound:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			w := NewWalker(tc.spec)
			nodes, _, walkerComments, err := w.ParseWithComments(
				context.Background(), tc.filename, []byte(tc.source),
			)
			if err != nil {
				t.Fatalf("[%s] 파싱 실패: %v", tc.label, err)
			}

			t.Logf("[%s] 노드 수=%d, 주석블록 수=%d",
				tc.label, len(nodes), len(walkerComments))

			// Phase 1: 심볼이 실제로 파싱되었는지 확인 (Kind 포함).
			//          바인딩 실패인지 파싱 실패인지 구분하기 위한 게이트.
			var symbol *model.Node
			for i := range nodes {
				if nodes[i].Name == tc.symbolName && nodes[i].Kind == tc.expectedKind {
					symbol = &nodes[i]
					break
				}
			}
			if symbol == nil {
				logNodeInfo(t, nodes)
				logCommentInfo(t, walkerComments)
				t.Fatalf("[%s] 심볼 파싱 실패: name=%q kind=%q 인 노드를 찾지 못함 (파서/LangSpec 회귀)",
					tc.label, tc.symbolName, tc.expectedKind)
			}

			// Phase 2: 바인딩 시도.
			binder := parse.NewBinder()
			bindings := binder.Bind(
				binderFromWalkerComments(walkerComments), nodes, tc.spec.Name,
			)

			var target *parse.Binding
			for i := range bindings {
				if bindings[i].Node.Name == tc.symbolName && bindings[i].Node.Kind == tc.expectedKind {
					target = &bindings[i]
					break
				}
			}

			// Phase 3: expectBound 분기.
			if !tc.expectBound {
				if target == nil {
					t.Logf("[%s] Red 계약 유지 (%s) — 바인딩 없음, 심볼은 정상 파싱됨",
						tc.label, tc.redReason)
					return
				}
				// 바인딩이 붙어버린 경우: 구현이 개선됐을 가능성이 높다.
				// Red → Green 승격을 강제하기 위해 실패로 처리.
				logNodeInfo(t, nodes)
				logCommentInfo(t, walkerComments)
				t.Fatalf("[%s] Red 계약 위반: 바인딩이 발생함. "+
					"redReason=%q 이 더 이상 유효하지 않으니 케이스를 expectBound=true 로 승격하세요. Tags=%+v",
					tc.label, tc.redReason, target.Annotation.Tags)
			}

			if target == nil {
				logNodeInfo(t, nodes)
				logCommentInfo(t, walkerComments)
				t.Fatalf("[%s] 심볼은 파싱됐지만 @intent 바인딩이 누락됨 (name=%q kind=%q). "+
					"binder/walker 회귀 가능성",
					tc.label, tc.symbolName, tc.expectedKind)
			}

			var intentValue string
			for _, tag := range target.Annotation.Tags {
				if tag.Kind == "intent" {
					intentValue = tag.Value
					break
				}
			}
			if intentValue == "" {
				t.Fatalf("[%s] %s에 @intent 태그 없음. Tags=%+v",
					tc.label, tc.symbolName, target.Annotation.Tags)
			}
			if intentValue != tc.expectedIntent {
				t.Errorf("[%s] @intent 값 불일치\n  expected: %q\n  actual:   %q",
					tc.label, tc.expectedIntent, intentValue)
			}
		})
	}
}
