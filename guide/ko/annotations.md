# 커스텀 어노테이션 (Custom Annotations)

[English](../annotations.md)

코드에 구조화된 메타데이터를 추가하여 AI, 생성 문서, RAG, 집중 검색이 비즈니스 컨텍스트를 활용할 수 있도록 합니다. 어노테이션은 `ccg search`를 위해 인덱싱되며, RAG 인덱스의 입력이 되는 생성 Markdown에도 반영됩니다.

LLM 에이전트의 자연어 기반 코드 탐색에는 docs/RAG 경로를 먼저 사용하십시오. `ccg docs`를 실행한 뒤 MCP `retrieve_docs`, `get_rag_tree`, `get_doc_content`를 사용합니다. `ccg search`는 어노테이션/키워드에 매칭되는 심볼 후보 목록이 필요할 때 사용하십시오.

어노테이션 품질은 `ccg lint`에 의해 검증됩니다. `unannotated`, `incomplete`, `dead-ref`, `contradiction`, `drifted`와 같은 카테고리의 의미는 [Lint 가이드](lint.md)를 참조하십시오.

## 파일 레벨 (File-level)

```go
// @index 사용자 인증 및 세션 관리 서비스.
package auth
```

## 함수 레벨 (Function-level)

```go
// AuthenticateUser는 자격 증명을 검증하고 세션을 생성합니다.
// 로그인 API 핸들러에서 호출됩니다.
//
// @param username 사용자 로그인 ID
// @param password 평문 비밀번호
// @return 성공 시 JWT 토큰
// @intent 시스템 접근 권한을 부여하기 전에 사용자 신원을 확인
// @domainRule 5회 연속 실패 시 계정 잠금
// @sideEffect audit_log 테이블에 로그인 시도 기록
// @mutates user.FailedAttempts, user.LockedUntil
// @requires user.IsActive == true
// @ensures err == nil이면 24시간 유효한 JWT 반환
func AuthenticateUser(username, password string) (string, error) {
```

## 사용 가능한 태그 (Available Tags)

| 태그 | 목적 | 예시 |
|-----|---------|---------|
| `@index` | 파일/패키지 설명 | `@index 결제 처리 서비스` |
| `@intent` | 함수가 존재하는 이유 | `@intent 세션 생성 전 자격 증명 검증` |
| `@domainRule` | 비즈니스 규칙 | `@domainRule 5회 실패 시 계정 잠금` |
| `@sideEffect` | 부작용(Side effect) | `@sideEffect 알림 이메일 발송` |
| `@mutates` | 상태 변경 | `@mutates user.FailedAttempts, session.Token` |
| `@requires` | 사전 조건 | `@requires user.IsActive == true` |
| `@ensures` | 사후 조건 | `@ensures session != nil` |
| `@param` | 파라미터 설명 | `@param username 로그인 ID` |
| `@return` | 반환 값 설명 | `@return 성공 시 JWT 토큰` |
| `@see` | 관련 함수 또는 CCG ref | `@see SessionManager.Create`, `@see ccg://auth-svc/internal/auth/token.go#ValidateToken` |

`@intent`는 특히 중요합니다. 어노테이션은 있지만 `@intent`가 없는 심볼은 `ccg lint`에서 `incomplete`로 보고되기 때문입니다. `@see` 태그 또한 린트 대상이며, 존재하지 않는 심볼을 가리키는 경우 `dead-ref` 결과가 발생할 수 있습니다.

다른 네임스페이스의 코드를 함께 봐야 하는 동작은 `@see`에 `ccg://{namespace}/{path}#{symbol}` 형식으로 기록합니다. 이유는 의미 태그에 두고, 구체적인 대상은 `@see`에 둡니다.

```go
// @sideEffect auth-svc에 토큰 검증 감사 기록을 남긴다.
// @see ccg://auth-svc/internal/audit/token_audit.go#RecordTokenAudit
```

path와 symbol은 선택 사항입니다. `ccg://auth-svc/internal/auth`는 패키지/경로 범위, `ccg://auth-svc/`는 네임스페이스 전체를 가리킬 수 있습니다.

## Retrieval 품질 (Retrieval Quality)

어노테이션은 retrieval feature입니다. `retrieve_docs`와 생성 문서는
`@index`, `@intent`, `@domainRule`, `@sideEffect`, `@requires`, `@ensures`,
`@see` 같은 구조화된 bucket을 파일 단위 근거로 점수화합니다. 자연어 검색
품질은 좋은 어노테이션으로 올라가지만, 태그가 실제 동작을 설명할 때만
효과가 있습니다.

필요에 맞게 태그를 선택하십시오:

| 상황 | 권장 태그 |
|------|-----------|
| 파일/패키지가 하나의 검색 단위로 발견되어야 함 | `@index` |
| public 함수, handler, CLI command, service method, UI workflow의 목적이 중요함 | `@intent` |
| 정책, 제약, false-positive/false-negative 기준, 운영 규칙이 중요함 | `@domainRule` |
| DB/파일 write, 네트워크 호출, 캐시 변경, 중요한 로그, 프로세스 실행이 있음 | `@sideEffect` |
| receiver 또는 입력 상태를 변경함 | `@mutates` |
| 입력 계약이나 결과 보장이 호출자에게 중요함 | `@requires`, `@ensures` |
| 다른 구현이나 namespace로 이동해야 이해됨 | `@see` |

좋은 retrieval 어노테이션은 엔지니어나 LLM이 자연스럽게 물어볼 표현을
사용하되, 코드의 실제 역할을 벗어나지 않습니다. 예를 들어 UI graph
component가 resolved `ccg://` node를 focus한다면 어노테이션에도 그 사실이
드러나야 합니다. 점수를 올리기 위해 관련 없는 키워드를 넣지 마십시오.
넓거나 거짓인 표현은 실제 구현이 아닌 파일을 위로 올립니다.

과한 어노테이션은 피하십시오:

- 단순 getter/setter, 작은 wrapper, 자명한 한 줄 helper는 건너뜁니다.
- 각 태그가 별도 근거를 추가하지 않는다면 같은 키워드를 반복하지 않습니다.
- 모호한 `@intent` 여러 줄보다 정확한 `@domainRule` 하나가 낫습니다.
- `@see`는 실제 이동 가치가 있는 연결, 특히 cross-namespace ref에 사용합니다.

## AI 기반 어노테이션 (AI-Driven Annotation)

`/ccg-annotate` 스킬을 사용할 수 있는 코딩 에이전트는 코드베이스를 분석하여
어노테이션을 생성할 수 있습니다:

```
사용자: "이 프로젝트에 어노테이션을 추가해줘"
Agent: 코드 분석 → @intent, @domainRule, @sideEffect, @mutates 생성
       → 어노테이션 작성 → 인덱스 재빌드
       → 이제 비즈니스 컨텍스트로 검색 가능
```

### CLI

```bash
# 어노테이션 예시 보기
ccg example go
ccg example python

# 전체 태그 레퍼런스
ccg tags
```

### 스킬 (Skill)

스킬을 지원하는 코딩 에이전트에서 `/ccg-annotate` 스킬을 직접 사용하십시오:

```
/ccg-annotate annotate internal/   — AI를 사용하여 어노테이션 자동 생성
```
