# ccg Vectorless RAG Design

## Goal

ccg에 Vectorless RAG 기능을 추가한다. LLM API 키 없이 docs/ + community 구조로 트리 인덱스(`.ccg/doc-index.json`)를 빌드하고, MCP 툴 3개로 AI 에이전트(Claude Code, Cursor 등)가 트리를 탐색하여 문서를 찾고 답변을 합성할 수 있게 한다.

## Context

PageIndex(VectifyAI/PageIndex)의 Vectorless RAG 개념에서 영감을 받았다. 문서를 벡터로 임베딩하는 대신, 계층적 트리 구조를 AI 에이전트가 탐색하여 필요한 섹션만 선택적으로 읽는 방식이다.

ccg는 이미 community detection과 @index/@intent 어노테이션 시스템을 갖추고 있으므로, LLM 없이도 의미 있는 트리 구조를 생성할 수 있다. AI 에이전트가 합성을 담당하므로 ccg 자체에 LLM API 키가 필요 없다.

## Architecture

```
ccg rag-index (CLI) ──┐
                      ├── ragindex.Builder ──→ .ccg/doc-index.json
build_rag_index (MCP) ┘      │
                             ├── DB: communities, nodes(@index/@intent)
                             └── docs/ 디렉토리 스캔

get_rag_tree (MCP) ──→ doc-index.json 읽기 → JSON 트리 반환
get_doc_content (MCP) → docs/ 파일 직접 읽기

AI 에이전트:
  get_rag_tree() → get_rag_tree(community_id) → get_doc_content(path) → 답변 합성
```

## Tree Index 데이터 구조

`.ccg/doc-index.json` 포맷:

```json
{
  "version": 1,
  "built_at": "2026-04-14T10:00:00Z",
  "root": {
    "id": "root",
    "label": "code-context-graph",
    "summary": "",
    "children": [
      {
        "id": "community:3",
        "label": "MCP Server",
        "summary": "MCP 서버 핸들러 및 캐시 레이어",
        "children": [
          {
            "id": "file:internal/mcp/handlers.go",
            "label": "handlers.go",
            "summary": "@index 태그 값 또는 첫 번째 @intent 값",
            "doc_path": "docs/internal/mcp/handlers.go.md",
            "children": []
          }
        ]
      }
    ]
  }
}
```

### 노드 계층

```
root
  └── community  (기존 community detection 결과 재사용)
        └── file (community에 속한 파일)
```

### summary 생성 규칙 (LLM 없이)

| 노드 타입 | summary 소스 |
|----------|-------------|
| community | `community.Label` (이미 존재) |
| file | `@index` 태그 값 → 없으면 첫 번째 `@intent` 값 → 없으면 `""` |

## MCP 툴 3개

| 툴 | 인자 | 반환 |
|---|---|---|
| `build_rag_index` | (없음) | `"Built doc-index: N communities, M files"` |
| `get_rag_tree` | `community_id?: string` | JSON 트리 (해당 community 서브트리 또는 전체 root) |
| `get_doc_content` | `file_path: string` | 마크다운 파일 원문 |

### 에이전트 탐색 패턴

```
1. get_rag_tree()               → 전체 커뮤니티 목록 + summary 확인
2. get_rag_tree("community:3")  → 해당 커뮤니티 파일 목록
3. get_doc_content("docs/internal/mcp/handlers.go.md") → 파일 원문 읽기
4. (AI 에이전트가 직접 답변 합성)
```

## CLI 명세

```
ccg rag-index [--out docs] [--db path]
```

- `--out`: docs 디렉토리 (기본: `docs`)
- `--db`: DB 경로 (기본: `.ccg/graph.db` 또는 `.ccg.yaml` db 값)
- 출력: `.ccg/doc-index.json` 생성/덮어쓰기
- 성공 시 stdout: `"Built doc-index: N communities, M files"`

`build_rag_index` MCP 툴은 동일한 `ragindex.Builder`를 호출한다. 로직 중복 없음.

## 파일 구조

| 파일 | 변경 |
|------|------|
| `internal/ragindex/builder.go` | 신규: 트리 빌드 로직 (DB → doc-index.json) |
| `internal/ragindex/builder_test.go` | 신규: 빌드 로직 단위 테스트 |
| `internal/mcp/handlers.go` | `build_rag_index`, `get_rag_tree`, `get_doc_content` 핸들러 추가 |
| `internal/mcp/server.go` | 3개 툴 등록 |
| `internal/cli/ragindex.go` | 신규: `ccg rag-index` CLI 커맨드 |
| `internal/cli/root.go` | 새 커맨드 등록 |

## 테스트 전략

| 테스트 | 검증 내용 |
|--------|----------|
| `TestBuilder_EmptyDB` | DB 비어있을 때 root 노드만 생성 |
| `TestBuilder_WithCommunities` | 커뮤니티 3개 → 트리에 3개 community 노드 |
| `TestBuilder_FileSummary_IndexTag` | `@index` 있는 파일 → summary에 반영 |
| `TestBuilder_FileSummary_Fallback` | `@index` 없고 `@intent` 있음 → fallback 정상 |
| `TestBuilder_WritesJSON` | `.ccg/doc-index.json` 올바른 형식으로 저장 |
| `TestGetRagTree_Root` | `community_id` 없으면 전체 트리 반환 |
| `TestGetRagTree_Filtered` | `community_id` 있으면 해당 서브트리만 반환 |
| `TestGetDocContent_NotFound` | 존재하지 않는 경로 → 에러 메시지 반환 |

## 캐시 통합

`get_rag_tree`와 `get_doc_content`는 기존 MCP 캐시(`internal/mcp/cache.go`) 패턴을 동일하게 적용한다. `build_rag_index`는 write 작업이므로 캐시 없음 (단, `cache.Flush()` 호출하여 트리 변경 반영).

## 미구현 (추후)

- **symbol 레벨 노드**: file 하위에 @intent 함수/타입 노드 추가
- **--watch 모드**: docs/ 변경 감지 시 자동 재빌드
- **cross-repo**: 다중 DB 병합 인덱스
