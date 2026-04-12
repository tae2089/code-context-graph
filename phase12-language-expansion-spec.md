# Phase 12: 언어 확장 — Tech Spec

> **목표**: 현재 5개 언어 지원을 ~15개로 확장.
> 각 언어는 `LangSpec` 정의 + Tree-sitter grammar 등록 + walker 테스트로 구성.
> `smacker/go-tree-sitter` 라이브러리의 사용 가능한 grammar만 대상으로 한다.

---

## 현재 상태

### 기존 지원 언어 (5개)

| 언어 | LangSpec 변수 | Grammar 패키지 | FunctionTypes | ClassTypes | ImportTypes | CallTypes | TestPrefix |
|------|--------------|---------------|---------------|-----------|-------------|-----------|------------|
| Go | `GoSpec` | (기본) | `function_declaration`, `method_declaration` | `type_declaration` | `import_declaration`, `import_spec` | `call_expression` | `Test` |
| Python | `PythonSpec` | `python` | `function_definition` | `class_definition` | `import_statement`, `import_from_statement` | `call` | `test_` |
| TypeScript | `TypeScriptSpec` | `typescript` | `function_declaration`, `method_definition` | `class_declaration` | `import_statement` | `call_expression` | `test` |
| Java | `JavaSpec` | `java` | `method_declaration`, `constructor_declaration` | `class_declaration` | `import_declaration` | `method_invocation` | `test` |
| Ruby | `RubySpec` | `ruby` | `method` | `class` | `call` (require) | `call` | `test_` |

### 아키텍처 패턴

**LangSpec 구조체** (`internal/parse/treesitter/langspec.go`):
```go
type LangSpec struct {
    Name            string
    FunctionTypes   []string
    ClassTypes      []string
    InterfaceTypes  []string
    ImportTypes     []string
    CallTypes       []string
    TestPrefix      string
    PackageNodeType string
}
```

**Walker** (`internal/parse/treesitter/walker.go`): LangSpec을 참조하여 AST 노드를 순회하며 model.Node, model.Edge 생성.

**Registry** (`internal/parse/registry.go`): 확장자 → LangSpec 매핑. `RegisterLanguage(ext, spec)`.

---

## smacker/go-tree-sitter 사용 가능 Grammar

> `github.com/smacker/go-tree-sitter` 라이브러리에서 제공하는 언어 grammar 서브패키지.

| 언어 | Import 경로 | 사용 가능 |
|------|------------|----------|
| JavaScript | `github.com/smacker/go-tree-sitter/javascript` | ✅ |
| C | `github.com/smacker/go-tree-sitter/c` | ✅ |
| C++ | `github.com/smacker/go-tree-sitter/cpp` | ✅ |
| Rust | `github.com/smacker/go-tree-sitter/rust` | ✅ |
| C# | `github.com/smacker/go-tree-sitter/csharp` | ✅ |
| Scala | `github.com/smacker/go-tree-sitter/scala` | ✅ |
| Swift | `github.com/smacker/go-tree-sitter/swift` | ✅ |
| PHP | `github.com/smacker/go-tree-sitter/php` | ✅ |
| Kotlin | `github.com/smacker/go-tree-sitter/kotlin` | ✅ |
| Lua | `github.com/smacker/go-tree-sitter/lua` | ✅ |
| Bash | `github.com/smacker/go-tree-sitter/bash` | ✅ |
| Elixir | `github.com/smacker/go-tree-sitter/elixir` | ✅ |
| CSS | `github.com/smacker/go-tree-sitter/css` | ✅ (구조 분석 불필요, 제외) |
| HTML | `github.com/smacker/go-tree-sitter/html` | ✅ (구조 분석 불필요, 제외) |
| YAML | `github.com/smacker/go-tree-sitter/yaml` | ✅ (구조 분석 불필요, 제외) |
| Dockerfile | `github.com/smacker/go-tree-sitter/dockerfile` | ✅ (구조 분석 불필요, 제외) |
| HCL | `github.com/smacker/go-tree-sitter/hcl` | ✅ (인프라용, 제외) |
| OCaml | `github.com/smacker/go-tree-sitter/ocaml` | ✅ |
| Protobuf | `github.com/smacker/go-tree-sitter/protobuf` | ✅ (스키마 전용, 제외) |

---

## 신규 언어 (10개 추가 → 총 15개)

### 우선순위 분류

| 우선순위 | 언어 | 사유 |
|---------|------|------|
| **Tier 1** (필수) | JavaScript, C#, Rust, Kotlin | 사용량 상위, Python 참조에도 있음 |
| **Tier 2** (중요) | C, C++, PHP | 시스템/웹 언어, 넓은 사용 범위 |
| **Tier 3** (선택) | Swift, Scala, Lua, Bash | 도메인 특화, 요청 시 추가 가능 |

> **제외**: Elixir (Tier 3 이하), OCaml (수요 낮음), CSS/HTML/YAML (코드 구조 분석 대상 아님)
> **C# 주의**: smacker 파서가 incomplete — 기본 구조(class, method, namespace)는 동작하나 고급 문법은 부분 파싱 가능

---

## 각 언어별 LangSpec 설계

### 12.1 JavaScript

```go
var JavaScriptSpec = &LangSpec{
    Name:          "javascript",
    FunctionTypes: []string{"function_declaration", "method_definition", "arrow_function"},
    ClassTypes:    []string{"class_declaration"},
    ImportTypes:   []string{"import_statement"},
    CallTypes:     []string{"call_expression"},
    TestPrefix:    "test",
}
```

| 항목 | 설명 |
|------|------|
| **확장자** | `.js`, `.jsx`, `.mjs`, `.cjs` |
| **Grammar** | `github.com/smacker/go-tree-sitter/javascript` |
| **특이사항** | `arrow_function`은 변수 할당 시 이름 추출 필요 (`const foo = () => {}`) |
| **테스트 감지** | `test`, `it`, `describe` — TestPrefix="test" + 추가 패턴 |
| **TypeScript와의 관계** | 별도 grammar. JSX는 JavaScript grammar에 포함. |

#### Walker 확장 필요사항
- `arrow_function`: 부모가 `variable_declarator`일 때 변수명을 함수명으로 사용
- `export_statement` 내 함수: export된 함수도 정상 추출

### 12.2 C

```go
var CSpec = &LangSpec{
    Name:          "c",
    FunctionTypes: []string{"function_definition"},
    ClassTypes:    []string{"struct_specifier"},
    ImportTypes:   []string{"preproc_include"},
    CallTypes:     []string{"call_expression"},
    TestPrefix:    "test_",
}
```

| 항목 | 설명 |
|------|------|
| **확장자** | `.c`, `.h` |
| **Grammar** | `github.com/smacker/go-tree-sitter/c` |
| **특이사항** | 헤더 파일(.h)은 선언만 포함 — 함수 선언(declaration)과 정의(definition) 구분 |
| **struct**: `struct_specifier` → Class 노드 |
| **include**: `#include` → IMPORTS_FROM 엣지 |

### 12.3 C++

```go
var CppSpec = &LangSpec{
    Name:           "cpp",
    FunctionTypes:  []string{"function_definition"},
    ClassTypes:     []string{"class_specifier", "struct_specifier"},
    InterfaceTypes: []string{},
    ImportTypes:    []string{"preproc_include"},
    CallTypes:      []string{"call_expression"},
    TestPrefix:     "TEST",
}
```

| 항목 | 설명 |
|------|------|
| **확장자** | `.cpp`, `.cc`, `.cxx`, `.hpp`, `.hh`, `.hxx` |
| **Grammar** | `github.com/smacker/go-tree-sitter/cpp` |
| **특이사항** | `class_specifier` + `struct_specifier` 모두 Class 노드 |
| **네임스페이스**: `namespace_definition` → 패키지 컨텍스트 |
| **템플릿**: `template_declaration` 내 함수 → 이름에 템플릿 파라미터 포함하지 않음 |

### 12.4 Rust

```go
var RustSpec = &LangSpec{
    Name:           "rust",
    FunctionTypes:  []string{"function_item"},
    ClassTypes:     []string{"struct_item", "enum_item"},
    InterfaceTypes: []string{"trait_item"},
    ImportTypes:    []string{"use_declaration"},
    CallTypes:      []string{"call_expression"},
    TestPrefix:     "test_",
}
```

| 항목 | 설명 |
|------|------|
| **확장자** | `.rs` |
| **Grammar** | `github.com/smacker/go-tree-sitter/rust` |
| **특이사항** | `trait_item` → Interface 노드. `impl_item` → IMPLEMENTS 엣지 |
| **모듈**: `mod_item` → 패키지 컨텍스트 |
| **테스트**: `#[test]` 속성 감지 (TestPrefix 외 추가 로직 필요) |
| **impl 블록**: `impl_item` 내 `function_item` → 메서드로 처리 (CONTAINS 엣지) |

#### Walker 확장 필요사항
- `#[test]` attribute 감지: `attribute_item` 노드에서 "test" 확인 → NodeKind=test
- `impl Trait for Struct` → IMPLEMENTS 엣지

### 12.5 C#

```go
var CSharpSpec = &LangSpec{
    Name:           "csharp",
    FunctionTypes:  []string{"method_declaration", "constructor_declaration"},
    ClassTypes:     []string{"class_declaration", "struct_declaration", "record_declaration"},
    InterfaceTypes: []string{"interface_declaration"},
    ImportTypes:    []string{"using_directive"},
    CallTypes:      []string{"invocation_expression"},
    TestPrefix:     "Test",
}
```

| 항목 | 설명 |
|------|------|
| **확장자** | `.cs` |
| **Grammar** | `github.com/smacker/go-tree-sitter/csharp` |
| **⚠️ 주의** | smacker 공식 주석: "The parser is incomplete, it may return a partial or wrong AST!" — 기본 class/method/namespace는 정상 동작하나, 복잡한 C# 문법(pattern matching, record 등)은 부분 파싱 가능 |
| **특이사항** | `namespace_declaration` → 패키지 컨텍스트 |
| **record**: C# 9.0+ `record` 타입 → Class 노드 |
| **테스트**: `[Test]`, `[Fact]`, `[Theory]` 속성 + TestPrefix 조합 |

### 12.6 Kotlin

```go
var KotlinSpec = &LangSpec{
    Name:           "kotlin",
    FunctionTypes:  []string{"function_declaration"},
    ClassTypes:     []string{"class_declaration", "object_declaration"},
    InterfaceTypes: []string{"interface_declaration"},
    ImportTypes:    []string{"import_header"},
    CallTypes:      []string{"call_expression"},
    TestPrefix:     "test",
}
```

| 항목 | 설명 |
|------|------|
| **확장자** | `.kt`, `.kts` |
| **Grammar** | `github.com/smacker/go-tree-sitter/kotlin` |
| **특이사항** | `object_declaration` (싱글턴/companion) → Class 노드 |
| **data class**: `class_declaration` modifier로 구분 가능하나 Phase 12에서는 일반 class로 처리 |
| **테스트**: `@Test` 어노테이션 + TestPrefix 조합 |

### 12.7 PHP

```go
var PHPSpec = &LangSpec{
    Name:          "php",
    FunctionTypes: []string{"function_definition", "method_declaration"},
    ClassTypes:    []string{"class_declaration"},
    InterfaceTypes: []string{"interface_declaration"},
    ImportTypes:   []string{"namespace_use_declaration"},
    CallTypes:     []string{"function_call_expression", "method_call_expression"},
    TestPrefix:    "test",
}
```

| 항목 | 설명 |
|------|------|
| **확장자** | `.php` |
| **Grammar** | `github.com/smacker/go-tree-sitter/php` |
| **특이사항** | `namespace_definition` → 패키지 컨텍스트 |
| **use 문**: `namespace_use_declaration` → IMPORTS_FROM 엣지 |
| **trait**: `trait_declaration` → 별도 처리 가능 (Phase 12 스코프 외) |

### 12.7 Swift

```go
var SwiftSpec = &LangSpec{
    Name:           "swift",
    FunctionTypes:  []string{"function_declaration"},
    ClassTypes:     []string{"class_declaration", "struct_declaration", "enum_declaration"},
    InterfaceTypes: []string{"protocol_declaration"},
    ImportTypes:    []string{"import_declaration"},
    CallTypes:      []string{"call_expression"},
    TestPrefix:     "test",
}
```

| 항목 | 설명 |
|------|------|
| **확장자** | `.swift` |
| **Grammar** | `github.com/smacker/go-tree-sitter/swift` |
| **특이사항** | `protocol_declaration` → Interface 노드 |
| **extension**: `extension_declaration` → 기존 타입에 메서드 추가 (CONTAINS 엣지) |

### 12.8 Scala

```go
var ScalaSpec = &LangSpec{
    Name:           "scala",
    FunctionTypes:  []string{"function_definition"},
    ClassTypes:     []string{"class_definition", "object_definition"},
    InterfaceTypes: []string{"trait_definition"},
    ImportTypes:    []string{"import_declaration"},
    CallTypes:      []string{"call_expression"},
    TestPrefix:     "test",
}
```

| 항목 | 설명 |
|------|------|
| **확장자** | `.scala`, `.sc` |
| **Grammar** | `github.com/smacker/go-tree-sitter/scala` |
| **특이사항** | `object_definition` (싱글턴) → Class 노드 |
| **trait**: `trait_definition` → Interface 노드 |
| **패턴 매칭**: 분석 대상 아님 (함수/클래스 구조만 추출) |

### 12.9 Lua

```go
var LuaSpec = &LangSpec{
    Name:          "lua",
    FunctionTypes: []string{"function_declaration", "local_function_declaration"},
    ClassTypes:    []string{},
    ImportTypes:   []string{},
    CallTypes:     []string{"function_call"},
    TestPrefix:    "test_",
}
```

| 항목 | 설명 |
|------|------|
| **확장자** | `.lua` |
| **Grammar** | `github.com/smacker/go-tree-sitter/lua` |
| **특이사항** | Lua는 클래스 개념 없음 — 함수만 추출 |
| **require**: `function_call` 중 `require("...")`를 import로 감지 (Walker 확장) |
| **로컬 함수**: `local_function_declaration` 포함 |

### 12.10 Bash

```go
var BashSpec = &LangSpec{
    Name:          "bash",
    FunctionTypes: []string{"function_definition"},
    ClassTypes:    []string{},
    ImportTypes:   []string{"command"},
    CallTypes:     []string{"command"},
    TestPrefix:    "test_",
}
```

| 항목 | 설명 |
|------|------|
| **확장자** | `.sh`, `.bash`, `.zsh` |
| **Grammar** | `github.com/smacker/go-tree-sitter/bash` |
| **특이사항** | 클래스 없음. 함수 정의만 추출 |
| **source/dot**: `command` 중 `source` 또는 `.`을 import로 감지 (Walker 확장) |
| **제한**: Bash AST는 정밀하지 않음 — 함수 선언/호출 수준만 |

---

## Walker 확장 포인트

현재 Walker는 LangSpec 기반으로 동작하지만, 일부 언어는 추가 로직이 필요하다.

### 12.A 공통 확장: Arrow Function / Anonymous Function 이름 추출

**대상 언어**: JavaScript, TypeScript (기존), Rust (클로저)

현재 Walker는 함수 선언의 `name` 필드를 사용. Arrow function (`const foo = () => {}`)은 `name` 필드가 없으므로 부모 `variable_declarator`에서 이름 추출 필요.

```
변경 파일: internal/parse/treesitter/walker.go
변경 내용: extractFunctionName() 메서드에 부모 노드 검사 로직 추가
```

- [ ] `TestWalker_ArrowFunctionName_JS` — `const foo = () => {}` → 노드명 "foo"
- [ ] `TestWalker_ArrowFunctionName_TS` — TypeScript arrow function도 동일

### 12.B 공통 확장: Attribute/Decorator 기반 테스트 감지

**대상 언어**: Rust (`#[test]`), C# (`[Test]`, `[Fact]`), Java (`@Test` — 기존 개선)

현재 TestPrefix만으로 테스트 감지. Attribute 기반 감지 추가.

```
변경 파일: internal/parse/treesitter/walker.go
변경 내용: isTestFunction() 메서드에 attribute 검사 로직 추가
LangSpec 확장: TestAttributes []string 필드 추가
```

```go
type LangSpec struct {
    // ... 기존 필드 ...
    TestPrefix      string
    TestAttributes  []string  // Phase 12 추가: ["test", "Test", "Fact"]
    PackageNodeType string
}
```

- [ ] `TestWalker_AttributeTest_Rust` — `#[test] fn test_foo()` → NodeKind=test
- [ ] `TestWalker_AttributeTest_CSharp` — `[Test] void TestFoo()` → NodeKind=test
- [ ] `TestWalker_AttributeTest_Java` — `@Test void testFoo()` → NodeKind=test (기존 TestPrefix와 병행)

### 12.C 공통 확장: impl/extension 블록 처리

**대상 언어**: Rust (`impl`), Swift (`extension`)

`impl Struct { fn method() {} }` → method는 Struct의 CONTAINS 자식.
`impl Trait for Struct` → IMPLEMENTS 엣지.

```
변경 파일: internal/parse/treesitter/walker.go
변경 내용: processImplBlock() 메서드 추가
LangSpec 확장: ImplTypes []string, ExtensionTypes []string 필드 추가
```

```go
type LangSpec struct {
    // ... 기존 필드 ...
    ImplTypes      []string  // Phase 12 추가: ["impl_item"]
    ExtensionTypes []string  // Phase 12 추가: ["extension_declaration"]
}
```

- [ ] `TestWalker_ImplBlock_Rust` — `impl Foo { fn bar() {} }` → bar는 Foo의 CONTAINS 자식
- [ ] `TestWalker_ImplTrait_Rust` — `impl Trait for Foo {}` → Foo → Trait IMPLEMENTS 엣지
- [ ] `TestWalker_Extension_Swift` — `extension Foo { func bar() {} }` → bar는 Foo의 CONTAINS 자식

---

## 테스트 픽스처

각 언어별 테스트 픽스처 파일이 필요하다. `internal/parse/treesitter/testdata/` 디렉토리에 생성.

| 언어 | 파일명 | 포함 내용 |
|------|--------|----------|
| JavaScript | `sample.js` | 함수, 클래스, import, arrow function, 테스트 |
| C | `sample.c`, `sample.h` | 함수 정의/선언, struct, #include |
| C++ | `sample.cpp` | 함수, class, struct, namespace, #include |
| Rust | `sample.rs` | fn, struct, enum, trait, impl, use, #[test] |
| C# | `sample.cs` | method, class, interface, using, namespace, [Test] |
| PHP | `sample.php` | function, class, interface, use, namespace |
| Swift | `sample.swift` | func, class, struct, protocol, import, extension |
| Scala | `sample.scala` | def, class, object, trait, import |
| Lua | `sample.lua` | function, local function, require |
| Bash | `sample.sh` | function, source |

---

## TDD 구현 계획

> **규칙**: 각 테스트는 `- [ ]` 체크박스로 표시. Red → Green → Refactor.
> 각 언어는 최소 4개 테스트: 함수 파싱, 클래스/구조체 파싱 (해당 시), import 파싱, 호출 감지.

### 12.0 구조적 변경 (Tidy First)

LangSpec 확장 + Registry에 신규 언어 등록 + Walker 확장 포인트 준비.

- [ ] `TestLangSpec_TestAttributes` — TestAttributes 필드 추가 후 기존 5개 언어 동작 유지
- [ ] `TestLangSpec_ImplTypes` — ImplTypes 필드 추가 후 기존 5개 언어 동작 유지
- [ ] `TestRegistry_AllLanguages` — 15개 언어 등록 확인

### 12.1 JavaScript

- [ ] `TestParseJS_Function` — `function foo() {}` → Function 노드
- [ ] `TestParseJS_ArrowFunction` — `const foo = () => {}` → Function 노드 (이름 "foo")
- [ ] `TestParseJS_Class` — `class Foo {}` → Class 노드
- [ ] `TestParseJS_Import` — `import { foo } from 'bar'` → IMPORTS_FROM 엣지
- [ ] `TestParseJS_Call` — `foo()` → CALLS 엣지
- [ ] `TestParseJS_Export` — `export function foo() {}` → Function 노드 (export 내 선언)

### 12.2 C

- [ ] `TestParseC_Function` — `void foo() {}` → Function 노드
- [ ] `TestParseC_Struct` — `struct Foo {}` → Class 노드
- [ ] `TestParseC_Include` — `#include "foo.h"` → IMPORTS_FROM 엣지
- [ ] `TestParseC_Call` — `foo()` → CALLS 엣지
- [ ] `TestParseC_HeaderDeclaration` — `.h` 파일의 함수 선언도 노드 생성

### 12.3 C++

- [ ] `TestParseCpp_Function` — `void foo() {}` → Function 노드
- [ ] `TestParseCpp_Class` — `class Foo {}` → Class 노드
- [ ] `TestParseCpp_Struct` — `struct Bar {}` → Class 노드
- [ ] `TestParseCpp_Namespace` — `namespace ns { void foo() {} }` → QualifiedName에 ns 포함
- [ ] `TestParseCpp_Include` — `#include <iostream>` → IMPORTS_FROM 엣지
- [ ] `TestParseCpp_Call` — `foo()` → CALLS 엣지

### 12.4 Rust

- [ ] `TestParseRust_Function` — `fn foo() {}` → Function 노드
- [ ] `TestParseRust_Struct` — `struct Foo {}` → Class 노드
- [ ] `TestParseRust_Enum` — `enum Bar {}` → Class 노드
- [ ] `TestParseRust_Trait` — `trait Baz {}` → Type(Interface) 노드
- [ ] `TestParseRust_ImplBlock` — `impl Foo { fn bar() {} }` → bar는 Foo의 CONTAINS 자식
- [ ] `TestParseRust_ImplTrait` — `impl Trait for Foo {}` → IMPLEMENTS 엣지
- [ ] `TestParseRust_Use` — `use std::io` → IMPORTS_FROM 엣지
- [ ] `TestParseRust_Call` — `foo()` → CALLS 엣지
- [ ] `TestParseRust_TestAttribute` — `#[test] fn test_foo() {}` → NodeKind=test

### 12.5 C#

- [ ] `TestParseCSharp_Method` — `void Foo() {}` → Function 노드
- [ ] `TestParseCSharp_Class` — `class Foo {}` → Class 노드
- [ ] `TestParseCSharp_Interface` — `interface IFoo {}` → Type 노드
- [ ] `TestParseCSharp_Using` — `using System;` → IMPORTS_FROM 엣지
- [ ] `TestParseCSharp_Call` — `Foo()` → CALLS 엣지
- [ ] `TestParseCSharp_Namespace` — `namespace Ns { class Foo {} }` → QualifiedName에 Ns 포함
- [ ] `TestParseCSharp_TestAttribute` — `[Test] void TestFoo() {}` → NodeKind=test

### 12.6 PHP

- [ ] `TestParsePHP_Function` — `function foo() {}` → Function 노드
- [ ] `TestParsePHP_Class` — `class Foo {}` → Class 노드
- [ ] `TestParsePHP_Interface` — `interface IFoo {}` → Type 노드
- [ ] `TestParsePHP_Use` — `use App\Models\User;` → IMPORTS_FROM 엣지
- [ ] `TestParsePHP_Call` — `foo()` → CALLS 엣지
- [ ] `TestParsePHP_Method` — `class Foo { function bar() {} }` → bar는 Foo의 CONTAINS 자식

### 12.7 Swift

- [ ] `TestParseSwift_Function` — `func foo() {}` → Function 노드
- [ ] `TestParseSwift_Class` — `class Foo {}` → Class 노드
- [ ] `TestParseSwift_Struct` — `struct Bar {}` → Class 노드
- [ ] `TestParseSwift_Protocol` — `protocol Baz {}` → Type 노드
- [ ] `TestParseSwift_Import` — `import Foundation` → IMPORTS_FROM 엣지
- [ ] `TestParseSwift_Call` — `foo()` → CALLS 엣지
- [ ] `TestParseSwift_Extension` — `extension Foo { func bar() {} }` → bar는 Foo의 CONTAINS 자식

### 12.8 Scala

- [ ] `TestParseScala_Function` — `def foo() = {}` → Function 노드
- [ ] `TestParseScala_Class` — `class Foo {}` → Class 노드
- [ ] `TestParseScala_Object` — `object Foo {}` → Class 노드 (싱글턴)
- [ ] `TestParseScala_Trait` — `trait Baz {}` → Type 노드
- [ ] `TestParseScala_Import` — `import scala.io._` → IMPORTS_FROM 엣지
- [ ] `TestParseScala_Call` — `foo()` → CALLS 엣지

### 12.9 Lua

- [ ] `TestParseLua_Function` — `function foo() end` → Function 노드
- [ ] `TestParseLua_LocalFunction` — `local function bar() end` → Function 노드
- [ ] `TestParseLua_Call` — `foo()` → CALLS 엣지
- [ ] `TestParseLua_Require` — `require("foo")` → IMPORTS_FROM 엣지

### 12.10 Bash

- [ ] `TestParseBash_Function` — `foo() { ... }` → Function 노드
- [ ] `TestParseBash_FunctionKeyword` — `function bar() { ... }` → Function 노드
- [ ] `TestParseBash_Call` — `foo` (명령어 호출) → CALLS 엣지
- [ ] `TestParseBash_Source` — `source ./lib.sh` → IMPORTS_FROM 엣지

### 12.11 Walker 확장 테스트 (12.A~C에서 정의한 것 포함)

이 테스트들은 12.A~C 섹션에서 이미 정의됨. 여기서는 중복 나열하지 않음.

- Arrow Function 이름 추출 (12.A): 2개 테스트
- Attribute 기반 테스트 감지 (12.B): 3개 테스트
- impl/extension 블록 (12.C): 3개 테스트

### 12.12 통합 테스트

- [ ] `TestRegistry_15Languages` — 15개 언어 모두 Registry에 등록, 확장자 조회 성공
- [ ] `TestWalker_MultiLanguageFile` — 같은 Walker로 Go, Python, JavaScript 파일 연속 파싱 성공
- [ ] `TestE2E_ParseMultiLangProject` — 여러 언어 파일이 섞인 디렉토리 파싱 → 각 언어별 노드 정상 생성
- [ ] `TestE2E_SearchAcrossLanguages` — 파싱 후 검색 시 모든 언어의 노드가 검색됨

---

## 구현 순서

```
12.0 구조적 변경 (LangSpec 확장, Registry 준비, Walker 확장 포인트)
  ↓
12.1 JavaScript     ← TypeScript grammar과 유사, 빠르게 구현
  ↓
12.2 C              ← 단순한 구조, 시스템 언어 기초
  ↓
12.3 C++            ← C 확장, namespace 추가
  ↓
12.4 Rust           ← impl/trait 처리 필요, Walker 확장
  ↓
12.5 C#             ← Java와 유사 구조
  ↓
12.6 PHP            ← 웹 언어, namespace/use 패턴
  ↓
12.7 Swift          ← protocol/extension 처리
  ↓
12.8 Scala          ← object/trait 처리
  ↓
12.9 Lua            ← 단순 구조 (클래스 없음)
  ↓
12.10 Bash          ← 최소 구조 (함수만)
  ↓
12.11 Walker 확장 (12.A~C 테스트 통과)
  ↓
12.12 통합 테스트
```

> **참고**: Tier 1 (JavaScript, C#, Rust) 완료 후 중간 검증 가능.
> Tier 2 (C, C++, PHP)는 독립적으로 병렬 구현 가능.

---

## 파일 변경 목록

| 파일 | 변경 내용 |
|------|----------|
| `internal/parse/treesitter/langspec.go` | LangSpec 확장 (TestAttributes, ImplTypes, ExtensionTypes) + 10개 신규 Spec 추가 |
| `internal/parse/treesitter/walker.go` | Arrow function 이름 추출, attribute 기반 테스트 감지, impl/extension 블록 처리 |
| `internal/parse/treesitter/walker_test.go` | 10개 언어 × ~5 테스트 + Walker 확장 8개 |
| `internal/parse/registry.go` | 10개 언어 확장자 등록 |
| `internal/parse/registry_test.go` | 15개 언어 등록 확인 테스트 |
| `internal/parse/treesitter/testdata/*.{js,c,cpp,rs,cs,php,swift,scala,lua,sh}` | 10개 테스트 픽스처 |
| `internal/mcp/handlers.go` | `parseProject` 핸들러의 확장자 필터에 신규 확장자 추가 |
| `go.mod` | 10개 grammar 의존성 추가 |

### go.mod 추가 의존성

```
github.com/smacker/go-tree-sitter/javascript
github.com/smacker/go-tree-sitter/c
github.com/smacker/go-tree-sitter/cpp
github.com/smacker/go-tree-sitter/rust
github.com/smacker/go-tree-sitter/csharp
github.com/smacker/go-tree-sitter/php
github.com/smacker/go-tree-sitter/swift
github.com/smacker/go-tree-sitter/scala
github.com/smacker/go-tree-sitter/lua
github.com/smacker/go-tree-sitter/bash
```

---

## 테스트 수 요약

| 서브-Phase | 테스트 수 |
|-----------|----------|
| 12.0 구조적 변경 | 3 |
| 12.1 JavaScript | 6 |
| 12.2 C | 5 |
| 12.3 C++ | 6 |
| 12.4 Rust | 9 |
| 12.5 C# | 7 |
| 12.6 PHP | 6 |
| 12.7 Swift | 7 |
| 12.8 Scala | 6 |
| 12.9 Lua | 4 |
| 12.10 Bash | 4 |
| 12.A Arrow Function | 2 |
| 12.B Attribute Test | 3 |
| 12.C impl/extension | 3 |
| 12.12 통합 테스트 | 4 |
| **합계** | **75** |

---

## 컨벤션 (기존 패턴 유지)

- LangSpec 변수명: `XXXSpec` (e.g., `JavaScriptSpec`, `CSpec`, `CppSpec`)
- Grammar 초기화: `sitter.NewLanguage(xxx.GetLanguage())` — smacker 패키지 사용
- 확장자 등록: `registry.Register(".ext", spec)` — 복수 확장자는 각각 등록
- 테스트 픽스처: `internal/parse/treesitter/testdata/sample.{ext}`
- Walker 테스트: `walker_test.go`에 `TestParseXXX_` 접두사로 추가
- QualifiedName: `filePath::name` (기존 Go와 동일 패턴)
- 테스트 DB: SQLite `:memory:`
- 로깅: `slog` 사용

---

## 향후 확장 가능 언어 (Phase 12 이후)

| 언어 | Grammar | 사유 |
|------|---------|------|
| Kotlin | 미제공 | smacker에 없음. 별도 tree-sitter-kotlin CGo 바인딩 필요 |
| Dart | 미제공 | smacker에 없음. Flutter 수요 있으나 바인딩 없음 |
| Elixir | `smacker/go-tree-sitter/elixir` | 수요 낮음, Tier 3 이하 |
| OCaml | `smacker/go-tree-sitter/ocaml` | 수요 낮음 |
| Vue | — | SFC 파싱 필요 (HTML + JS + CSS 혼합). 별도 전처리기 필요 |
| R | — | smacker에 없음 |
| Jupyter | — | JSON 파싱 + 셀 추출. 별도 전처리기 필요 |
