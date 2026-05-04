# Multi-Repo RAG Sync — TDD 구현 계획

## 설계 결정 (확정)

- **Namespace 전파**: `context.Context`에 namespace를 넣고 store 내부에서 자동 추출 (호출자 변경 최소화)
- **Git 라이브러리**: `go-git/go-git/v6` (순수 Go)
- **DB**: Node 모델에 `Namespace` 컬럼 추가, `UNIQUE(namespace, qualified_name)` 복합 인덱스
- **workspace = namespace**: 하나의 개념으로 통합
- **인증**: SSH key + GitHub App Token 둘 다 지원
- **Allow-Repo**: Atlantis 패턴 (`org/*`, `!org/private`)

---

## Step 1: context namespace 패키지 + Node.Namespace 컬럼 추가

### 테스트

- [x] `TestWithNamespace_setsNamespaceInContext` — `WithNamespace(ctx, "pay")` → `FromContext(ctx)` 가 `"pay"` 반환
- [x] `TestFromContext_emptyWhenNotSet` — 빈 context에서 `FromContext(ctx)` 가 `""` 반환
- [x] `TestNode_NamespaceField` — `model.Node{Namespace: "svc"}` → GORM AutoMigrate → DB에 namespace 컬럼 존재 확인
- [x] `TestNode_UniqueIndex_NamespaceQualifiedName` — 같은 QualifiedName, 다른 Namespace → 두 레코드 모두 저장 가능
- [x] `TestNode_UniqueIndex_DuplicateWithinNamespace` — 같은 Namespace+QualifiedName → UNIQUE 위반

### 구현

- `internal/ctxns/namespace.go` — `WithNamespace(ctx, ns)`, `FromContext(ctx) string`
- `internal/model/node.go` — `Namespace string` 필드 + GORM 태그 변경

---

## Step 2: gormstore 메서드에 context namespace 자동 필터 적용

### 테스트

- [x] `TestUpsertNodes_SetsNamespaceFromContext` — context에 namespace 설정 후 UpsertNodes → 저장된 Node에 namespace 반영
- [x] `TestUpsertNodes_EmptyNamespace_BackwardCompatible` — namespace 없는 context → Node.Namespace == ""
- [x] `TestGetNode_FiltersByNamespace` — namespace "a"에 노드 저장 → namespace "b" context로 GetNode → nil 반환
- [x] `TestGetNode_EmptyNamespace_FindsLegacyNodes` — namespace "" 노드 → 빈 context로 GetNode → 정상 조회
- [x] `TestGetNodesByFile_FiltersByNamespace` — namespace "a" context → 해당 namespace 노드만 반환
- [x] `TestGetNodesByQualifiedNames_FiltersByNamespace` — namespace별 격리 확인
- [x] `TestDeleteNodesByFile_FiltersByNamespace` — namespace "a" context → "a" 노드만 삭제, "b" 노드 유지
- [x] `TestUpsertNodes_ConflictWithinSameNamespace` — 같은 namespace+QualifiedName → 업데이트

### 구현

- `internal/store/gormstore/gormstore.go` — 모든 Node 쿼리에 `ctxns.FromContext(ctx)` 기반 `WHERE namespace = ?` 추가
- `UpsertNodes`: context에서 namespace 추출하여 각 Node에 설정 후 upsert
- `GetNode`, `GetNodeByID`, `GetNodesByIDs`, `GetNodesByQualifiedNames`, `GetNodesByFile`, `GetNodesByFiles`, `DeleteNodesByFile`: namespace 필터 추가

---

## Step 3: analysis 서비스들에 namespace 전파

### 테스트

- [x] `TestDeadcodeService_RespectsNamespace` — namespace context 전달 → 해당 namespace 노드만 분석
- [x] `TestCoverageService_RespectsNamespace` — namespace별 커버리지 격리
- [x] `TestQueryService_RespectsNamespace` — 쿼리 결과가 namespace 내로 한정
- [x] `TestCommunityBuilder_RespectsNamespace` — 커뮤니티 빌드가 namespace별 격리
- [x] `TestChangesService_RespectsNamespace` — 변경 분석이 namespace 격리
- [x] `TestLargefuncService_RespectsNamespace` — 대형 함수 분석이 namespace 격리

### 구현

- 각 서비스에서 `h.deps.DB` 직접 사용하는 쿼리에 namespace 필터 추가
- `deadcode/service.go`, `coverage/service.go`, `query/service.go`, `community/service.go`, `changes/service.go`, `largefunc/service.go`

---

## Step 4: MCP 핸들러에 workspace→namespace 통합

### 테스트

- [x] `TestMCPHandler_WorkspaceToNamespace` — MCP 요청에 workspace 파라미터 → context에 namespace 설정 확인
- [x] `TestMCPHandler_SearchWithNamespace` — namespace context로 search → 해당 namespace 결과만 반환
- [x] `TestMCPHandler_GraphWithNamespace` — handler_graph 쿼리가 namespace 필터 적용
- [x] `TestMCPHandler_QueryWithNamespace` — handler_query가 namespace 필터 적용

### 구현

- `internal/mcp/handler_graph.go`, `handler_query.go` — 직접 DB 쿼리에 namespace 필터
- MCP 핸들러에서 `workspace` 파라미터 → `ctxns.WithNamespace(ctx, workspace)` 변환
- `search/sqlite.go`, `search/postgres.go` — FTS 쿼리 결과 조인 시 namespace 필터

---

## Step 5: RepoAllowlist 구현

### 테스트

- [x] `TestRepoAllowlist_ExactMatch` — `"org/svc"` 규칙 → `"org/svc"` 허용, `"org/other"` 거부
- [x] `TestRepoAllowlist_WildcardOrg` — `"org/*"` 규칙 → `"org/svc"` 허용, `"other/svc"` 거부
- [x] `TestRepoAllowlist_Negation` — `"org/*"`, `"!org/private"` → `"org/svc"` 허용, `"org/private"` 거부
- [x] `TestRepoAllowlist_EmptyAllowsNothing` — 빈 규칙 → 모든 repo 거부
- [x] `TestRepoAllowlist_MultipleRules` — 여러 규칙 조합 테스트

### 구현

- `internal/webhook/allowlist.go` — `RepoAllowlist` struct + `IsAllowed(repoFullName string) bool`

---

## Step 6: GitOps (clone/pull) 구현 (go-git)

### 테스트

- [x] `TestGitOps_CloneRepo` — 로컬 bare repo → clone → 파일 존재 확인
- [x] `TestGitOps_PullUpdates` — clone 후 → bare repo에 commit 추가 → pull → 새 파일 확인
- [x] `TestGitOps_RepoDir` — `repoRoot + namespace` 경로 계산 확인

### 구현

- `internal/webhook/gitops.go` — `CloneOrPull(ctx, repoURL, repoRoot, namespace string, auth transport.AuthMethod) error`

---

## Step 7: Auth (SSH + GitHub App Token) 구현

### 테스트

- [x] `TestSSHAuth_FromKeyFile` — SSH key 파일 경로 → `transport.AuthMethod` 생성
- [x] `TestSSHAuth_FromKeyData` — PEM bytes → `transport.AuthMethod` 생성
- [x] `TestGitHubAppAuth_JWTGeneration` — AppID + PrivateKey → JWT 토큰 생성 (만료시간 10분)
- [x] `TestGitHubAppAuth_TokenAuth` — Installation Token → `http.BasicAuth` 생성
- [x] `TestGitAuth_ResolveMethod` — GitAuth 구조체 → 적절한 AuthMethod 선택

### 구현

- `internal/webhook/auth.go` — `GitAuth` struct + `Resolve() (transport.AuthMethod, error)`

---

## Step 8: /webhook 핸들러 조립

### 테스트

- [x] `TestWebhookHandler_ValidPushEvent` — 유효한 PushEvent (main branch, allowed repo) → 200 OK
- [x] `TestWebhookHandler_InvalidSignature` — HMAC 불일치 → 403
- [x] `TestWebhookHandler_NonMainBranch` — develop branch push → 200 + skip 로그
- [x] `TestWebhookHandler_DisallowedRepo` — allow-repo에 없는 repo → 200 + skip 로그
- [x] `TestWebhookHandler_NonPushEvent` — IssueEvent → 200 + skip
- [x] `TestExtractNamespace` — `"org/pay-svc"` → `"pay-svc"`, `"org/sub/repo"` → `"sub-repo"`

### 구현

- `internal/webhook/handler.go` — HTTP 핸들러 + HMAC 검증 + 파이프라인 조립

---

## Step 9: serve 커맨드 플래그 추가

### 테스트

- [x] `TestServeCmdFlags_AllowRepo` — `--allow-repo "org/*"` 플래그 파싱
- [x] `TestServeCmdFlags_WebhookSecret` — `--webhook-secret` 플래그 파싱
- [x] `TestServeCmdFlags_RepoRoot` — `--repo-root` 플래그 파싱

### 구현

- `internal/cli/serve.go` — `ServeConfig`에 webhook 관련 필드 추가 + cobra 플래그
- `cmd/ccg/main.go` — `/webhook` 라우트 연결

---

## Step 10: E2E 테스트

### 테스트

- [x] `TestE2E_WebhookToRAG` — webhook 수신 → git clone → build → RAG 인덱스 생성 전체 파이프라인
- [x] `TestE2E_MultiRepoIsolation` — 2개 repo webhook → 각각 다른 namespace에 저장 → cross-query 격리 확인

### 구현

- 테스트용 local bare repo 생성 → webhook 시뮬레이션 → 파이프라인 실행 → DB 검증

---

## Step 11: SyncQueue — repo별 work queue (K8s WorkQueue 패턴)

### 테스트

- [x] `TestSyncQueue_DeduplicatesRapidPushes` — 같은 repo에 3번 연속 Add → handler 1번만 호출
- [x] `TestSyncQueue_MultiRepoConcurrent` — 다른 repo 2개 Add → 동시 처리 확인
- [x] `TestSyncQueue_RequeuesOnDirtyDuringProcessing` — 처리 중 새 push 도착 → Done 후 재처리, 최신 payload 사용
- [x] `TestSyncQueue_ShutdownDrainsWorkers` — Shutdown 호출 → 진행 중 작업 완료 대기
- [x] `TestSyncQueue_PayloadUpdatedToLatest` — 3번 Add → handler에 마지막 payload 전달
- [x] `TestE2E_SyncQueueDedup` — bare repo + webhook 5번 rapid push → SyncQueue 경유 → CloneOrPull 최대 2번 (dedup 확인)
- [x] `TestE2E_SyncQueueMultiRepoParallel` — 2개 bare repo + webhook → SyncQueue 경유 → 병렬 clone + namespace 격리 확인

### 구현

- `internal/webhook/syncqueue.go` — dirty/processing set + FIFO queue + bounded worker pool
- `cmd/ccg/main.go` — `onSync`에서 `go func()` 제거, `SyncQueue.Add()` 사용 + graceful shutdown 연결

---

## Step 12: Lifecycle / Incremental Hardening

### 테스트

- [x] `TestUpdateCommand_DeletesRemovedFiles` — `ccg update`가 현재 DB의 기존 파일 목록을 기준으로 삭제된 파일의 노드/엣지/search 문서를 정리
- [x] `TestSyncWithExisting_RestoresAnnotationsForModifiedFile` — incremental sync가 수정 파일의 annotation을 full build와 동일하게 복구
- [x] `TestBuildOrUpdateGraph_IncrementalIncludePaths_DefaultsToReplace` — MCP incremental + `include_paths` 기본 동작이 기존 replace semantics를 유지함을 명시
- [x] `TestBuildOrUpdateGraph_IncrementalIncludePaths_ReplaceFalsePreservesOutOfScopeFiles` — `replace=false`일 때 include 범위 밖 기존 그래프를 유지
- [x] `TestDeleteWorkspace_PurgesNamespaceGraphRAGAndCache` — workspace 삭제 시 namespace graph, workspace RAG index, MCP cache가 함께 정리
- [x] `TestBuilder_WriteIndex_UsesUniqueTempFileForConcurrentBuilds` — 같은 index dir 동시 빌드에서도 temp file 경합 없이 안전하게 완료
- [x] `TestDeleteGraph_LeavesCommunityParentsUntilExplicitCleanup` — 현재 `DeleteGraph`가 community parent row는 직접 정리하지 않는 동작을 characterization으로 고정
- [x] `TestDeleteGraph_LeavesFlowParentsUntilExplicitCleanup` — 현재 `DeleteGraph`가 flow parent row는 직접 정리하지 않는 동작을 characterization으로 고정

### 구현

- `internal/cli/update.go` — namespace 범위의 existing file paths를 읽어 `SyncWithExisting()`로 전달
- `internal/analysis/incremental/incremental.go` — annotation-aware parser sibling interface를 지원하고 modified file의 annotation을 재생성
- `internal/mcp/handler_parse.go`, `internal/mcp/tools_parse.go` — incremental `replace` flag 추가 (기본값 `true`)
- `internal/mcp/handler_workspace.go` — workspace 삭제 시 namespace graph purge + workspace RAG index 제거 + cache flush
- `internal/ragindex/builder.go` — same-dir unique temp file + `Sync()` + atomic rename
- `internal/store/gormstore/gormstore.go` — production semantics 변경 대신 lifecycle characterization test를 통과하도록 현재 contract 유지

---

## Step 13: Persisted Flow Rebuild

### 테스트

- [x] `TestFlowBuilder_Rebuild_PersistsFlowPerEntrypoint` — inbound `calls`가 없는 function/test 노드마다 stored flow와 memberships를 저장
- [x] `TestFlowBuilder_Rebuild_DeletesPriorFlowsInNamespace` — 같은 namespace 재빌드 시 이전 stored flows를 교체하고 다른 namespace는 유지
- [x] `TestFlowBuilder_Rebuild_NoEntrypointsReturnsEmpty` — entrypoint가 없으면 0 flows와 nil error를 반환
- [x] `TestRunPostprocess_RebuildsFlowsWhenBuilderConfigured` — `run_postprocess(flows=true)`가 실제 rebuild를 호출해 `flows_count`를 채우고 `skipped_steps`에서 `flows`를 제거
- [x] `TestRunPostprocess_FlowsSkippedWhenBuilderNil` — `FlowBuilder`가 없으면 기존처럼 `flows`를 skipped로 보고
- [x] `TestBuildOrUpdateGraph_FullPostprocess_RebuildsFlows` — `build_or_update_graph postprocess=full`이 stored flow rebuild를 실행

### 구현

- `internal/analysis/flows/builder.go` — `Builder` + `Rebuild(ctx, Config)` 추가
- `internal/mcp/deps.go` — `FlowBuilder` interface와 dependency field 추가
- `internal/mcp/handler_parse.go` — `run_postprocess` / `build_or_update_graph` flow rebuild wiring
- `internal/mcp/handler_graph.go` — persisted flow refresh hint 갱신
- `cmd/ccg/main.go` — MCP deps에 `flows.NewBuilder(...)` 주입

---

## Step 14: Automatic Postprocess Policy Engine

### 테스트

- [x] `TestAutoMigrate_CreatesPostprocessPolicyTables` — GORM test path가 policy state/log 테이블을 생성
- [x] `TestRunMigrations_SqliteAppliesPolicyTables` — versioned sqlite migration이 policy 테이블을 만들고 schema version을 올림
- [x] `TestPolicyEngine_DecideDefaultsToDegraded` — 명시적 요청이 없고 이력이 없으면 degraded를 선택
- [x] `TestPolicyEngine_DecideEscalatesAfterThreeFailures` — 같은 namespace+tool에서 최근 3회 연속 실패면 fail_closed로 승격
- [x] `TestPolicyStore_RecordAndUpsertState` — 실행 로그 append와 최신 state upsert가 저장됨
- [x] `TestBuildOrUpdateGraph_UsesEnginePolicyWhenCallerOmitsHint` — caller가 `postprocess_policy`를 안 넘기면 엔진 결정을 사용
- [x] `TestBuildOrUpdateGraph_CallerPolicyWinsOverEngine` — caller가 명시한 `postprocess_policy`가 엔진보다 우선
- [x] `TestRunPostprocess_RecordsPolicyDecision` — run_postprocess가 policy decision과 결과를 기록

### 구현

- `internal/model/postprocess_policy.go` — policy state/log 모델 추가
- `internal/store/gormstore/gormstore.go` — AutoMigrate에 policy 모델 추가
- `internal/migrationfs/sqlite/000003_postprocess_policy.{up,down}.sql` — sqlite migration 추가
- `internal/migrationfs/postgres/000003_postprocess_policy.{up,down}.sql` — postgres migration 추가
- `internal/policy/postprocess/engine.go` — 자동 postprocess 정책 결정 엔진 추가
- `internal/policy/postprocess/store.go` — policy state/log persistence 추가
- `internal/mcp/deps.go` — PolicyEngine dependency 추가
- `internal/mcp/handler_parse.go` — build/run_postprocess 경로에 policy 결정/기록 연결
- `cmd/ccg/main.go` — migration schema version bump, schema parity, engine wiring 추가

---

## Step 15: Parser eval edge endpoint normalization collapse

### 테스트

- [x] `TestNormalizeEdges_PreservesParserStageEndpoints` — parser-stage nodes/edges의 ID==0 상태에서 fingerprint + node context로 from/to 엔드포인트가 서로 collapse되지 않음
- [x] `TestNormalizeEdges_ImportsFromUsesFilePathAndFullTarget` — imports_from는 From을 file path로 유지하고 fingerprint의 전체 target(import path with colon 포함)을 To로 복원
- [x] `TestNormalizeEdges_TestedByUsesTestQNameAsFrom` — tested_by는 From을 test qualified name으로, To를 production callee로 복원
- [x] `TestNormalizeEdges_ImplementsUsesFingerprintEndpoints` — implements는 fallback owner가 아니라 fingerprint에서 From=impl, To=iface를 복원
- [x] `TestNormalizeEdges_CallsUsesLastColonSafeTargetAndNumericLine` — calls는 target에 콜론이 있어도 마지막 콜론 기준으로 QN와 trailing line을 복원
- [x] `TestNormalizeEdges_ContainsUsesFullTargetAfterFilePathPrefix` — contains는 filePath prefix 뒤의 전체 QN을 그대로 복원

### 구현

- `internal/eval/parser.go` — parser-stage fingerprint 형식에서 edge endpoint를 복원하는 최소 정상화 로직 추가
- `internal/eval/runner.go` — ID 기반 맵 대신 parser node 컨텍스트를 NormalizeEdges에 전달

---

## Step 16: Parser eval inherits edge normalization

### 테스트

- [x] `TestNormalizeEdges_InheritsUsesFingerprintEndpoints` — inherits는 v2 fingerprint helper의 colon-safe parent QN을 From/To로 복원

### 구현

- `internal/eval/parser.go` — `EdgeKindInherits` case를 추가해 fingerprint endpoint를 복원

---

## Step 17: Search query corpus Java case expansion

### 테스트

- [x] `TestLoadQueryCorpus_IncludesJavaCase` — `testdata/eval/queries.json`에 Java query case가 포함되어 총 6개가 되고 `class:UserService@Sample.java`가 relevant에 존재
- [x] `TestLoadQueryCorpus_IncludesRustCase` — `testdata/eval/queries.json`에 Rust query case가 포함되어 총 7개가 되고 `function:get_user@sample.rs`가 relevant에 존재
- [x] `TestLoadQueryCorpus_UsesFixtureAlignedQueryTexts` — Python/TypeScript/Rust query text와 relevant ID가 실제 fixture와 일치함을 확인
- [x] `TestLoadQueryCorpus_IncludesJavaScriptCase` — `testdata/eval/queries.json`에 JavaScript query case가 포함되어 총 8개가 되고 `function:getUser@sample.js`가 relevant에 존재
- [x] `TestLoadQueryCorpus_IncludesKotlinCase` — `testdata/eval/queries.json`에 Kotlin query case가 포함되어 총 9개가 되고 `function:getUser@Sample.kt`가 relevant에 존재
- [x] `TestLoadQueryCorpus_IncludesPHPCase` — `testdata/eval/queries.json`에 PHP query case가 포함되어 총 10개가 되고 `function:getUser@sample.php`가 relevant에 존재
- [x] `TestLoadQueryCorpus_CoversRemainingLanguages` — `testdata/eval/queries.json`에 Ruby/C/C++/Lua query cases가 추가되어 총 14개가 되고 네 언어의 relevant ID가 모두 존재

### 구현

- `testdata/eval/queries.json` — Ruby/C/C++/Lua query cases 4개 추가

---

## Step 18: Eval workflow script

### 테스트

- [x] `test_eval_script_exists_and_is_executable` — `scripts/eval.sh`가 존재하고 실행 가능해야 함
- [x] `test_eval_default_db_path_uses_temp_dir` — 기본 DB 경로 helper가 temp dir 아래 `eval.db`를 반환해야 함
- [x] `test_eval_build_and_run_cmds_use_shared_db_namespace_and_corpus` — migrate/build/eval command helper가 같은 DB/namespace/corpus를 사용해야 함
- [x] `test_eval_main_invokes_build_then_eval_in_order` — main이 migrate 후 corpus build, 같은 DB로 eval을 실행해야 함

### 구현

- `scripts/eval.sh` — eval corpus build + eval 실행 wrapper
- `scripts/eval_test.sh` — shell helper/function tests
- `guide/eval.md` — script usage 문서화

---

## Step 19: Search eval result key normalization

### 테스트

- [x] `TestNodeToKeys_UsesBaseFilePathForEvalMatching` — search eval key가 corpus relevant ID와 맞도록 `FilePath`의 basename만 사용해야 함

### 구현

- `internal/cli/eval.go` — `nodeToKeys()`가 `filepath.Base(n.FilePath)`를 사용하도록 조정
- `internal/cli/eval_test.go` — result key normalization test 추가

---

## Step 20: Search FTS content path-token enrichment

### 테스트

- [x] `TestBuildSearchDocuments_IndexesFileBaseAndLanguageTokens` — search document content가 파일 basename과 확장자/언어 alias 토큰을 포함해야 함

### 구현

- `internal/service/indexer.go` — `buildSearchContent()` helper 추출 및 file path token append
- `internal/service/indexer_test.go` — path token indexing test 추가

---

## Step 21: Exact-name match promotion for bare identifier queries

### 테스트

- [x] `TestExtractExactNameToken` — 단일 identifier query만 exact-name promotion 대상으로 추출해야 함
- [x] `TestSQLiteFTS_Query_PromotesExactNameMatch` — bare identifier query에서 exact `Name` match가 top-1로 승격되어야 함
- [x] `TestPromoteExactNameMatch_DoesNotPromoteMultiTokenQuery` — multi-token query에는 exact-name promotion을 적용하지 않아야 함
- [x] `TestPromoteExactNameMatch_DoesNotPromoteSubstringMatch` — substring match는 승격하면 안 됨
- [x] `TestPromoteExactNameMatch_PreservesStableOrderAmongNonMatches` — 승격 대상이 없으면 기존 순서를 유지해야 함

### 구현

- `internal/store/search/sanitize.go` — `extractExactNameToken()` / `promoteExactNameMatch()` 추가
- `internal/store/search/sqlite.go` — SQLite query result post-processing에 exact-name promotion 적용
- `internal/store/search/postgres.go` — Postgres query result post-processing에 exact-name promotion 적용
- `internal/store/search/sanitize_test.go` — promotion 경계조건 unit test 추가

---

## Step 22: Multi-relevant corpus contract for ambiguous bare-name queries

### 테스트

- [x] `TestLoadQueryCorpus_BareNameQueriesAreMultiRelevant` — bare identifier query는 동일 이름이 여러 언어에 존재하면 multi-relevant로 취급해야 함

### 구현

- `testdata/eval/queries.json` — `UserService`, `get_user`, `getUser` bare query relevant IDs를 다중 언어로 확장
- `internal/eval/search_test.go` — multi-relevant corpus contract test 추가
- `guide/eval.md` — bare query vs language-qualified query relevance 규칙 문서화

---

## Step 23: Negative-control search eval support

### 테스트

- [x] `TestFalsePositiveRate_EmptyRankedReturnsZero` — negative-control query가 결과를 반환하지 않으면 false positive rate는 0이어야 함
- [x] `TestFalsePositiveRate_NonEmptyRankedReturnsOne` — negative-control query가 어떤 결과라도 반환하면 false positive rate는 1이어야 함
- [x] `TestEvaluateQueries_NegativeControlExcludedFromRankingAverages` — negative-control case는 ranking average 계산에서 제외되어야 함
- [x] `TestEvaluateQueries_NegativeControlDetectsLeak` — negative-control case가 결과를 반환하면 false positive로 집계되어야 함
- [x] `TestEvaluateQueries_PositiveBaselineUnchanged` — negative-control 지원 추가 후에도 positive-only baseline은 그대로여야 함
- [x] `TestLoadQueryCorpus_IncludesNegativeControl` — real corpus가 최소 1개의 negative-control query를 포함해야 함

### 구현

- `internal/eval/metrics.go` — `FalsePositiveRate()` 추가
- `internal/eval/schema.go` — `negative_queries`, `negative_false_positives`, `negative_pass_rate` 필드 추가
- `internal/eval/search.go` — negative-control query를 ranking average에서 분리 집계
- `internal/eval/metrics_test.go` / `internal/eval/search_test.go` — negative-control semantics tests 추가
- `testdata/eval/queries.json` — sentinel negative-control query 추가
- `guide/eval.md` — negative-control metric semantics 문서화

---

## Step 24: Render negative-control metrics in eval table

### 테스트

- [x] `TestWriteTable_RendersNegativeControlBlock` — negative-control metrics가 있을 때 human-readable table에 출력되어야 함
- [x] `TestWriteTable_OmitsNegativeBlock_WhenZeroNegatives` — negative-control metrics가 없으면 table에 출력되지 않아야 함

### 구현

- `internal/eval/runner.go` — search eval table에 `Negatives`, `FP`, `Pass Rate` conditional block 추가
- `internal/eval/runner_test.go` — negative-control table rendering tests 추가
- `guide/eval.md` — table output example 문서화

---

## Step 25: Per-query diagnostics for search eval JSON

### 테스트

- [x] `TestEvaluateQueries_EmitsPerQueryDiagnostics` — search eval이 per-query diagnostic records를 생성해야 함
- [x] `TestEvaluateQueries_AggregatesUnchangedAfterDiagnostics` — diagnostics 추가 후에도 aggregate metric semantics는 그대로여야 함

### 구현

- `internal/eval/schema.go` — `QueryDiagnostic` / `SearchReport.PerQuery` 추가
- `internal/eval/search.go` — positive/negative query별 diagnostic record 생성
- `guide/eval.md` — JSON `per_query` diagnostics 문서화

---

## Step 26: Top returned keys in per-query diagnostics

### 테스트

- [x] `TestEvaluateQueries_NegativeDiagnosticIncludesReturnedKeys` — negative diagnostic이 실제 반환된 result keys를 포함해야 함
- [x] `TestEvaluateQueries_PositiveDiagnosticIncludesReturnedKeys` — positive diagnostic이 실제 반환된 result keys를 포함해야 함
- [x] `TestEvaluateQueries_DiagnosticTopResultsCappedAtFive` — stored `top_results`는 최대 5개여야 함
- [x] `TestEvaluateQueries_AggregatesUnchangedAfterTopResults` — `top_results` 추가 후에도 aggregate semantics는 그대로여야 함

### 구현

- `internal/eval/schema.go` — `QueryDiagnostic.TopResults` 추가
- `internal/eval/search.go` — `capResults()` helper와 `top_results` population 추가
- `guide/eval.md` — `top_results` diagnostics 문서화
