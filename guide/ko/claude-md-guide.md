# CCG 사용자를 위한 CLAUDE.md 가이드 (CLAUDE.md Guide for CCG Users)

[English](../claude-md-guide.md)

아래 템플릿을 복사하여 CCG를 사용하는 모든 프로젝트의 CLAUDE.md에 맞춰서 적용하십시오.

---

## 템플릿 (Template)

````markdown
## 코드 지식 그래프 (Code Knowledge Graph, CCG)

이 프로젝트는 `.mcp.json`에 등록된 [code-context-graph](https://github.com/tae2089/code-context-graph) MCP 서버를 사용합니다.

### 코드 분석 흐름 (Code Analysis Flow)

```
get_minimal_context          ← 항상 여기서 시작하십시오 (그래프 상태 + 권장 도구 확인)
        │
        ├─ 그래프가 없는 경우 → build_or_update_graph(path: ".")
        │
        ├─ 코드 찾기 → search(query: "keyword")
        │               → query_graph(pattern: "callers_of", target: "pkg.Func")
        │
        ├─ 변경 영향도 분석 → detect_changes(repo_root: ".")
        │                   → get_impact_radius(qualified_name: "...", depth: 3)
        │                   → get_affected_flows(repo_root: ".")
        │
        ├─ 구조 파악 → search_docs()
        │                          → get_doc_content()
        │                          → query_graph(file_summary)
        │
        └─ 코드 변경 후 → build_or_update_graph(path: ".", full_rebuild: false)
```

### 팁 (Tips)

- `search`는 코드뿐만 아니라 어노테이션(`@intent`, `@domainRule` 등)도 검색 대상에 포함합니다.
- 경로를 지정하여 범위를 좁힐 수 있습니다: `search(path: "internal/auth")`
- MSA 환경에서는 모든 도구에 `namespace` 파라미터를 사용하여 서비스를 격리하십시오.
````

---

## 최소 버전 (Minimal Version)

```markdown
## CCG

코드를 분석할 때 `get_minimal_context`를 먼저 호출하십시오. 그래프 상태를 보고하고 다음 단계를 제안해줍니다.
```
