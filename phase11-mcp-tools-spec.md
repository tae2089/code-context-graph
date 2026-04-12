# Phase 11: MCP 도구 확장 — Tech Spec

> **목표**: 현재 6개 MCP 도구를 18개로 확장.
> 기존 분석 서비스(Phase 5, 9)를 MCP 핸들러로 노출한다.
> Python 참조 프로젝트의 24개 도구 중 우리 아키텍처에 맞는 것을 선별한다.

---

## 현재 상태

### 기존 MCP 도구 (6개)

| # | 도구 | 핸들러 메서드 | 사용하는 서비스 |
|---|------|-------------|----------------|
| 1 | `parse_project` | `parseProject` | Parser, Store, SearchBackend |
| 2 | `get_node` | `getNode` | Store |
| 3 | `get_impact_radius` | `getImpactRadius` | Store, ImpactAnalyzer |
| 4 | `search` | `search` | SearchBackend |
| 5 | `get_annotation` | `getAnnotation` | Store |
| 6 | `trace_flow` | `traceFlow` | Store, FlowTracer |

### 기존 분석 서비스 (10개, Phase 5 + 9)

| 서비스 | 패키지 | 주요 메서드 |
|--------|--------|-------------|
| ImpactRadius | `analysis/impact` | `ImpactRadius(ctx, nodeID, depth)` |
| FlowTracer | `analysis/flows` | `TraceFlow(ctx, startNodeID)` |
| Incremental | `analysis/incremental` | `Sync(ctx, dir, store, parser)` |
| Query | `analysis/query` | `CallersOf`, `CalleesOf`, `ImportsOf`, `ImportersOf`, `ChildrenOf`, `TestsFor`, `InheritorsOf`, `FileSummary` |
| LargeFunc | `analysis/largefunc` | `Find(ctx, threshold)` |
| DeadCode | `analysis/deadcode` | `Find(ctx, opts)` |
| Community | `analysis/community` | `Rebuild(ctx, cfg)` |
| Coverage | `analysis/coverage` | `ByFile(ctx, filePath)`, `ByCommunity(ctx, communityID)` |
| Coupling | `analysis/coupling` | `Analyze(ctx)` |
| Changes | `analysis/changes` | `Analyze(ctx, repoDir, baseRef)` |

---

## 신규 MCP 도구 설계 (12개 추가 → 총 18개)

### 11.1 그래프 빌드/후처리 도구 (2개)

#### `build_or_update_graph` — 증분 빌드 지원 parse_project 강화판

기존 `parse_project`는 단일 디렉토리 파싱만 지원. 이 도구는 재귀 탐색 + 증분 업데이트 + 후처리를 통합.

| 항목 | 설명 |
|------|------|
| **파라미터** | `path` (string, required), `full_rebuild` (bool, default false), `postprocess` (string, default "full": "full"/"minimal"/"none") |
| **내부 동작** | 1. `full_rebuild=true` → 전체 파싱, `false` → `incremental.Sync()` |
|               | 2. `postprocess="full"` → flows + community + search 재빌드 |
|               | 3. `postprocess="minimal"` → search만 재빌드 |
|               | 4. `postprocess="none"` → 스킵 |
| **사용 서비스** | Incremental, Parser, Store, SearchBackend, FlowTracer, Community |
| **반환** | `{"status":"ok", "files_parsed": N, "nodes_created": N, "edges_created": N, "elapsed_ms": N}` |
| **Deps 추가** | 없음 (기존 deps 조합) |

> **기존 `parse_project`와의 관계**: `build_or_update_graph`가 상위 호환. `parse_project`는 유지하되 deprecation 주석 추가.

#### `run_postprocess` — 후처리 독립 실행

| 항목 | 설명 |
|------|------|
| **파라미터** | `flows` (bool, default true), `communities` (bool, default true), `fts` (bool, default true), `community_depth` (number, default 2) |
| **내부 동작** | 각 플래그에 따라 FlowTracer, Community.Rebuild, SearchBackend.Rebuild 선택 실행 |
| **사용 서비스** | FlowTracer, Community, SearchBackend |
| **반환** | `{"status":"ok", "flows_count": N, "communities_count": N, "fts_indexed": N}` |
| **Deps 추가** | 없음 |

---

### 11.2 쿼리/검색 도구 (3개)

#### `query_graph` — 미리 정의된 그래프 쿼리

| 항목 | 설명 |
|------|------|
| **파라미터** | `pattern` (string, required: "callers_of"\|"callees_of"\|"imports_of"\|"importers_of"\|"children_of"\|"tests_for"\|"inheritors_of"\|"file_summary"), `target` (string, required: qualified_name 또는 file_path) |
| **내부 동작** | pattern에 따라 `query.Service`의 해당 메서드 호출 |
| **사용 서비스** | Query, Store |
| **반환** | `{"pattern": "callers_of", "target": "...", "results": [nodes]}` |
| **Deps 추가** | `QueryService` 인터페이스 (prompts.go에서 이미 사용 중, Deps에 필드 추가) |

#### `list_graph_stats` — 그래프 통계

| 항목 | 설명 |
|------|------|
| **파라미터** | 없음 |
| **내부 동작** | GORM Count로 노드/엣지/파일 수, Kind별/Language별 GROUP BY 집계 |
| **사용 서비스** | DB (직접 GORM 쿼리) |
| **반환** | `{"total_nodes": N, "total_edges": N, "nodes_by_kind": {...}, "nodes_by_language": {...}, "edges_by_kind": {...}}` |
| **Deps 추가** | 없음 |

#### `find_large_functions` — 대형 함수 탐지

| 항목 | 설명 |
|------|------|
| **파라미터** | `min_lines` (number, default 50), `limit` (number, default 50) |
| **내부 동작** | `largefunc.Service.Find(ctx, min_lines)` 호출 후 limit 적용 |
| **사용 서비스** | LargeFunc |
| **반환** | `{"results": [{"name": "...", "file": "...", "lines": N}], "count": N}` |
| **Deps 추가** | `LargefuncAnalyzer` 인터페이스 |

---

### 11.3 변경 감지/영향 분석 도구 (2개)

#### `detect_changes` — 리스크 점수 기반 변경 감지

| 항목 | 설명 |
|------|------|
| **파라미터** | `base` (string, default "HEAD~1"), `repo_root` (string, required) |
| **내부 동작** | `changes.Service.Analyze(ctx, repoRoot, base)` → RiskEntry 목록 반환 |
| **사용 서비스** | Changes |
| **반환** | `{"base": "HEAD~1", "entries": [{"name": "...", "file": "...", "hunk_count": N, "risk_score": F}]}` |
| **Deps 추가** | `ChangesGitClient` (이미 Deps에 존재) |

#### `get_affected_flows` — 변경에 영향받는 Flow 목록

| 항목 | 설명 |
|------|------|
| **파라미터** | `base` (string, default "HEAD~1"), `repo_root` (string, required) |
| **내부 동작** | 1. `changes.Service.Analyze()` → 변경된 노드 ID 추출 |
|               | 2. 각 변경 노드에 대해 FlowMembership 조회 → 해당 Flow 목록 반환 |
| **사용 서비스** | Changes, DB (FlowMembership 조회) |
| **반환** | `{"affected_flows": [{"id": N, "name": "...", "affected_nodes": [...]}]}` |
| **Deps 추가** | 없음 |

---

### 11.4 Flow 도구 (1개)

#### `list_flows` — Flow 목록 조회

| 항목 | 설명 |
|------|------|
| **파라미터** | `sort_by` (string, default "name": "name"\|"node_count"), `limit` (number, default 50) |
| **내부 동작** | GORM으로 Flow + FlowMembership 조회, 정렬 후 반환 |
| **사용 서비스** | DB (직접 GORM 쿼리) |
| **반환** | `{"flows": [{"id": N, "name": "...", "description": "...", "node_count": N}]}` |
| **Deps 추가** | 없음 |

> **참고**: 기존 `trace_flow`는 특정 노드에서 시작하는 흐름 추적. `list_flows`는 저장된 모든 Flow 목록 조회.

---

### 11.5 커뮤니티/아키텍처 도구 (3개)

#### `list_communities` — 커뮤니티 목록

| 항목 | 설명 |
|------|------|
| **파라미터** | `sort_by` (string, default "size": "size"\|"name"\|"cohesion"), `min_size` (number, default 0) |
| **내부 동작** | DB에서 Community + CommunityMembership Count 조회, cohesion 계산 |
| **사용 서비스** | DB (직접), Community (Stats 계산) |
| **반환** | `{"communities": [{"id": N, "label": "...", "node_count": N, "cohesion": F}]}` |
| **Deps 추가** | 없음 |

#### `get_community` — 커뮤니티 상세

| 항목 | 설명 |
|------|------|
| **파라미터** | `community_id` (number, required), `include_members` (bool, default false) |
| **내부 동작** | Community + 선택적 멤버 노드 Preload + cohesion 계산 + Coverage 조회 |
| **사용 서비스** | DB, Coverage |
| **반환** | `{"id": N, "label": "...", "node_count": N, "cohesion": F, "coverage": F, "members": [...]}` |
| **Deps 추가** | 없음 |

#### `get_architecture_overview` — 아키텍처 개요

| 항목 | 설명 |
|------|------|
| **파라미터** | 없음 |
| **내부 동작** | 1. Community 목록 + 각 cohesion/coverage 계산 |
|               | 2. `coupling.Analyze()` → 모듈 간 결합도 |
|               | 3. 결합도 높은 쌍 경고 생성 |
| **사용 서비스** | DB, Coupling, Coverage |
| **반환** | `{"communities": [...], "coupling": [...], "warnings": [...]}` |
| **Deps 추가** | `CouplingAnalyzer` 인터페이스 |

---

### 11.6 Dead Code 도구 (1개)

#### `find_dead_code` — 미사용 코드 탐지

| 항목 | 설명 |
|------|------|
| **파라미터** | `kinds` (string[], optional: ["function", "class"]), `file_pattern` (string, optional) |
| **내부 동작** | `deadcode.Service.Find(ctx, opts)` 호출 |
| **사용 서비스** | DeadCode |
| **반환** | `{"dead_code": [{"name": "...", "kind": "...", "file": "...", "start_line": N}], "count": N}` |
| **Deps 추가** | `DeadcodeAnalyzer` 인터페이스 |

---

## 도구 최종 목록 (18개)

| # | 도구명 | 카테고리 | 신규/기존 |
|---|--------|---------|----------|
| 1 | `parse_project` | 빌드 | 기존 (deprecated) |
| 2 | `get_node` | 쿼리 | 기존 |
| 3 | `get_impact_radius` | 분석 | 기존 |
| 4 | `search` | 검색 | 기존 |
| 5 | `get_annotation` | 쿼리 | 기존 |
| 6 | `trace_flow` | 분석 | 기존 |
| 7 | `build_or_update_graph` | 빌드 | **신규** |
| 8 | `run_postprocess` | 빌드 | **신규** |
| 9 | `query_graph` | 쿼리 | **신규** |
| 10 | `list_graph_stats` | 쿼리 | **신규** |
| 11 | `find_large_functions` | 분석 | **신규** |
| 12 | `detect_changes` | 변경 | **신규** |
| 13 | `get_affected_flows` | 변경 | **신규** |
| 14 | `list_flows` | Flow | **신규** |
| 15 | `list_communities` | 커뮤니티 | **신규** |
| 16 | `get_community` | 커뮤니티 | **신규** |
| 17 | `get_architecture_overview` | 커뮤니티 | **신규** |
| 18 | `find_dead_code` | 분석 | **신규** |

---

## 제외한 Python 참조 도구 및 사유

| Python 도구 | 제외 사유 |
|-------------|----------|
| `get_review_context_tool` | deprecated (Python에서도), `get_minimal_context` + `get_impact_radius`로 대체 |
| `get_minimal_context_tool` | 기존 `search` + `get_impact_radius` 조합으로 충분. 필요시 Phase 13 이후 추가 |
| `semantic_search_nodes_tool` | 벡터 임베딩 필요 — 현재 아키텍처에 없음. 별도 Phase로 분리 |
| `embed_graph_tool` | 벡터 임베딩 인프라 필요 — 별도 Phase |
| `refactor_tool` | 코드 변경 도구 — 읽기 전용 분석 도구와 분리. 별도 Phase |
| `apply_refactor_tool` | 위와 동일 |
| `generate_wiki_tool` | 마크다운 위키 생성 — 별도 Phase |
| `get_wiki_page_tool` | 위와 동일 |
| `list_repos_tool` | 멀티-리포 지원 — 별도 Phase |
| `cross_repo_search_tool` | 멀티-리포 지원 — 별도 Phase |
| `get_docs_section_tool` | 정적 문서 반환 — 빌드 시 README에 포함하면 충분 |

---

## Deps 구조체 변경

```go
type Deps struct {
	// 기존
	Store            store.GraphStore
	DB               *gorm.DB
	Parser           Parser
	SearchBackend    storesearch.Backend
	ImpactAnalyzer   ImpactAnalyzer
	FlowTracer       FlowTracer
	ChangesGitClient changes.GitClient
	Logger           *slog.Logger

	// Phase 11 추가
	QueryService      QueryService        // analysis/query
	LargefuncAnalyzer LargefuncAnalyzer   // analysis/largefunc
	DeadcodeAnalyzer  DeadcodeAnalyzer    // analysis/deadcode
	CouplingAnalyzer  CouplingAnalyzer    // analysis/coupling
	CoverageAnalyzer  CoverageAnalyzer    // analysis/coverage
	CommunityBuilder  CommunityBuilder    // analysis/community
	Incremental       IncrementalSyncer   // analysis/incremental
}
```

> **참고**: `prompts.go`에서 일부 서비스를 `deps.DB`로 직접 생성하던 방식에서 Deps 주입 방식으로 전환.
> prompts.go의 inline 서비스 생성 코드도 Deps 필드를 사용하도록 리팩터링 (Tidy First: 구조적 변경 먼저).

### 신규 인터페이스 정의

```go
// server.go에 추가

type QueryService interface {
	CallersOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	CalleesOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	ImportsOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	ImportersOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	ChildrenOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	TestsFor(ctx context.Context, nodeID uint) ([]model.Node, error)
	InheritorsOf(ctx context.Context, nodeID uint) ([]model.Node, error)
	FileSummary(ctx context.Context, filePath string) (*query.FileSummary, error)
}

type LargefuncAnalyzer interface {
	Find(ctx context.Context, threshold int) ([]model.Node, error)
}

type DeadcodeAnalyzer interface {
	Find(ctx context.Context, opts deadcode.Options) ([]model.Node, error)
}

type CouplingAnalyzer interface {
	Analyze(ctx context.Context) ([]coupling.CouplingPair, error)
}

type CoverageAnalyzer interface {
	ByFile(ctx context.Context, filePath string) (*coverage.FileCoverage, error)
	ByCommunity(ctx context.Context, communityID uint) (*coverage.CommunityCoverage, error)
}

type CommunityBuilder interface {
	Rebuild(ctx context.Context, cfg community.Config) ([]community.Stats, error)
}

type IncrementalSyncer interface {
	Sync(ctx context.Context, dir string) (*incremental.SyncResult, error)
}
```

---

## TDD 구현 계획

> **규칙**: 각 테스트는 `- [ ]` 체크박스로 표시. Red → Green → Refactor.

### 11.0 구조적 변경 (Tidy First)

Deps에 인터페이스 추가 + prompts.go 리팩터링. 행위 변경 없이 구조만 정리.

- [ ] `TestDeps_NewInterfaces` — 신규 인터페이스 필드가 nil이어도 기존 6개 도구 정상 동작
- [ ] `TestPrompts_UsesDepsInterfaces` — prompts.go가 Deps 필드를 사용하도록 리팩터링 후 기존 5개 프롬프트 테스트 유지

### 11.1 `build_or_update_graph` 핸들러

- [ ] `TestBuildOrUpdateGraph_FullRebuild` — `full_rebuild=true` → 전체 파싱, 노드/엣지 수 반환
- [ ] `TestBuildOrUpdateGraph_Incremental` — `full_rebuild=false` → Incremental.Sync 호출
- [ ] `TestBuildOrUpdateGraph_PostprocessFull` — `postprocess="full"` → flows + community + search 재빌드
- [ ] `TestBuildOrUpdateGraph_PostprocessNone` — `postprocess="none"` → 후처리 스킵
- [ ] `TestBuildOrUpdateGraph_MissingPath` — path 파라미터 없으면 에러

### 11.2 `run_postprocess` 핸들러

- [ ] `TestRunPostprocess_AllEnabled` — flows=true, communities=true, fts=true → 3개 모두 실행
- [ ] `TestRunPostprocess_OnlyFTS` — flows=false, communities=false, fts=true → search만 재빌드
- [ ] `TestRunPostprocess_NoneEnabled` — 모두 false → 아무것도 안 함, `{"status":"ok"}` 반환

### 11.3 `query_graph` 핸들러

- [ ] `TestQueryGraph_CallersOf` — `pattern="callers_of"`, `target="pkg.Func"` → 호출자 목록
- [ ] `TestQueryGraph_CalleesOf` — `pattern="callees_of"` → 피호출자 목록
- [ ] `TestQueryGraph_ImportsOf` — `pattern="imports_of"` → import 목록
- [ ] `TestQueryGraph_ImportersOf` — `pattern="importers_of"` → importer 목록
- [ ] `TestQueryGraph_ChildrenOf` — `pattern="children_of"` → contains 자식 목록
- [ ] `TestQueryGraph_TestsFor` — `pattern="tests_for"` → 테스트 노드 목록
- [ ] `TestQueryGraph_InheritorsOf` — `pattern="inheritors_of"` → 상속자 목록
- [ ] `TestQueryGraph_FileSummary` — `pattern="file_summary"`, `target="path/file.go"` → 파일 요약
- [ ] `TestQueryGraph_InvalidPattern` — 알 수 없는 pattern → 에러 메시지
- [ ] `TestQueryGraph_TargetNotFound` — 존재하지 않는 target → 빈 결과

### 11.4 `list_graph_stats` 핸들러

- [ ] `TestListGraphStats_ReturnsAllCounts` — 노드/엣지/파일 수, Kind별, Language별 카운트 확인
- [ ] `TestListGraphStats_EmptyDB` — 빈 DB → 모든 카운트 0

### 11.5 `find_large_functions` 핸들러

- [ ] `TestFindLargeFunctions_DefaultThreshold` — `min_lines` 없으면 50 적용
- [ ] `TestFindLargeFunctions_CustomThreshold` — `min_lines=30` → 30줄 초과 함수 반환
- [ ] `TestFindLargeFunctions_Limit` — `limit=3` → 최대 3개 반환
- [ ] `TestFindLargeFunctions_NoResults` — threshold 이상 함수 없으면 빈 결과

### 11.6 `detect_changes` 핸들러

- [ ] `TestDetectChanges_ReturnsRiskEntries` — 변경된 함수의 RiskEntry 반환
- [ ] `TestDetectChanges_DefaultBase` — `base` 없으면 "HEAD~1" 사용
- [ ] `TestDetectChanges_EmptyDiff` — 변경 없음 → 빈 entries
- [ ] `TestDetectChanges_MissingRepoRoot` — `repo_root` 없으면 에러

### 11.7 `get_affected_flows` 핸들러

- [ ] `TestGetAffectedFlows_ReturnsFlows` — 변경 노드가 속한 Flow 반환
- [ ] `TestGetAffectedFlows_NoFlows` — 변경 노드가 Flow에 속하지 않음 → 빈 결과
- [ ] `TestGetAffectedFlows_EmptyChanges` — 변경 없음 → 빈 결과

### 11.8 `list_flows` 핸들러

- [ ] `TestListFlows_SortByName` — `sort_by="name"` → 이름순 정렬
- [ ] `TestListFlows_SortByNodeCount` — `sort_by="node_count"` → 노드 수 내림차순
- [ ] `TestListFlows_Limit` — `limit=2` → 최대 2개
- [ ] `TestListFlows_Empty` — Flow 없으면 빈 결과

### 11.9 `list_communities` 핸들러

- [ ] `TestListCommunities_SortBySize` — `sort_by="size"` → 노드 수 내림차순
- [ ] `TestListCommunities_SortByName` — `sort_by="name"` → 이름순
- [ ] `TestListCommunities_MinSize` — `min_size=3` → 노드 3개 이상만
- [ ] `TestListCommunities_Empty` — 커뮤니티 없으면 빈 결과

### 11.10 `get_community` 핸들러

- [ ] `TestGetCommunity_Basic` — `community_id=1` → 커뮤니티 기본 정보
- [ ] `TestGetCommunity_WithMembers` — `include_members=true` → 멤버 노드 포함
- [ ] `TestGetCommunity_WithCoverage` — 커버리지 정보 포함
- [ ] `TestGetCommunity_NotFound` — 존재하지 않는 ID → 에러

### 11.11 `get_architecture_overview` 핸들러

- [ ] `TestArchitectureOverview_ReturnsCommunities` — 커뮤니티 목록 + cohesion
- [ ] `TestArchitectureOverview_ReturnsCoupling` — 결합도 쌍 포함
- [ ] `TestArchitectureOverview_Warnings` — 높은 결합도 → 경고 메시지 생성
- [ ] `TestArchitectureOverview_Empty` — 커뮤니티 없으면 경고 메시지

### 11.12 `find_dead_code` 핸들러

- [ ] `TestFindDeadCode_ReturnsUnusedFunctions` — incoming edge 없는 함수 반환
- [ ] `TestFindDeadCode_FilterByKind` — `kinds=["function"]` → 함수만
- [ ] `TestFindDeadCode_FilterByFilePattern` — `file_pattern="internal/"` → 경로 필터
- [ ] `TestFindDeadCode_NoDeadCode` — 모든 함수에 incoming edge → 빈 결과

### 11.13 E2E 통합 테스트

- [ ] `TestE2E_BuildAndQueryGraph` — `build_or_update_graph` → `query_graph(callers_of)` → 결과 확인
- [ ] `TestE2E_BuildAndStats` — `build_or_update_graph` → `list_graph_stats` → 카운트 확인
- [ ] `TestE2E_BuildAndCommunities` — `build_or_update_graph` + `run_postprocess(communities=true)` → `list_communities` → 결과 확인
- [ ] `TestE2E_BuildAndDeadCode` — `build_or_update_graph` → `find_dead_code` → 미사용 코드 확인

### 11.14 서버 등록 확인

- [ ] `TestMCPServer_ListTools_18` — 서버에 18개 도구 등록 확인
- [ ] `TestMCPServer_ToolDescriptions` — 각 도구의 description이 비어있지 않음

---

## 구현 순서

```
11.0 구조적 변경 (Deps 인터페이스 추가, prompts.go 리팩터링)
  ↓
11.1 build_or_update_graph         ← Incremental + 후처리 통합
  ↓
11.2 run_postprocess               ← 후처리 독립 실행
  ↓
11.3 query_graph                   ← Query 서비스 노출 (가장 많은 테스트)
  ↓
11.4 list_graph_stats              ← 단순 GORM 집계
  ↓
11.5 find_large_functions          ← LargeFunc 서비스 노출
  ↓
11.6 detect_changes                ← Changes 서비스 노출
  ↓
11.7 get_affected_flows            ← Changes + FlowMembership 조합
  ↓
11.8 list_flows                    ← 단순 GORM 쿼리
  ↓
11.9 list_communities              ← Community 목록
  ↓
11.10 get_community                ← Community 상세 + Coverage
  ↓
11.11 get_architecture_overview    ← Community + Coupling 통합
  ↓
11.12 find_dead_code               ← DeadCode 서비스 노출
  ↓
11.13 E2E 통합 테스트
  ↓
11.14 서버 등록 확인
```

---

## 파일 변경 목록

| 파일 | 변경 내용 |
|------|----------|
| `internal/mcp/server.go` | Deps 필드 추가, 인터페이스 정의, 12개 도구 등록 |
| `internal/mcp/handlers.go` | 12개 핸들러 메서드 추가 |
| `internal/mcp/handlers_test.go` | 50+ 테스트 추가 |
| `internal/mcp/e2e_test.go` | 4개 E2E 테스트 추가 |
| `internal/mcp/prompts.go` | Deps 인터페이스 사용으로 리팩터링 |
| `internal/mcp/server_test.go` | 도구 수 확인 테스트 업데이트 |

---

## 테스트 수 요약

| 서브-Phase | 테스트 수 |
|-----------|----------|
| 11.0 구조적 변경 | 2 |
| 11.1 build_or_update_graph | 5 |
| 11.2 run_postprocess | 3 |
| 11.3 query_graph | 10 |
| 11.4 list_graph_stats | 2 |
| 11.5 find_large_functions | 4 |
| 11.6 detect_changes | 4 |
| 11.7 get_affected_flows | 3 |
| 11.8 list_flows | 4 |
| 11.9 list_communities | 4 |
| 11.10 get_community | 4 |
| 11.11 get_architecture_overview | 4 |
| 11.12 find_dead_code | 4 |
| 11.13 E2E 통합 | 4 |
| 11.14 서버 등록 | 2 |
| **합계** | **59** |

---

## 컨벤션 (기존 패턴 유지)

- 핸들러 메서드: `func (h *handlers) toolName(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)`
- 파라미터 추출: `request.RequireString("name")`, `request.GetString("name", "default")`, `request.GetNumber("name", 10)`
- 에러 반환: `mcp.NewToolResultError(fmt.Sprintf("..."))`
- 성공 반환: `mcp.NewToolResultText(jsonString)`
- JSON 직렬화: `encoding/json.Marshal` → string으로 반환
- 로깅: `h.logger().Info("tool_name called", "param", value)`
- DB 쿼리: GORM builder만 사용 (raw SQL 금지)
- 테스트: SQLite `:memory:` + unique DSN per test (`file:testN?mode=memory&cache=shared`)
