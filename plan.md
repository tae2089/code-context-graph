# Code Context Graph — 아키텍처 및 TDD 구현 계획

## 개요

**code-context-graph**는 코드베이스를 Tree-sitter로 파싱하여 구조적 노드/엣지를 지식 그래프로 저장하고,
blast-radius 분석 및 MCP 도구를 제공하는 로컬 코드 분석 도구이다.
Python 기반 [code-review-graph](https://github.com/tirth8205/code-review-graph)를 Go로 재구현하되,
다음을 추가/개선한다:

- **GORM ORM** 기반 다중 DB 지원 (SQLite, PostgreSQL, MySQL)
- **커스텀 어노테이션 시스템** — 비즈니스/AI 컨텍스트를 코드에 직접 기록하여 문서 생성 품질 향상
- **Tree-sitter 다중 언어 지원** — 처음부터 언어 레지스트리 기반 확장 구조

## 참조 프로젝트 분석 요약

### 핵심 모듈

| 모듈 | 역할 |
|------|------|
| `parser.py` (156KB) | Tree-sitter AST 파싱, 19개 언어, 노드/엣지 추출 |
| `graph.py` (38KB) | SQLite 그래프 저장소, BFS, impact radius |
| `incremental.py` | 파일 해시 기반 증분 파싱 |
| `changes.py` | Git diff 기반 변경 감지 |
| `flows.py` | 데이터 흐름 추적 |
| `communities.py` | 커뮤니티 탐지 (Louvain) |
| `search.py` | FTS5 전문 검색 |
| `main.py` | MCP 서버 엔트리포인트 |

### 데이터 모델

- **Node 종류**: File, Class, Function, Type, Test
- **Edge 종류**: CALLS, IMPORTS_FROM, INHERITS, IMPLEMENTS, CONTAINS, TESTED_BY, DEPENDS_ON, REFERENCES

### 참조 프로젝트의 한계 (개선 대상)

1. SQLite 전용 (FTS5, recursive CTE) → 다중 DB 추상화 필요
2. 어노테이션이 `NodeInfo.extra` dict에 비정규화 저장 → 정규화된 테이블로 분리
3. 단일 언어(Python) 구현 → Go로 재구현

---

## 패키지 구조

```
code-context-graph/
├── cmd/
│   └── ccg/
│       └── main.go                 # 엔트리포인트
├── internal/
│   ├── app/
│   │   └── bootstrap.go            # 앱 초기화, DI
│   ├── config/
│   │   └── config.go               # 설정 로드 (env, YAML)
│   ├── model/
│   │   ├── node.go                 # Node GORM 모델
│   │   ├── edge.go                 # Edge GORM 모델
│   │   ├── flow.go                 # Flow, FlowMembership 모델
│   │   ├── community.go           # Community, CommunityMembership 모델
│   │   ├── annotation.go          # Annotation, DocTag 모델
│   │   └── search.go              # SearchDocument 모델
│   ├── annotation/
│   │   ├── parser.go              # 어노테이션 태그 파서
│   │   ├── parser_test.go
│   │   ├── normalizer.go          # 언어별 주석 정규화
│   │   └── normalizer_test.go
│   ├── parse/
│   │   ├── service.go             # 파싱 오케스트레이션
│   │   ├── service_test.go
│   │   ├── registry.go            # 언어 레지스트리
│   │   ├── registry_test.go
│   │   ├── binder.go              # 어노테이션-노드 바인딩
│   │   ├── binder_test.go
│   │   └── treesitter/
│   │       ├── walker.go          # AST 워커
│   │       ├── walker_test.go
│   │       ├── langspec.go        # 언어별 AST 노드 매핑
│   │       └── langspec_test.go
│   ├── store/
│   │   ├── store.go               # GraphStore 인터페이스
│   │   ├── gormstore/
│   │   │   ├── gormstore.go       # GORM 구현체
│   │   │   └── gormstore_test.go
│   │   └── search/
│   │       ├── backend.go         # Search Backend 인터페이스
│   │       ├── sqlite.go          # SQLite FTS5
│   │       ├── postgres.go        # PostgreSQL tsvector+GIN
│   │       ├── mysql.go           # MySQL FULLTEXT
│   │       └── search_test.go
│   ├── analysis/
│   │   ├── impact/
│   │   │   ├── impact.go          # BFS blast-radius
│   │   │   └── impact_test.go
│   │   ├── flows/
│   │   │   ├── flows.go           # 데이터 흐름 추적
│   │   │   └── flows_test.go
│   │   └── incremental/
│   │       ├── incremental.go     # 증분 파싱
│   │       └── incremental_test.go
│   ├── mcp/
│   │   ├── server.go              # MCP 서버
│   │   ├── server_test.go
│   │   ├── handlers.go            # MCP 핸들러
│   │   └── handlers_test.go
│   └── testutil/
│       ├── fixture.go             # 테스트 픽스처 헬퍼
│       └── dbhelper.go            # 테스트용 DB 헬퍼
├── go.mod
├── go.sum
├── plan.md
└── README.md
```

---

## GORM 모델 정의

### Node

```go
type NodeKind string

const (
    NodeKindFile     NodeKind = "file"
    NodeKindClass    NodeKind = "class"
    NodeKindFunction NodeKind = "function"
    NodeKindType     NodeKind = "type"
    NodeKindTest     NodeKind = "test"
)

type Node struct {
    ID            uint     `gorm:"primaryKey"`
    QualifiedName string   `gorm:"uniqueIndex;size:512;not null"`
    Kind          NodeKind `gorm:"size:32;not null;index"`
    Name          string   `gorm:"size:256;not null"`
    FilePath      string   `gorm:"size:1024;not null;index"`
    StartLine     int      `gorm:"not null"`
    EndLine       int      `gorm:"not null"`
    Hash          string   `gorm:"size:64"`       // 증분 감지용
    Language      string   `gorm:"size:32;index"` // 소스 언어
    CreatedAt     time.Time
    UpdatedAt     time.Time

    Edges       []Edge      `gorm:"foreignKey:FromNodeID"`
    InEdges     []Edge      `gorm:"foreignKey:ToNodeID"`
    Annotation  *Annotation `gorm:"foreignKey:NodeID"`
}
```

### Edge

```go
type EdgeKind string

const (
    EdgeKindCalls       EdgeKind = "calls"
    EdgeKindImportsFrom EdgeKind = "imports_from"
    EdgeKindInherits    EdgeKind = "inherits"
    EdgeKindImplements  EdgeKind = "implements"
    EdgeKindContains    EdgeKind = "contains"
    EdgeKindTestedBy    EdgeKind = "tested_by"
    EdgeKindDependsOn   EdgeKind = "depends_on"
    EdgeKindReferences  EdgeKind = "references"
)

type Edge struct {
    ID          uint     `gorm:"primaryKey"`
    FromNodeID  uint     `gorm:"not null;index"`
    ToNodeID    uint     `gorm:"not null;index"`
    Kind        EdgeKind `gorm:"size:32;not null;index"`
    FilePath    string   `gorm:"size:1024"`
    Line        int
    Fingerprint string   `gorm:"uniqueIndex;size:128;not null"` // hash(kind+from+to+file+line)
    CreatedAt   time.Time

    FromNode Node `gorm:"foreignKey:FromNodeID"`
    ToNode   Node `gorm:"foreignKey:ToNodeID"`
}
```

### Annotation & DocTag

```go
type Annotation struct {
    ID        uint   `gorm:"primaryKey"`
    NodeID    uint   `gorm:"uniqueIndex;not null"`
    Summary   string `gorm:"size:1024"`  // 첫 번째 줄 (태그 없는 줄)
    Context   string `gorm:"size:2048"` // 두 번째 줄 (태그 없는 줄)
    RawText   string `gorm:"type:text"` // 원본 주석 전체
    CreatedAt time.Time
    UpdatedAt time.Time

    Node Node     `gorm:"foreignKey:NodeID"`
    Tags []DocTag `gorm:"foreignKey:AnnotationID"`
}

type TagKind string

const (
    TagParam      TagKind = "param"
    TagReturn     TagKind = "return"
    TagSee        TagKind = "see"
    TagIntent     TagKind = "intent"
    TagDomainRule TagKind = "domainRule"
    TagSideEffect TagKind = "sideEffect"
    TagMutates    TagKind = "mutates"
    TagRequires   TagKind = "requires"
    TagEnsures    TagKind = "ensures"
)

type DocTag struct {
    ID           uint    `gorm:"primaryKey"`
    AnnotationID uint    `gorm:"not null;index"`
    Kind         TagKind `gorm:"size:32;not null;index"`
    Name         string  `gorm:"size:128"` // @param의 파라미터 이름 등
    Value        string  `gorm:"type:text;not null"`
    Ordinal      int     `gorm:"not null"` // 동일 Kind 내 순서
    CreatedAt    time.Time

    Annotation Annotation `gorm:"foreignKey:AnnotationID"`
}
```

### Flow & Community

```go
type Flow struct {
    ID          uint   `gorm:"primaryKey"`
    Name        string `gorm:"size:256;not null"`
    Description string `gorm:"type:text"`
    CreatedAt   time.Time

    Members []FlowMembership `gorm:"foreignKey:FlowID"`
}

type FlowMembership struct {
    ID       uint `gorm:"primaryKey"`
    FlowID   uint `gorm:"not null;index"`
    NodeID   uint `gorm:"not null;index"`
    Ordinal  int  `gorm:"not null"`

    Flow Flow `gorm:"foreignKey:FlowID"`
    Node Node `gorm:"foreignKey:NodeID"`
}

type Community struct {
    ID        uint   `gorm:"primaryKey"`
    Label     string `gorm:"size:128;not null"`
    CreatedAt time.Time

    Members []CommunityMembership `gorm:"foreignKey:CommunityID"`
}

type CommunityMembership struct {
    ID          uint `gorm:"primaryKey"`
    CommunityID uint `gorm:"not null;index"`
    NodeID      uint `gorm:"not null;index"`

    Community Community `gorm:"foreignKey:CommunityID"`
    Node      Node      `gorm:"foreignKey:NodeID"`
}
```

### SearchDocument

```go
type SearchDocument struct {
    ID       uint   `gorm:"primaryKey"`
    NodeID   uint   `gorm:"uniqueIndex;not null"`
    Content  string `gorm:"type:text;not null"` // 검색용 통합 텍스트
    Language string `gorm:"size:32;index"`

    Node Node `gorm:"foreignKey:NodeID"`
}
```

---

## 인터페이스 정의

### parse.Parser

```go
// Parser는 소스 파일을 파싱하여 노드와 엣지를 추출한다.
type Parser interface {
    // Parse는 주어진 파일 내용을 파싱하여 노드/엣지 슬라이스를 반환한다.
    Parse(filePath string, content []byte) ([]model.Node, []model.Edge, error)

    // SupportedLanguages는 이 파서가 지원하는 언어 목록을 반환한다.
    SupportedLanguages() []string
}
```

### annotation.Parser

```go
// Parser는 주석 텍스트에서 어노테이션을 추출한다.
type Parser interface {
    // Parse는 정규화된 주석 텍스트를 파싱하여 Annotation을 반환한다.
    Parse(commentText string) (*model.Annotation, error)
}
```

### store.GraphStore

```go
// GraphStore는 그래프 데이터의 영속화를 담당한다.
type GraphStore interface {
    // 노드 CRUD
    UpsertNodes(ctx context.Context, nodes []model.Node) error
    GetNode(ctx context.Context, qualifiedName string) (*model.Node, error)
    GetNodesByFile(ctx context.Context, filePath string) ([]model.Node, error)
    DeleteNodesByFile(ctx context.Context, filePath string) error

    // 엣지 CRUD
    UpsertEdges(ctx context.Context, edges []model.Edge) error
    GetEdgesFrom(ctx context.Context, nodeID uint) ([]model.Edge, error)
    GetEdgesTo(ctx context.Context, nodeID uint) ([]model.Edge, error)
    DeleteEdgesByFile(ctx context.Context, filePath string) error

    // 어노테이션
    UpsertAnnotation(ctx context.Context, ann *model.Annotation) error
    GetAnnotation(ctx context.Context, nodeID uint) (*model.Annotation, error)

    // 트랜잭션
    WithTx(ctx context.Context, fn func(store GraphStore) error) error

    // 마이그레이션
    AutoMigrate() error
}
```

### search.Backend

```go
// Backend는 DB별 전문 검색 구현을 추상화한다.
type Backend interface {
    // Migrate는 검색에 필요한 DB 스키마(인덱스, 가상 테이블 등)를 생성한다.
    Migrate(db *gorm.DB) error

    // Rebuild는 전체 검색 인덱스를 재구축한다.
    Rebuild(ctx context.Context, db *gorm.DB) error

    // Query는 검색어로 노드를 검색하여 관련도 순으로 반환한다.
    Query(ctx context.Context, db *gorm.DB, query string, limit int) ([]model.Node, error)
}
```

### analysis.Analyzer

```go
// Analyzer는 그래프 분석 기능을 제공한다.
type Analyzer interface {
    // ImpactRadius는 주어진 노드에서 depth 깊이까지 영향받는 노드를 BFS로 탐색한다.
    ImpactRadius(ctx context.Context, nodeID uint, depth int) ([]model.Node, error)

    // TraceFlow는 주어진 노드의 데이터 흐름을 추적한다.
    TraceFlow(ctx context.Context, nodeID uint) (*model.Flow, error)
}
```

---

## 커스텀 어노테이션 시스템

### 태그 사양

#### 표준 태그

| 태그 | 설명 | 예시 |
|------|------|------|
| _(첫 번째 줄, 태그 없음)_ | 한 줄 요약 → `Summary` | `사용자 인증을 수행한다` |
| _(두 번째 줄, 태그 없음)_ | 호출 컨텍스트/흐름 → `Context` | `로그인 핸들러에서 호출됨` |
| `@param <name> <desc>` | 파라미터 설명 (반복 가능) | `@param username 사용자 로그인 ID` |
| `@return <desc>` | 반환값 설명 | `@return 인증된 사용자 토큰` |
| `@see <ref>` | 관련 함수/호출처 (반복 가능) | `@see LoginHandler.Handle` |

#### 확장 태그 (AI & 비즈니스 컨텍스트)

| 태그 | 설명 | 예시 |
|------|------|------|
| `@intent` | 이 함수를 호출하는 목적/의도 | `@intent 사용자 세션 생성 전 자격 검증` |
| `@domainRule` | 비즈니스 정책/도메인 규칙 | `@domainRule 5회 실패 시 계정 잠금` |
| `@sideEffect` | 실행 시 부작용 | `@sideEffect 감사 로그 기록` |
| `@mutates` | 이 함수가 변경하는 필드/상태 | `@mutates user.LastLoginAt, session.Token` |
| `@requires` | 실행 전 사전조건 | `@requires user.IsActive == true` |
| `@ensures` | 실행 후 사후조건 | `@ensures session != nil` |

### 파싱 규칙

1. 주석 블록의 **첫 번째 비공백 줄** (태그로 시작하지 않음) → `Summary`
2. **두 번째 비공백 줄** (태그로 시작하지 않음) → `Context`
3. `@` 로 시작하는 줄 → 태그 파싱
   - `@param`: `@param <name> <description>` (name과 description 분리)
   - 기타: `@<tag> <value>` (전체가 value)
4. 태그는 여러 줄에 걸칠 수 있음 (다음 `@` 또는 블록 끝까지)
5. 언어별 주석 접두사 (`//`, `#`, `*`, `--` 등)는 normalizer가 제거

### 예시

```go
// AuthenticateUser는 사용자 자격 증명을 검증한다.
// 로그인 API 핸들러에서 호출되며, 세션 생성 전 단계이다.
//
// @param username 사용자의 로그인 ID
// @param password 해시되지 않은 평문 비밀번호
// @return 인증 성공 시 JWT 토큰, 실패 시 error
// @intent 사용자 세션 생성 전 자격 검증
// @domainRule 5회 연속 실패 시 30분간 계정 잠금
// @sideEffect 로그인 시도를 audit_log 테이블에 기록
// @mutates user.FailedAttempts, user.LockedUntil
// @requires user.IsActive == true
// @ensures err == nil이면 token은 유효한 JWT
// @see LoginHandler.Handle
// @see SessionManager.Create
func AuthenticateUser(username, password string) (string, error) {
```

---

## 다중 DB 전략

### GORM 공통 스키마

- 모든 모델은 표준 GORM 태그만 사용 (`gorm:"..."`)
- `AutoMigrate()`로 테이블 생성 — SQLite, PostgreSQL, MySQL 모두 동일 코드

### DB별 검색 (search.Backend)

| DB | 구현 | 인덱스 방식 |
|----|------|-------------|
| SQLite | `sqlite.go` | FTS5 가상 테이블 + 트리거 |
| PostgreSQL | `postgres.go` | `tsvector` 컬럼 + GIN 인덱스 |
| MySQL | `mysql.go` | `FULLTEXT` 인덱스 |

### BFS / Blast-radius

- SQL recursive CTE 대신 **Go 코드에서 frontier 배치 쿼리**로 구현
- DB 독립적: `SELECT * FROM edges WHERE from_node_id IN (?)` 반복

---

## TDD 구현 계획 — 7단계

> **규칙**: 각 테스트는 `- [ ]` 체크박스로 표시. 테스트를 먼저 작성(Red), 최소 코드로 통과(Green), 리팩터링(Refactor).

### Phase 1: 어노테이션 파서 (순수 문자열 처리, 외부 의존성 없음)

#### 1.1 빈 입력 처리
- [x] `TestParse_EmptyString` — 빈 문자열 입력 시 빈 Annotation 반환 (Summary="", Context="", Tags 비어있음)
- [x] `TestParse_WhitespaceOnly` — 공백만 있는 입력 시 빈 Annotation 반환

#### 1.2 Summary/Context 추출
- [x] `TestParse_SummaryOnly` — 한 줄짜리 주석에서 Summary만 추출
- [x] `TestParse_SummaryAndContext` — 두 줄 주석에서 Summary + Context 추출
- [x] `TestParse_SummaryAndContext_WithBlankLine` — Summary와 Context 사이에 빈 줄이 있어도 정상 추출
- [x] `TestParse_ThreeNonTagLines` — 세 번째 비태그 줄은 Context에 이어붙기 (또는 무시 — 정책 결정)

#### 1.3 표준 태그 파싱
- [x] `TestParse_SingleParam` — `@param name desc` 파싱 → TagKind=param, Name="name", Value="desc"
- [x] `TestParse_MultipleParams` — 여러 `@param` 태그가 Ordinal 순서대로 파싱
- [x] `TestParse_Return` — `@return desc` 파싱 → TagKind=return, Value="desc"
- [x] `TestParse_See` — `@see ref` 파싱 → TagKind=see, Value="ref"
- [x] `TestParse_MultipleSee` — 여러 `@see` 태그 파싱

#### 1.4 확장 태그 파싱
- [x] `TestParse_Intent` — `@intent value` 파싱
- [x] `TestParse_DomainRule` — `@domainRule value` 파싱
- [x] `TestParse_SideEffect` — `@sideEffect value` 파싱
- [x] `TestParse_Mutates` — `@mutates value` 파싱
- [x] `TestParse_Requires` — `@requires value` 파싱
- [x] `TestParse_Ensures` — `@ensures value` 파싱

#### 1.5 멀티라인 태그
- [x] `TestParse_MultiLineParam` — `@param` 뒤 다음 줄이 `@`로 시작하지 않으면 값에 이어붙기
- [x] `TestParse_MultiLineIntent` — `@intent` 다음 줄 이어붙기

#### 1.6 복합 주석 블록
- [x] `TestParse_FullAnnotation` — Summary + Context + 모든 태그 종류가 포함된 전체 주석 블록 파싱
- [x] `TestParse_RawTextPreserved` — 파싱 후 RawText 필드에 원본 전체가 보존됨

#### 1.7 엣지 케이스
- [x] `TestParse_UnknownTag` — 알 수 없는 `@foo` 태그는 무시하거나 경고 (에러 아님)
- [x] `TestParse_TagWithoutValue` — `@return` 뒤에 값 없이 끝나면 빈 Value
- [x] `TestParse_MixedIndentation` — 탭/스페이스 혼용 시 정상 파싱

---

### Phase 2: 주석 정규화 및 선언 바인딩

#### 2.1 주석 정규화 (Normalizer)
- [x] `TestNormalize_GoSlashSlash` — Go `//` 주석 접두사 제거
- [x] `TestNormalize_GoBlockComment` — Go `/* ... */` 블록 주석 정규화
- [x] `TestNormalize_PythonHash` — Python `#` 주석 접두사 제거
- [x] `TestNormalize_PythonDocstring` — Python `"""..."""` 독스트링 정규화
- [x] `TestNormalize_JavaDocComment` — Java `/** ... */` JavaDoc 정규화 (`*` 접두사 제거)
- [x] `TestNormalize_CppSlashSlash` — C++ `//` 주석 정규화
- [x] `TestNormalize_RubyHash` — Ruby `#` 주석 정규화
- [x] `TestNormalize_EmptyComment` — 빈 주석 블록 → 빈 문자열 반환

#### 2.2 언어 레지스트리 (LanguageSpec)
- [x] `TestRegistry_RegisterAndLookup` — 언어 등록 후 조회
- [x] `TestRegistry_LookupByExtension` — 파일 확장자로 언어 조회 (`.go` → Go)
- [x] `TestRegistry_UnknownExtension` — 미등록 확장자 → error 또는 nil
- [x] `TestRegistry_DuplicateRegister` — 동일 언어 중복 등록 시 덮어쓰기 또는 에러

#### 2.3 어노테이션-노드 바인딩 (Binder)
- [x] `TestBinder_FunctionWithPrecedingComment` — 함수 선언 직전 주석을 해당 함수 노드에 바인딩
- [x] `TestBinder_ClassWithPrecedingComment` — 클래스/구조체 선언 직전 주석 바인딩
- [x] `TestBinder_NoComment` — 주석 없는 선언 → 어노테이션 nil
- [x] `TestBinder_CommentNotAdjacent` — 주석과 선언 사이에 빈 줄이 N개 이상이면 바인딩 안 함 (정책)
- [x] `TestBinder_MultipleDeclarations` — 파일 내 여러 선언에 각각 주석 바인딩

---

### Phase 3: Tree-sitter 파서 코어

#### 3.1 기본 파싱 (Go 언어 먼저)
- [x] `TestParseGo_EmptyFile` — 빈 Go 파일 → 빈 노드/엣지 슬라이스
- [x] `TestParseGo_SingleFunction` — 단일 함수 선언 → Function 노드 1개
- [x] `TestParseGo_FunctionWithParams` — 파라미터 있는 함수 → Node에 정보 포함
- [x] `TestParseGo_SingleStruct` — 구조체 선언 → Class 노드 (또는 Type 노드)
- [x] `TestParseGo_Interface` — 인터페이스 선언 → Type 노드
- [x] `TestParseGo_MethodOnStruct` — 메서드 → Function 노드 + CONTAINS 엣지

#### 3.2 엣지 추출 (Go)
- [x] `TestParseGo_FunctionCall` — 함수 호출 → CALLS 엣지
- [x] `TestParseGo_Import` — import 문 → IMPORTS_FROM 엣지
- [x] `TestParseGo_InterfaceImplementation` — 인터페이스 구현 → IMPLEMENTS 엣지
- [x] `TestParseGo_StructEmbedding` — 구조체 임베딩 → INHERITS 엣지

#### 3.3 파일 노드
- [x] `TestParseGo_FileNode` — 파일 자체가 File 노드로 생성
- [x] `TestParseGo_ContainsEdges` — File → Function/Class에 CONTAINS 엣지

#### 3.4 QualifiedName 생성
- [x] `TestParseGo_QualifiedName_Function` — `package.FunctionName` 형식
- [x] `TestParseGo_QualifiedName_Method` — `package.StructName.MethodName` 형식
- [x] `TestParseGo_QualifiedName_NestedType` — 중첩 타입의 정규화된 이름

#### 3.5 다중 언어 확장
- [x] `TestParsePython_SingleFunction` — Python 함수 파싱
- [x] `TestParsePython_Class` — Python 클래스 파싱
- [x] `TestParseTypeScript_Function` — TypeScript 함수 파싱
- [x] `TestParseTypeScript_Class` — TypeScript 클래스 파싱
- [x] `TestParseJava_Class` — Java 클래스 파싱
- [x] `TestParseRuby_Method` — Ruby 메서드 파싱

#### 3.6 테스트 감지
- [x] `TestParseGo_TestFunction` — `Test` 접두사 함수 → NodeKind=test
- [x] `TestParsePython_TestFunction` — `test_` 접두사 함수 → NodeKind=test
- [x] `TestParseGo_TestedBy` — 테스트 함수 → 대상 함수에 TESTED_BY 엣지

---

### Phase 4: GORM 저장소

#### 4.1 마이그레이션 (SQLite 먼저)
- [x] `TestAutoMigrate_SQLite` — SQLite DB에 모든 테이블 생성 확인
- [-] `TestAutoMigrate_Postgres` — PostgreSQL DB에 모든 테이블 생성 확인 (CI 전용, 로컬 스킵)
- [-] `TestAutoMigrate_MySQL` — MySQL DB에 모든 테이블 생성 확인 (CI 전용, 로컬 스킵)

#### 4.2 노드 CRUD
- [x] `TestUpsertNodes_Insert` — 새 노드 삽입
- [x] `TestUpsertNodes_Update` — 동일 QualifiedName 노드 업데이트 (Hash 변경)
- [x] `TestGetNode_ByQualifiedName` — QualifiedName으로 노드 조회
- [x] `TestGetNode_NotFound` — 없는 노드 조회 시 nil 반환
- [x] `TestGetNodesByFile` — 파일 경로로 해당 파일의 모든 노드 조회
- [x] `TestDeleteNodesByFile` — 파일 삭제 시 해당 파일 노드 전부 삭제

#### 4.3 엣지 CRUD
- [x] `TestUpsertEdges_Insert` — 새 엣지 삽입
- [x] `TestUpsertEdges_Dedup` — 동일 Fingerprint 엣지 중복 삽입 시 무시/업데이트
- [x] `TestGetEdgesFrom` — 출발 노드 기준 엣지 조회
- [x] `TestGetEdgesTo` — 도착 노드 기준 엣지 조회
- [x] `TestDeleteEdgesByFile` — 파일 삭제 시 해당 파일 엣지 전부 삭제

#### 4.4 어노테이션 CRUD
- [x] `TestUpsertAnnotation_Insert` — 새 어노테이션 + DocTag 삽입
- [x] `TestUpsertAnnotation_Update` — 기존 어노테이션 업데이트 (태그 교체)
- [x] `TestGetAnnotation` — 노드 ID로 어노테이션 + 태그 조회
- [x] `TestGetAnnotation_WithTags` — 어노테이션 조회 시 DocTag가 Preload되는지 확인

#### 4.5 트랜잭션
- [x] `TestWithTx_Success` — 트랜잭션 내 노드+엣지 삽입 성공 → 커밋
- [x] `TestWithTx_Rollback` — 트랜잭션 내 에러 → 롤백, DB 변경 없음

#### 4.6 Cascade 삭제
- [x] `TestDeleteNode_CascadeEdges` — 노드 삭제 시 관련 엣지 cascade 삭제
- [x] `TestDeleteNode_CascadeAnnotation` — 노드 삭제 시 어노테이션 + DocTag cascade 삭제

---

### Phase 5: 그래프 분석

#### 5.1 Impact Radius (BFS)
- [x] `TestImpactRadius_Depth0` — depth 0 → 자기 자신만 반환
- [x] `TestImpactRadius_Depth1` — depth 1 → 직접 연결된 노드
- [x] `TestImpactRadius_Depth2` — depth 2 → 2홉 거리까지
- [x] `TestImpactRadius_Cycle` — 순환 그래프에서 무한루프 방지
- [x] `TestImpactRadius_Disconnected` — 연결 안 된 노드 → 자기 자신만
- [x] `TestImpactRadius_LargeGraph` — 100+ 노드 그래프에서 성능 확인

#### 5.2 Flow 추적
- [x] `TestTraceFlow_SimpleChain` — A→B→C 호출 체인 추적
- [x] `TestTraceFlow_Branch` — A→B, A→C 분기 추적
- [x] `TestTraceFlow_Merge` — B→D, C→D 합류 추적
- [x] `TestTraceFlow_NoEdges` — 엣지 없는 노드 → 단일 노드 Flow

#### 5.3 증분 파싱
- [x] `TestIncremental_NewFile` — 새 파일 추가 시 파싱 및 저장
- [x] `TestIncremental_UnchangedFile` — Hash 동일 파일 → 스킵
- [x] `TestIncremental_ModifiedFile` — Hash 변경 파일 → 재파싱 후 업데이트
- [x] `TestIncremental_DeletedFile` — 삭제된 파일 → 노드/엣지 제거

---

### Phase 6: 검색 (DB별 방언)

#### 6.1 SQLite FTS5
- [x] `TestSQLiteFTS_Migrate` — FTS5 가상 테이블 생성 확인
- [x] `TestSQLiteFTS_Rebuild` — SearchDocument → FTS5 인덱스 재구축
- [x] `TestSQLiteFTS_Query` — 검색어 매칭 확인
- [x] `TestSQLiteFTS_QueryNoResults` — 매칭 없는 검색어 → 빈 결과
- [x] `TestSQLiteFTS_Ranking` — 관련도 순 정렬 확인

#### 6.2 PostgreSQL tsvector (CI/선택적)
- [x] `TestPostgresFTS_Migrate` — tsvector 컬럼 + GIN 인덱스 생성
- [x] `TestPostgresFTS_Rebuild` — 인덱스 재구축
- [x] `TestPostgresFTS_Query` — ts_query 검색

#### 6.3 MySQL FULLTEXT (CI/선택적)
- [x] `TestMySQLFTS_Migrate` — FULLTEXT 인덱스 생성
- [x] `TestMySQLFTS_Rebuild` — 인덱스 재구축
- [x] `TestMySQLFTS_Query` — MATCH AGAINST 검색

---

### Phase 7: MCP 서버 및 E2E 통합

#### 7.1 MCP 서버 기본
- [x] `TestMCPServer_Start` — 서버 시작 및 정상 리스닝
- [x] `TestMCPServer_ListTools` — 도구 목록 반환

#### 7.2 MCP 핸들러
- [x] `TestHandler_ParseProject` — 프로젝트 파싱 도구 호출
- [x] `TestHandler_GetNode` — 노드 조회 도구
- [x] `TestHandler_GetImpactRadius` — blast-radius 도구
- [x] `TestHandler_Search` — 검색 도구
- [x] `TestHandler_GetAnnotation` — 어노테이션 조회 도구
- [x] `TestHandler_TraceFlow` — 흐름 추적 도구

#### 7.3 E2E 통합 테스트
- [x] `TestE2E_ParseAndQuery` — 실제 Go 파일 파싱 → DB 저장 → 노드 조회
- [x] `TestE2E_ParseWithAnnotation` — 어노테이션 포함 Go 파일 파싱 → 어노테이션 조회
- [x] `TestE2E_IncrementalReparse` — 파일 수정 후 증분 재파싱
- [x] `TestE2E_BlastRadius` — 파싱 → blast-radius 분석 결과 검증
- [x] `TestE2E_FullTextSearch` — 파싱 → 전문 검색 결과 검증

---

### Phase 8: CLI 서브커맨드

#### 8.0 구조적 변경 — App 추출 (Tidy First: 행위 변경 없이 구조만 분리)
- [x] `TestApp_UnknownCommand` — 알 수 없는 서브커맨드 → 에러 메시지 + 사용법 출력
- [x] `TestApp_NoCommand` — 서브커맨드 없이 실행 → 사용법 출력
- [x] `TestApp_ServeCommand` — `serve` 서브커맨드 → 기존 MCP stdio 서버 동작 유지

#### 8.1 build 커맨드
- [x] `TestBuildCommand_ParsesDirectory` — `ccg build <dir>` → 디렉토리 재귀 탐색, Go 파일 파싱, 노드/엣지 DB 저장
- [x] `TestBuildCommand_DefaultCurrentDir` — 인자 없으면 현재 디렉토리 사용
- [x] `TestBuildCommand_SkipsDotGitVendor` — `.git`, `vendor`, `node_modules` 디렉토리 스킵
- [x] `TestBuildCommand_ReportsStats` — 완료 시 파일 수, 노드 수, 엣지 수 출력

#### 8.2 update 커맨드 (증분)
- [x] `TestUpdateCommand_IncrementalSync` — `ccg update <dir>` → 변경된 파일만 재파싱
- [x] `TestUpdateCommand_ReportsAddedModifiedDeleted` — added/modified/skipped/deleted 통계 출력

#### 8.3 status 커맨드
- [x] `TestStatusCommand_ShowsStats` — `ccg status` → 총 노드/엣지/파일 수 출력
- [x] `TestStatusCommand_ShowsKindBreakdown` — 노드 종류별, 엣지 종류별 카운트 출력
- [x] `TestStatusCommand_EmptyDB` — 빈 DB → "No data" 메시지

#### 8.4 search 커맨드
- [x] `TestSearchCommand_FindsResults` — `ccg search <query>` → 매칭 노드 출력
- [x] `TestSearchCommand_NoResults` — 매칭 없으면 "No results" 메시지
- [x] `TestSearchCommand_LimitFlag` — `--limit N` 플래그 동작

#### 8.5 serve 커맨드 (기존 로직 이동)
- [x] `TestServeCommand_AcceptsDBFlags` — `--db`, `--dsn` 플래그 파싱 확인

---

### Phase 9: 분석 기능 (Analysis Features)

> 모든 분석 서비스는 `type Service struct { db *gorm.DB }` 패턴을 따른다.
> GraphStore 인터페이스를 확장하지 않고, `*gorm.DB`를 직접 주입한다.
> 테스트는 SQLite `:memory:` + `gormlogger.Discard`로 구성한다.
> DB 쿼리는 GORM builder (joins, subqueries) 사용 — raw SQL 금지.

#### 9.0 Community 모델 추가

`internal/model/community.go` — `Community` + `CommunityMembership` 구조체.
AutoMigrate에 추가 필요.

- [x] `TestCommunityModel_AutoMigrate` — Community, CommunityMembership 테이블 생성 확인

#### 9.1 analysis/query — 미리 정의된 그래프 쿼리

`internal/analysis/query/service.go` — `Service` 구조체, `New(db *gorm.DB)`.
각 메서드는 `(ctx context.Context, nodeID uint) ([]model.Node, error)` 시그니처.
`FileSummary`는 `(ctx, filePath) (*FileSummary, error)`.

- [x] `TestCallersOf_ReturnsCallingNodes` — A→B calls 엣지가 있으면 CallersOf(B) → [A]
- [x] `TestCallersOf_NoCallers` — 호출자 없으면 빈 슬라이스
- [x] `TestCalleesOf_ReturnsCalledNodes` — A→B calls 엣지가 있으면 CalleesOf(A) → [B]
- [x] `TestCalleesOf_NoCallees` — 피호출자 없으면 빈 슬라이스
- [x] `TestImportsOf_ReturnsImportedNodes` — A→B imports_from 엣지 → ImportsOf(A) → [B]
- [x] `TestImportersOf_ReturnsImportingNodes` — A→B imports_from 엣지 → ImportersOf(B) → [A]
- [x] `TestChildrenOf_ReturnsContainedNodes` — A→B contains 엣지 → ChildrenOf(A) → [B]
- [x] `TestTestsFor_ReturnsTestNodes` — T→F tested_by 엣지 → TestsFor(F) → [T]
- [x] `TestInheritorsOf_ReturnsInheritingNodes` — C→P inherits 엣지 → InheritorsOf(P) → [C]
- [x] `TestFileSummary_ReturnsNodesByKind` — 파일 내 노드를 Kind별로 그룹핑 (functions, classes, types 카운트)
- [x] `TestFileSummary_FileNotFound` — 존재하지 않는 파일 → 빈 FileSummary (에러 아님)

#### 9.2 analysis/largefunc — 대형 함수 탐지

`internal/analysis/largefunc/service.go` — `Service` 구조체.
`Find(ctx, threshold int) ([]model.Node, error)` — `end_line - start_line + 1 > threshold`인 function/test 노드 반환.

- [x] `TestFind_AboveThreshold` — 50줄 함수, threshold=30 → 반환됨
- [x] `TestFind_BelowThreshold` — 10줄 함수, threshold=30 → 반환 안됨
- [x] `TestFind_ExactThreshold` — 정확히 threshold 줄 → 반환 안됨 (초과만)
- [x] `TestFind_OnlyFunctionKinds` — class/type 노드는 threshold 초과해도 무시, function/test만 반환
- [x] `TestFind_OrderByLineCount` — 결과가 줄 수 내림차순 정렬

#### 9.3 analysis/deadcode — 미사용 코드 탐지

`internal/analysis/deadcode/service.go` — `Service` 구조체.
`Find(ctx, opts Options) ([]model.Node, error)` — incoming edge가 0인 노드.
`Options { Kinds []model.NodeKind; FilePattern string }` — 필터.

- [x] `TestFind_NoIncomingEdges` — 호출받지 않는 함수 → dead code로 반환
- [x] `TestFind_HasIncomingEdges` — 호출받는 함수 → 반환 안됨
- [x] `TestFind_FilterByKind` — Kinds=[function]이면 class/type 제외
- [x] `TestFind_FilterByFilePattern` — FilePattern="internal/" → 해당 경로만 포함
- [x] `TestFind_ExcludesFileNodes` — file 노드는 항상 제외 (file은 dead code 아님)
- [x] `TestFind_ExcludesTestNodes` — test 노드는 항상 제외 (테스트는 dead code 아님)

#### 9.4 analysis/community — 커뮤니티 탐지

`internal/analysis/community/service.go` — `Builder` 구조체.
디렉토리 기반 그룹핑 전략: `FilePath`에서 `Depth` 수준까지 접두사 추출.
`Rebuild(ctx, Config) ([]Stats, error)` — 전체 교체 트랜잭션.
`Stats { Community model.Community; NodeCount, InternalEdges, ExternalEdges int64; Cohesion float64 }`.
Cohesion = InternalEdges / (InternalEdges + ExternalEdges). 엣지 없으면 0.0.

- [x] `TestRebuild_GroupsByDirectory` — `a/x.go`, `a/y.go`, `b/z.go` → 2개 커뮤니티 (a, b)
- [x] `TestRebuild_DepthConfig` — Depth=2면 `a/b/x.go`, `a/c/y.go` → 2개 커뮤니티 (a/b, a/c)
- [x] `TestRebuild_Depth1` — Depth=1이면 `a/b/x.go`, `a/c/y.go` → 1개 커뮤니티 (a)
- [x] `TestRebuild_CohesionScore` — 커뮤니티 내부 엣지 3, 외부 엣지 1 → Cohesion = 0.75
- [x] `TestRebuild_NoEdges` — 엣지 없는 커뮤니티 → Cohesion = 0.0
- [x] `TestRebuild_ReplacesPrevious` — 두 번 호출하면 이전 커뮤니티 삭제 후 재생성
- [x] `TestRebuild_MembershipLinks` — 각 노드가 정확히 하나의 커뮤니티에 속함

#### 9.5 analysis/coverage — 테스트 커버리지 분석

`internal/analysis/coverage/service.go` — `Service` 구조체.
`tested_by` 엣지 방향 확인: `FromNode=test → ToNode=function` (test가 function을 테스트함).
커버리지 = 해당 스코프에서 `tested_by` incoming 엣지가 1개 이상인 function 수 / 전체 function 수.

`ByFile(ctx, filePath) (*FileCoverage, error)` — 파일별 커버리지.
`ByCommunity(ctx, communityID uint) (*CommunityCoverage, error)` — 커뮤니티별 커버리지.

`FileCoverage { FilePath string; Total, Tested int; Ratio float64 }`.
`CommunityCoverage { CommunityID uint; Label string; Total, Tested int; Ratio float64 }`.

- [x] `TestByFile_AllTested` — 파일 내 모든 함수에 tested_by 엣지 → Ratio = 1.0
- [x] `TestByFile_NoneTested` — tested_by 엣지 없음 → Ratio = 0.0
- [x] `TestByFile_PartialCoverage` — 2/3 함수 테스트됨 → Ratio ≈ 0.667
- [x] `TestByFile_NoFunctions` — 파일에 함수 없음 → Total=0, Ratio=0.0
- [x] `TestByCommunity_AggregatesFiles` — 커뮤니티 내 여러 파일의 함수 합산
- [x] `TestByCommunity_InvalidID` — 존재하지 않는 커뮤니티 → 에러 반환

#### 9.6 analysis/coupling — 아키텍처 결합도

`internal/analysis/coupling/service.go` — `Service` 구조체.
커뮤니티 간 엣지 분석. 한 커뮤니티에서 다른 커뮤니티로의 엣지 수를 집계.

`Analyze(ctx) ([]CouplingPair, error)` — 모든 커뮤니티 쌍의 결합도.
`CouplingPair { FromCommunity, ToCommunity string; EdgeCount int64; Strength float64 }`.
Strength = EdgeCount / max(EdgeCount across all pairs). 쌍이 없으면 0.0.

- [x] `TestAnalyze_TwoCommunities` — 커뮤니티 A→B 엣지 5개 → CouplingPair 반환
- [x] `TestAnalyze_NoCrossCommunityEdges` — 커뮤니티 내부 엣지만 → 빈 결과
- [x] `TestAnalyze_Strength` — A→B 10개, A→C 5개 → A→B Strength=1.0, A→C Strength=0.5
- [x] `TestAnalyze_BidirectionalCounting` — A→B 3개, B→A 2개 → 별도의 CouplingPair 2개
- [x] `TestAnalyze_NoCommunities` — 커뮤니티 없으면 빈 결과

#### 9.7 analysis/changes — Git 변경 탐지

`internal/analysis/changes/service.go` — `Service` 구조체 + `GitClient` 인터페이스.
`GitClient` mock으로 테스트. 실제 구현은 `os/exec`.

`GitClient` 인터페이스:
```go
type GitClient interface {
    ChangedFiles(ctx context.Context, repoDir, baseRef string) ([]string, error)
    DiffHunks(ctx context.Context, repoDir, baseRef string, paths []string) ([]Hunk, error)
}
type Hunk struct { FilePath string; StartLine, EndLine int }
```

`Analyze(ctx, repoDir, baseRef string) ([]RiskEntry, error)` — 변경된 함수 + 리스크 점수.
`RiskEntry { Node model.Node; HunkCount int; RiskScore float64 }`.
RiskScore = HunkCount × (node의 outgoing edge 수 + 1). 영향 범위 기반.

- [x] `TestAnalyze_ChangedFunction` — 변경된 라인이 함수 범위 내 → RiskEntry 반환
- [x] `TestAnalyze_NoOverlap` — 변경된 라인이 어떤 함수 범위에도 안 겹침 → 빈 결과
- [x] `TestAnalyze_MultipleHunks` — 한 함수에 hunk 3개 겹침 → HunkCount=3
- [x] `TestAnalyze_RiskScoreCalculation` — HunkCount=2, outgoing edges=3 → RiskScore=8.0
- [x] `TestAnalyze_EmptyDiff` — 변경 파일 없음 → 빈 결과
- [x] `TestGitClient_ChangedFiles` — 실제 os/exec 테스트 (git diff --name-only)
- [x] `TestGitClient_DiffHunks` — 실제 os/exec 테스트 (git diff -U0)

---

### Phase 10: MCP 프롬프트 (Prompt Templates)

> MCP 프롬프트는 LLM이 호출할 수 있는 사전 정의된 워크플로우 템플릿이다.
> 각 프롬프트는 기존 분석 서비스(Phase 5, 9)를 조합하여 컨텍스트 메시지를 생성한다.
> 핸들러는 `internal/mcp/prompts.go`에, 테스트는 `internal/mcp/prompts_test.go`에 작성한다.
> `server.go`에서 `server.WithPromptCapabilities(true)` 추가 및 `srv.AddPrompts(...)` 등록.

#### 10.0 프롬프트 인프라 설정

`server.go` — `WithPromptCapabilities(true)` 추가, `Deps`에 분석 서비스 인터페이스 추가.
`prompts.go` — `promptHandlers` 구조체 + 공통 헬퍼.

Deps 확장:
```go
type ChangesAnalyzer interface {
    Analyze(ctx context.Context, repoDir, baseRef string) ([]changes.RiskEntry, error)
}
type CoverageAnalyzer interface {
    ByFile(ctx context.Context, filePath string) (*coverage.FileCoverage, error)
    ByCommunity(ctx context.Context, communityID uint) (*coverage.CommunityCoverage, error)
}
type CommunityBuilder interface {
    Rebuild(ctx context.Context, cfg community.Config) ([]community.Stats, error)
}
type CouplingAnalyzer interface {
    Analyze(ctx context.Context) ([]coupling.CouplingPair, error)
}
type DeadcodeAnalyzer interface {
    Find(ctx context.Context, opts deadcode.Options) ([]model.Node, error)
}
type LargefuncAnalyzer interface {
    Find(ctx context.Context, threshold int) ([]model.Node, error)
}
type QueryService interface {
    CallersOf(ctx context.Context, nodeID uint) ([]model.Node, error)
    CalleesOf(ctx context.Context, nodeID uint) ([]model.Node, error)
    TestsFor(ctx context.Context, nodeID uint) ([]model.Node, error)
    FileSummary(ctx context.Context, filePath string) (*query.FileSummary, error)
}
```

- [x] `TestNewServer_WithPromptCapabilities` — 서버 생성 시 프롬프트 capability 활성화 확인
- [x] `TestPromptHandlers_DepsNilSafe` — 분석 서비스가 nil이어도 프롬프트 등록은 성공 (호출 시 에러 메시지 반환)

#### 10.1 review_changes — 변경 리스크 분석 프롬프트

파라미터: `base` (string, optional, default "HEAD~1"), `repo_root` (string, required).
내부 동작:
1. `ChangesAnalyzer.Analyze(ctx, repoRoot, base)` → RiskEntry 목록
2. 각 RiskEntry의 파일에 대해 `CoverageAnalyzer.ByFile` → 테스트 갭 파악
3. 결과를 구조화된 텍스트로 조합하여 `GetPromptResult` 반환

반환 메시지 구조:
- Role: user
- Content: "다음은 {base} 이후 변경된 코드의 리스크 분석입니다.\n\n## 변경된 함수\n{risk entries}\n\n## 테스트 갭\n{uncovered files}\n\n위 정보를 바탕으로 코드 리뷰를 진행해주세요."

- [x] `TestReviewChanges_ReturnsRiskEntries` — RiskEntry 2개 → 메시지에 함수명, 리스크 점수 포함
- [x] `TestReviewChanges_IncludesTestGaps` — 커버리지 0%인 파일 → 테스트 갭 섹션에 표시
- [x] `TestReviewChanges_EmptyChanges` — 변경 없음 → "변경사항이 없습니다" 메시지
- [x] `TestReviewChanges_DefaultBase` — base 파라미터 없으면 "HEAD~1" 사용

#### 10.2 architecture_map — 아키텍처 개요 프롬프트

파라미터: 없음 (repo_root만 optional).
내부 동작:
1. DB에서 community + membership 조회 (GORM 직접 쿼리)
2. `CouplingAnalyzer.Analyze(ctx)` → 커뮤니티 간 결합도
3. 각 커뮤니티별 노드 수, cohesion 점수 포함

반환 메시지 구조:
- Role: user
- Content: "다음은 프로젝트의 아키텍처 개요입니다.\n\n## 모듈 (커뮤니티)\n{communities with stats}\n\n## 모듈 간 결합도\n{coupling pairs}\n\n이 구조를 바탕으로 아키텍처를 설명해주세요."

- [x] `TestArchitectureMap_ReturnsCommunities` — 커뮤니티 2개 → 메시지에 레이블, 노드 수 포함
- [x] `TestArchitectureMap_IncludesCoupling` — 커플링 1쌍 → 결합도 섹션에 표시
- [x] `TestArchitectureMap_NoCommunities` — 커뮤니티 없음 → "커뮤니티가 없습니다. 먼저 빌드를 실행하세요." 메시지

#### 10.3 debug_issue — 디버깅 가이드 프롬프트

파라미터: `description` (string, required) — 이슈 설명.
내부 동작:
1. `SearchBackend.Query(ctx, db, description, 10)` → 관련 노드 검색
2. 상위 3개 노드에 대해 `QueryService.CallersOf` + `CalleesOf` → 호출 관계
3. 상위 3개 노드에 대해 `ImpactAnalyzer.ImpactRadius(ctx, nodeID, 1)` → 영향 범위

반환 메시지 구조:
- Role: user
- Content: "다음 이슈를 디버깅합니다: {description}\n\n## 관련 코드\n{search results}\n\n## 호출 관계\n{callers/callees}\n\n## 영향 범위\n{impact nodes}\n\n위 컨텍스트를 바탕으로 이슈의 원인을 분석해주세요."

- [x] `TestDebugIssue_ReturnsSearchResults` — 검색 결과 3개 → 메시지에 노드 정보 포함
- [x] `TestDebugIssue_IncludesCallGraph` — 호출 관계 → callers/callees 섹션 존재
- [x] `TestDebugIssue_NoResults` — 검색 결과 없음 → "관련 코드를 찾을 수 없습니다" 메시지

#### 10.4 onboard_developer — 온보딩 프롬프트

파라미터: 없음.
내부 동작:
1. DB에서 노드/엣지 카운트 집계 (GORM Count)
2. DB에서 language 별 노드 수 집계 (GROUP BY language)
3. 커뮤니티 목록 조회 + 각 커뮤니티별 `CoverageAnalyzer.ByCommunity` → 테스트 커버리지
4. `LargefuncAnalyzer.Find(ctx, 50)` → 대형 함수 상위 5개

반환 메시지 구조:
- Role: user
- Content: "이 프로젝트에 대한 온보딩 가이드입니다.\n\n## 프로젝트 통계\n- 노드: N개, 엣지: M개\n- 언어: {lang counts}\n\n## 모듈 구조\n{communities with coverage}\n\n## 주의할 대형 함수\n{large functions}\n\n신규 개발자에게 이 프로젝트를 설명해주세요."

- [x] `TestOnboardDeveloper_ReturnsStats` — 노드/엣지 카운트 → 통계 섹션에 표시
- [x] `TestOnboardDeveloper_IncludesCommunities` — 커뮤니티 + 커버리지 → 모듈 구조 섹션
- [x] `TestOnboardDeveloper_IncludesLargeFunctions` — 대형 함수 → 주의 섹션에 표시
- [x] `TestOnboardDeveloper_EmptyProject` — 노드 0개 → "프로젝트가 비어있습니다. 먼저 빌드를 실행하세요." 메시지

#### 10.5 pre_merge_check — PR 병합 전 체크 프롬프트

파라미터: `base` (string, optional, default "HEAD~1"), `repo_root` (string, required).
내부 동작:
1. `ChangesAnalyzer.Analyze(ctx, repoRoot, base)` → RiskEntry 목록
2. 각 변경 파일에 대해 `CoverageAnalyzer.ByFile` → 테스트 커버리지
3. `DeadcodeAnalyzer.Find(ctx, opts)` → 미사용 코드
4. `LargefuncAnalyzer.Find(ctx, 50)` → 대형 함수 중 변경된 것만 필터

반환 메시지 구조:
- Role: user
- Content: "PR 병합 전 체크리스트입니다.\n\n## 리스크 분석\n{risk entries}\n\n## 테스트 커버리지\n{coverage by file}\n\n## 미사용 코드\n{dead code candidates}\n\n## 대형 함수\n{large functions in changed files}\n\n위 분석을 바탕으로 이 PR의 병합 준비 상태를 평가해주세요."

- [x] `TestPreMergeCheck_ReturnsRiskAndCoverage` — 리스크 + 커버리지 → 두 섹션 모두 존재
- [x] `TestPreMergeCheck_IncludesDeadCode` — 미사용 함수 → 미사용 코드 섹션에 표시
- [x] `TestPreMergeCheck_IncludesLargeFunctions` — 변경된 대형 함수 → 대형 함수 섹션에 표시
- [x] `TestPreMergeCheck_EmptyChanges` — 변경 없음 → "변경사항이 없습니다" 메시지

---

### Phase 11: MCP 도구 확장

#### 11.0 구조적 변경 (Tidy First)
- [x] `TestDeps_NewInterfaces` — 신규 인터페이스 필드가 nil이어도 기존 6개 도구 정상 동작
- [x] `TestPrompts_UsesDepsInterfaces` — prompts.go가 Deps 필드를 사용하도록 리팩터링 후 기존 5개 프롬프트 테스트 유지

#### 11.1 build_or_update_graph
- [x] `TestBuildOrUpdateGraph_FullRebuild` — full_rebuild=true → 전체 파싱, 노드/엣지 수 반환
- [x] `TestBuildOrUpdateGraph_Incremental` — full_rebuild=false → Incremental.Sync 호출
- [x] `TestBuildOrUpdateGraph_PostprocessFull` — postprocess="full" → flows + community + search 재빌드
- [x] `TestBuildOrUpdateGraph_PostprocessNone` — postprocess="none" → 후처리 스킵
- [x] `TestBuildOrUpdateGraph_MissingPath` — path 파라미터 없으면 에러

#### 11.2 run_postprocess
- [x] `TestRunPostprocess_AllEnabled` — flows=true, communities=true, fts=true → 3개 모두 실행
- [x] `TestRunPostprocess_OnlyFTS` — flows=false, communities=false, fts=true → search만 재빌드
- [x] `TestRunPostprocess_NoneEnabled` — 모두 false → 아무것도 안 함, {"status":"ok"} 반환

#### 11.3 query_graph
- [x] `TestQueryGraph_CallersOf` — pattern="callers_of", target="pkg.Func" → 호출자 목록
- [x] `TestQueryGraph_CalleesOf` — pattern="callees_of" → 피호출자 목록
- [x] `TestQueryGraph_ImportsOf` — pattern="imports_of" → import 목록
- [x] `TestQueryGraph_ImportersOf` — pattern="importers_of" → importer 목록
- [x] `TestQueryGraph_ChildrenOf` — pattern="children_of" → contains 자식 목록
- [x] `TestQueryGraph_TestsFor` — pattern="tests_for" → 테스트 노드 목록
- [x] `TestQueryGraph_InheritorsOf` — pattern="inheritors_of" → 상속자 목록
- [x] `TestQueryGraph_FileSummary` — pattern="file_summary", target="path/file.go" → 파일 요약
- [x] `TestQueryGraph_InvalidPattern` — 알 수 없는 pattern → 에러 메시지
- [x] `TestQueryGraph_TargetNotFound` — 존재하지 않는 target → 빈 결과

#### 11.4 list_graph_stats
- [x] `TestListGraphStats_ReturnsAllCounts` — 노드/엣지/파일 수, Kind별, Language별 카운트 확인
- [x] `TestListGraphStats_EmptyDB` — 빈 DB → 모든 카운트 0

#### 11.5 find_large_functions
- [x] `TestFindLargeFunctions_DefaultThreshold` — min_lines 없으면 50 적용
- [x] `TestFindLargeFunctions_CustomThreshold` — min_lines=30 → 30줄 초과 함수 반환
- [x] `TestFindLargeFunctions_Limit` — limit=3 → 최대 3개 반환
- [x] `TestFindLargeFunctions_NoResults` — threshold 이상 함수 없으면 빈 결과

#### 11.6 detect_changes
- [x] `TestDetectChanges_ReturnsRiskEntries` — 변경된 함수의 RiskEntry 반환
- [x] `TestDetectChanges_DefaultBase` — base 없으면 "HEAD~1" 사용
- [x] `TestDetectChanges_EmptyDiff` — 변경 없음 → 빈 entries
- [x] `TestDetectChanges_MissingRepoRoot` — repo_root 없으면 에러

#### 11.7 get_affected_flows
- [x] `TestGetAffectedFlows_ReturnsFlows` — 변경 노드가 속한 Flow 반환
- [x] `TestGetAffectedFlows_NoFlows` — 변경 노드가 Flow에 속하지 않음 → 빈 결과
- [x] `TestGetAffectedFlows_EmptyChanges` — 변경 없음 → 빈 결과

#### 11.8 list_flows
- [x] `TestListFlows_SortByName` — sort_by="name" → 이름순 정렬
- [x] `TestListFlows_SortByNodeCount` — sort_by="node_count" → 노드 수 내림차순
- [x] `TestListFlows_Limit` — limit=2 → 최대 2개
- [x] `TestListFlows_Empty` — Flow 없으면 빈 결과

#### 11.9 list_communities
- [x] `TestListCommunities_SortBySize` — sort_by="size" → 노드 수 내림차순
- [x] `TestListCommunities_SortByName` — sort_by="name" → 이름순
- [x] `TestListCommunities_MinSize` — min_size=3 → 노드 3개 이상만
- [x] `TestListCommunities_Empty` — 커뮤니티 없으면 빈 결과

#### 11.10 get_community
- [x] `TestGetCommunity_Basic` — community_id=1 → 커뮤니티 기본 정보
- [x] `TestGetCommunity_WithMembers` — include_members=true → 멤버 노드 포함
- [x] `TestGetCommunity_WithCoverage` — 커버리지 정보 포함
- [x] `TestGetCommunity_NotFound` — 존재하지 않는 ID → 에러

#### 11.11 get_architecture_overview
- [x] `TestArchitectureOverview_ReturnsCommunities` — 커뮤니티 목록 + cohesion
- [x] `TestArchitectureOverview_ReturnsCoupling` — 결합도 쌍 포함
- [x] `TestArchitectureOverview_Warnings` — 높은 결합도 → 경고 메시지 생성
- [x] `TestArchitectureOverview_Empty` — 커뮤니티 없으면 경고 메시지

#### 11.12 find_dead_code
- [x] `TestFindDeadCode_ReturnsUnusedFunctions` — incoming edge 없는 함수 반환
- [x] `TestFindDeadCode_FilterByKind` — kinds=["function"] → 함수만
- [x] `TestFindDeadCode_FilterByFilePattern` — file_pattern="internal/" → 경로 필터
- [x] `TestFindDeadCode_NoDeadCode` — 모든 함수에 incoming edge → 빈 결과

#### 11.13 E2E 통합 테스트
- [x] `TestE2E_BuildAndQueryGraph` — build_or_update_graph → query_graph(callers_of) → 결과 확인
- [x] `TestE2E_BuildAndStats` — build_or_update_graph → list_graph_stats → 카운트 확인
- [x] `TestE2E_BuildAndCommunities` — build_or_update_graph + run_postprocess(communities=true) → list_communities → 결과 확인
- [x] `TestE2E_BuildAndDeadCode` — build_or_update_graph → find_dead_code → 미사용 코드 확인

#### 11.14 서버 등록 확인
- [x] `TestMCPServer_ListTools_18` — 서버에 18개 도구 등록 확인
- [x] `TestMCPServer_ToolDescriptions` — 각 도구의 description이 비어있지 않음

---

### Phase 12: 언어 확장

#### 12.0 구조적 변경 (Tidy First)
- [x] `TestLangSpec_TestAttributes` — TestAttributes 필드 추가 후 기존 5개 언어 동작 유지
- [x] `TestLangSpec_ImplTypes` — ImplTypes 필드 추가 후 기존 5개 언어 동작 유지
- [x] `TestRegistry_AllLanguages` — 15개 언어 등록 확인

#### 12.1 JavaScript
- [x] `TestParseJS_Function` — function foo() {} → Function 노드
- [x] `TestParseJS_ArrowFunction` — const foo = () => {} → Function 노드 (이름 "foo")
- [x] `TestParseJS_Class` — class Foo {} → Class 노드
- [x] `TestParseJS_Import` — import { foo } from 'bar' → IMPORTS_FROM 엣지
- [x] `TestParseJS_Call` — foo() → CALLS 엣지
- [x] `TestParseJS_Export` — export function foo() {} → Function 노드

#### 12.2 C
- [x] `TestParseC_Function` — void foo() {} → Function 노드
- [x] `TestParseC_Struct` — struct Foo {} → Class 노드
- [x] `TestParseC_Include` — #include "foo.h" → IMPORTS_FROM 엣지
- [x] `TestParseC_Call` — foo() → CALLS 엣지
- [x] `TestParseC_HeaderDeclaration` — .h 파일의 함수 선언도 노드 생성

#### 12.3 C++
- [x] `TestParseCpp_Function` — void foo() {} → Function 노드
- [x] `TestParseCpp_Class` — class Foo {} → Class 노드
- [x] `TestParseCpp_Struct` — struct Bar {} → Class 노드
- [x] `TestParseCpp_Namespace` — namespace ns { void foo() {} } → QualifiedName에 ns 포함
- [x] `TestParseCpp_Include` — #include <iostream> → IMPORTS_FROM 엣지
- [x] `TestParseCpp_Call` — foo() → CALLS 엣지

#### 12.4 Rust
- [x] `TestParseRust_Function` — fn foo() {} → Function 노드
- [x] `TestParseRust_Struct` — struct Foo {} → Class 노드
- [x] `TestParseRust_Enum` — enum Bar {} → Class 노드
- [x] `TestParseRust_Trait` — trait Baz {} → Type(Interface) 노드
- [x] `TestParseRust_ImplBlock` — impl Foo { fn bar() {} } → bar는 Foo의 CONTAINS 자식
- [x] `TestParseRust_ImplTrait` — impl Trait for Foo {} → IMPLEMENTS 엣지
- [x] `TestParseRust_Use` — use std::io → IMPORTS_FROM 엣지
- [x] `TestParseRust_Call` — foo() → CALLS 엣지
- [x] `TestParseRust_TestAttribute` — #[test] fn test_foo() {} → NodeKind=test

#### 12.5 C#
- [x] `TestParseCSharp_Method` — void Foo() {} → Function 노드
- [x] `TestParseCSharp_Class` — class Foo {} → Class 노드
- [x] `TestParseCSharp_Interface` — interface IFoo {} → Type 노드
- [x] `TestParseCSharp_Using` — using System; → IMPORTS_FROM 엣지
- [x] `TestParseCSharp_Call` — Foo() → CALLS 엣지
- [x] `TestParseCSharp_Namespace` — namespace Ns { class Foo {} } → QualifiedName에 Ns 포함
- [x] `TestParseCSharp_TestAttribute` — [Test] void TestFoo() {} → NodeKind=test

#### 12.6 PHP
- [x] `TestParsePHP_Function` — function foo() {} → Function 노드
- [x] `TestParsePHP_Class` — class Foo {} → Class 노드
- [x] `TestParsePHP_Interface` — interface IFoo {} → Type 노드
- [x] `TestParsePHP_Use` — use App\Models\User; → IMPORTS_FROM 엣지
- [x] `TestParsePHP_Call` — foo() → CALLS 엣지
- [x] `TestParsePHP_Method` — class Foo { function bar() {} } → bar는 Foo의 CONTAINS 자식

#### 12.7 Swift
- [x] `TestParseSwift_Function` — func foo() {} → Function 노드
- [x] `TestParseSwift_Class` — class Foo {} → Class 노드
- [x] `TestParseSwift_Struct` — struct Bar {} → Class 노드
- [x] `TestParseSwift_Protocol` — protocol Baz {} → Type 노드
- [x] `TestParseSwift_Import` — import Foundation → IMPORTS_FROM 엣지
- [x] `TestParseSwift_Call` — foo() → CALLS 엣지
- [x] `TestParseSwift_Extension` — extension Foo { func bar() {} } → bar는 Foo의 CONTAINS 자식

#### 12.8 Scala
- [x] `TestParseScala_Function` — def foo() = {} → Function 노드
- [x] `TestParseScala_Class` — class Foo {} → Class 노드
- [x] `TestParseScala_Object` — object Foo {} → Class 노드 (싱글턴)
- [x] `TestParseScala_Trait` — trait Baz {} → Type 노드
- [x] `TestParseScala_Import` — import scala.io._ → IMPORTS_FROM 엣지
- [x] `TestParseScala_Call` — foo() → CALLS 엣지

#### 12.9 Lua
- [x] `TestParseLua_Function` — function foo() end → Function 노드
- [x] `TestParseLua_LocalFunction` — local function bar() end → Function 노드
- [x] `TestParseLua_Call` — foo() → CALLS 엣지
- [x] `TestParseLua_Require` — require("foo") → IMPORTS_FROM 엣지

#### 12.10 Bash
- [x] `TestParseBash_Function` — foo() { ... } → Function 노드
- [x] `TestParseBash_FunctionKeyword` — function bar() { ... } → Function 노드
- [x] `TestParseBash_Call` — foo (명령어 호출) → CALLS 엣지
- [x] `TestParseBash_Source` — source ./lib.sh → IMPORTS_FROM 엣지

#### 12.A Walker 확장: Arrow Function 이름 추출
- [x] `TestWalker_ArrowFunctionName_JS` — const foo = () => {} → 노드명 "foo"
- [x] `TestWalker_ArrowFunctionName_TS` — TypeScript arrow function도 동일

#### 12.B Walker 확장: Attribute/Decorator 기반 테스트 감지
- [x] `TestWalker_AttributeTest_Rust` — #[test] fn test_foo() → NodeKind=test
- [x] `TestWalker_AttributeTest_CSharp` — [Test] void TestFoo() → NodeKind=test
- [x] `TestWalker_AttributeTest_Java` — @Test void testFoo() → NodeKind=test

#### 12.C Walker 확장: impl/extension 블록 처리
- [x] `TestWalker_ImplBlock_Rust` — impl Foo { fn bar() {} } → bar는 Foo의 CONTAINS 자식
- [x] `TestWalker_ImplTrait_Rust` — impl Trait for Foo {} → IMPLEMENTS 엣지
- [x] `TestWalker_Extension_Swift` — extension Foo { func bar() {} } → bar는 Foo의 CONTAINS 자식

#### 12.12 통합 테스트
- [x] `TestRegistry_15Languages` — 15개 언어 모두 Registry에 등록, 확장자 조회 성공
- [x] `TestWalker_MultiLanguageFile` — 같은 Walker로 Go, Python, JavaScript 파일 연속 파싱 성공
- [x] `TestE2E_ParseMultiLangProject` — 여러 언어 파일이 섞인 디렉토리 파싱 → 각 언어별 노드 정상 생성
- [x] `TestE2E_SearchAcrossLanguages` — 파싱 후 검색 시 모든 언어의 노드가 검색됨

---

## 구현 순서 요약

```
Phase 1 (어노테이션 파서)       ← 외부 의존성 없음, 순수 문자열
  ↓
Phase 2 (주석 정규화/바인딩)     ← 언어별 주석 처리 추가
  ↓
Phase 3 (Tree-sitter 파서)      ← CGo 바인딩, AST 워킹
  ↓
Phase 4 (GORM 저장소)           ← DB 연동, CRUD
  ↓
Phase 5 (그래프 분석)            ← BFS, 흐름 추적
  ↓
Phase 6 (검색 방언)              ← DB별 FTS 구현
  ↓
Phase 7 (MCP 서버)               ← 통합 및 E2E
  ↓
Phase 8 (CLI 서브커맨드)          ← build, update, status, search, serve
  ↓
Phase 9 (분석 기능)              ← query, largefunc, deadcode, community, coverage, coupling, changes
  ↓
Phase 10 (MCP 프롬프트)          ← review_changes, architecture_map, debug_issue, onboard_developer, pre_merge_check
```

각 Phase는 이전 Phase의 테스트가 모두 통과한 후에만 시작한다.
Phase 내에서도 번호 순서대로 Red→Green→Refactor 사이클을 반복한다.

---

## 의존성 목록 (go.mod)

```
module github.com/imtaebin/code-context-graph

go 1.23

require (
    github.com/tree-sitter/go-tree-sitter  // Tree-sitter CGo 바인딩
    gorm.io/gorm                            // GORM ORM
    gorm.io/driver/sqlite                   // SQLite 드라이버
    gorm.io/driver/postgres                 // PostgreSQL 드라이버
    gorm.io/driver/mysql                    // MySQL 드라이버
    github.com/mark3labs/mcp-go             // MCP Go SDK
)
```

---

## 설정 (config)

```yaml
# config.yaml
database:
  driver: sqlite        # sqlite | postgres | mysql
  dsn: "ccg.db"         # SQLite: 파일 경로, PG: connection string, MySQL: DSN

parser:
  languages:
    - go
    - python
    - typescript
    - java
    - ruby
  ignore_patterns:
    - "vendor/**"
    - "node_modules/**"
    - "**/*_test.go"     # 선택적

annotation:
  enabled: true
  strict_mode: false     # true면 알 수 없는 태그 시 에러

server:
  transport: stdio       # stdio | sse
```
