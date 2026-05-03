# 커스텀 어노테이션 (Custom Annotations)

[English](../annotations.md)

코드에 구조화된 메타데이터를 추가하여 AI와 검색 엔진이 비즈니스 컨텍스트를 활용할 수 있도록 합니다. 어노테이션은 인덱싱되어 `ccg search`를 통해 검색할 수 있습니다.

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
| `@see` | 관련 함수 | `@see SessionManager.Create` |

`@intent`는 특히 중요합니다. 어노테이션은 있지만 `@intent`가 없는 심볼은 `ccg lint`에서 `incomplete`로 보고되기 때문입니다. `@see` 태그 또한 린트 대상이며, 존재하지 않는 심볼을 가리키는 경우 `dead-ref` 결과가 발생할 수 있습니다.

## AI 기반 어노테이션 (AI-Driven Annotation)

Claude Code는 코드베이스를 분석하여 어노테이션을 자동으로 생성할 수 있습니다:

```
사용자: "이 프로젝트에 어노테이션을 추가해줘"
Claude: 코드 분석 → @intent, @domainRule, @sideEffect, @mutates 생성
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

Claude Code에서 `/ccg-annotate` 스킬을 직접 사용하십시오:

```
/ccg-annotate annotate internal/   — AI를 사용하여 어노테이션 자동 생성
```
