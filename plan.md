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
