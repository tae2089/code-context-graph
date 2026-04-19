# CLAUDE.md Guide for CCG Users

CCG를 사용하는 프로젝트의 CLAUDE.md에 아래 템플릿을 복사·수정하여 사용하세요.

---

## Template

````markdown
## Code Knowledge Graph (CCG)

이 프로젝트는 `.mcp.json`에 등록된 [code-context-graph](https://github.com/tae2089/code-context-graph) MCP 서버를 사용합니다.

### 코드 분석 플로우

```
get_minimal_context          ← 항상 여기서 시작 (그래프 상태 + 추천 도구)
        │
        ├─ 그래프 없음 → build_or_update_graph(path: ".")
        │
        ├─ 코드 찾기 → search(query: "키워드")
        │                → query_graph(pattern: "callers_of", target: "pkg.Func")
        │
        ├─ 변경 영향 → detect_changes(repo_root: ".")
        │                → get_impact_radius(qualified_name: "...", depth: 3)
        │                → get_affected_flows(repo_root: ".")
        │
        ├─ 구조 파악 → get_architecture_overview()
        │                → list_communities()
        │
        └─ 코드 변경 후 → build_or_update_graph(path: ".", full_rebuild: false)
```

### 팁

- `search`는 코드뿐 아니라 `@intent`, `@domainRule` 등 어노테이션도 검색합니다
- `search(path: "internal/auth")` 처럼 경로로 범위를 좁힐 수 있습니다
- MSA 환경에서는 모든 도구에 `workspace` 파라미터로 서비스를 격리하세요
````

---

## 최소 버전

```markdown
## CCG

코드 분석 시 `get_minimal_context`를 먼저 호출하세요. 그래프 상태와 다음 단계를 안내합니다.
```
