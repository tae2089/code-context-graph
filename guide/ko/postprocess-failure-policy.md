# 사후 처리 실패 정책 (Postprocess Failure Policy)

[English](../postprocess-failure-policy.md)

이 가이드는 파생된 그래프 상태를 재생성할 수 있는 두 가지 MCP 도구에서 `ok`, `degraded`, `fail_closed`, `skipped_steps`가 어떻게 보고되는지 설명합니다:

- `build_or_update_graph`
- `run_postprocess`

또한 호출자가 명시적인 `postprocess_policy`를 제공하지 않았을 때 `degraded` 또는 `fail_closed`를 선택하는 자동 정책 엔진에 대해서도 설명합니다.

## 용어 정의 (Terms)

| 용어 | 의미 |
|------|---------|
| `ok` | 요청된 작업이 성공했거나 건너뛴 단계만 남은 상태입니다. |
| `degraded` | 요청된 일부 사후 처리 단계가 실패했지만, 도구가 구조화된 성공 결과를 반환한 상태입니다. |
| `fail_closed` | 사후 처리 단계의 오류를 허용하지 않는 실패 정책입니다. 요청된 단계가 실패하면 도구는 해당 호출에 대해 에러를 반환합니다. |
| `skipped_steps` | 호출자가 비활성화했거나, 선택된 모드에 포함되지 않았거나, 필요한 빌더/백엔드가 설정되지 않아 시도되지 않은 단계들입니다. |

## 자동 정책 엔진 (Automatic policy engine)

호출자가 `postprocess_policy`를 생략하면 CCG는 다음 순서에 따라 유효 정책을 결정합니다:

1. 호출자가 값을 명시적으로 제공한 경우 해당 값이 우선합니다.
2. 제공되지 않은 경우, 자동 정책 엔진은 동일한 `(namespace, tool)` 쌍에 대한 최근 실행 기록을 확인합니다.
3. 기본 정책은 `degraded`입니다.
4. 동일한 `(namespace, tool)`에 대해 **3회 연속 `degraded` 실행**이 발생하면, 자동 정책은 `fail_closed`로 격상됩니다.
5. 이후 `ok` 실행이 발생하면 연속 실패 기록이 초기화됩니다.

정책 상태는 다음과 같이 저장됩니다:

- `ccg_postprocess_policy_state`: 현재 유효 상태
- `ccg_postprocess_run_logs`: 추가 전용 실행 이력

운영자를 위해 CCG는 두 가지 경량 제어 인터페이스를 제공합니다:

- `get_postprocess_policy`: 현재 fail-closed 항목 및 최근 실패 내역을 반환합니다.
- `reset_postprocess_policy`: 현재 네임스페이스의 특정 도구에 대해 리셋 마커 실행을 기록합니다.

리셋 경로는 실행 이력을 삭제하지 않습니다. 대신 소스가 `reset`인 `ok` 마커를 삽입하여, 자동 결정 엔진이 사용하는 연속 실패 기록을 안전하게 끊어줍니다.

실행 로그는 쓰기 작업 후 기회적으로(opportunistically) 정리되어, 각 `(namespace, tool)`이 영원히 늘어나는 대신 제한된 최신 이력만 유지하도록 관리됩니다.

## 네임스페이스 격리 및 공유 상태

자동 정책 결정 및 사후 처리 결과의 범위는 현재 `namespace`로 제한됩니다.

- 정책 격상은 `(namespace, tool)`별로 추적됩니다.
- 파생 상태 재생성(`flows`, `communities`, `search_documents`, `fts`)은 활성 네임스페이스에만 적용됩니다.
- 한 네임스페이스에서의 실패가 다른 네임스페이스를 직접 `degraded` 또는 `fail_closed`로 표시하지 않습니다.

정상적인 운영 상황에서 이는 네임스페이스 `A`에서 재생성이 실패하여 해당 네임스페이스만 오래된 상태가 되더라도, 네임스페이스 `B`는 정상적으로 쿼리를 수행할 수 있음을 의미합니다.

주요 예외는 스키마 마이그레이션 호환성과 같은 진정한 전역 운영 상태입니다. 예를 들어 `SchemaVersion`은 네임스페이스 범위가 아니므로, 사후 처리 정책과 파생 그래프 상태가 네임스페이스별로 격리되어 있더라도 전역 스키마 불일치는 전체 배포에 영향을 줄 수 있습니다.

## `build_or_update_graph`

`build_or_update_graph`는 두 단계로 구성됩니다:

1. 그래프 빌드/업데이트
2. `postprocess`에 의해 제어되는 선택적 사후 처리 작업

빌드/업데이트 단계 자체가 실패하면, 도구는 사후 처리 상태를 생성하기 전에 에러를 반환합니다.

### 입력 검증 및 하드 실패 (Hard failures)

| 조건 | 내부 동작 | 결과 |
|-----------|-------------------|--------|
| `path` 누락 | 실행 전 요청 검증 실패 | 에러 |
| `path`가 설정된 분석 루트 외부임 | 경로 검증 실패 | 에러 |
| `postprocess`가 `full`, `minimal`, `none` 중 하나가 아님 | 요청 검증 실패 | 에러 |
| `postprocess_policy`가 `degraded`, `fail_closed` 중 하나가 아님 | 요청 검증 실패 | 에러 |
| 그래프 빌드/업데이트 실패 | 빌드 또는 증분 업데이트 에러 반환 | 에러 |

### 사후 처리 모드 동작

| `postprocess` 값 | 시도되는 단계 | 설계상 건너뛰는 단계 |
|---------------------|-----------------|--------------------------|
| `full` | `flows`, `communities`, `search_documents`, `fts` | 없음 |
| `minimal` | `search_documents`, `fts` | `flows`, `communities` |
| `none` | 없음 | `flows`, `communities`, `search_documents`, `fts` |

### 상태 표 (Status table)

| 상황 | 예시 | 반환 결과 |
|-----------|---------|-----------------|
| 빌드/업데이트 성공 및 요청된 모든 사후 처리 단계 성공 | 전체 재빌드 + 활성화된 모든 파생 상태 갱신 완료 | `status="ok"` |
| 요청된 단계가 실패했으나 유효 정책이 `degraded`임 | `communities` 재생성 실패 | `status="degraded"`, `failed_steps`에 `communities` 포함 |
| 요청된 단계가 실패했고 유효 정책이 `fail_closed`임 | 자동 격상 또는 명시적 재정의 후 `fts` 재생성 실패 | 도구가 해당 호출에 대해 에러 반환 |
| 요청된 단계를 사용할 수 없음 | `FlowBuilder == nil` 또는 `SearchBackend == nil` | `skipped_steps`에 표시됨; 그 자체로 에러는 아님 |
| 선택된 모드에서 특정 단계를 제외함 | `postprocess="minimal"` 또는 `postprocess="none"` | `skipped_steps`에 표시됨; 그 자체로 에러는 아님 |

### 단계별 실패 매핑

| 단계 | 실패 원인 | `degraded` 정책 결과 | `fail_closed` 정책 결과 |
|------|---------------|--------------------------|-----------------------------|
| `flows` | `FlowBuilder.Rebuild()` 에러 반환 | `failed_steps += ["flows"]`, JSON 결과는 반환됨 | 실패가 기록된 후 도구가 에러 반환 |
| `communities` | `CommunityBuilder.Rebuild()` 에러 반환 | `failed_steps += ["communities"]`, JSON 결과는 반환됨 | 실패가 기록된 후 도구가 에러 반환 |
| `search_documents` | 검색 문서 갱신 실패 | `failed_steps += ["search_documents"]`, JSON 결과는 반환됨 | 실패가 기록된 후 도구가 에러 반환 |
| `fts` | `SearchBackend.Rebuild()` 에러 반환 | `failed_steps += ["fts"]`, JSON 결과는 반환됨 | 실패가 기록된 후 도구가 에러 반환 |

### 확인해야 할 응답 필드

| 필드 | 의미 |
|-------|---------|
| `status` | 전체 결과: `ok` 또는 `degraded` |
| `postprocess_policy` | 호출에 사용된 유효 정책 |
| `policy_source` | 호출자가 정책을 제공한 경우 `explicit`, 그 외에는 `auto` |
| `failed_steps` | 실행되었으나 실패한 요청 단계들 |
| `skipped_steps` | 의도적으로 실행되지 않은 요청 또는 모드 제어 단계들 |

## `run_postprocess`

`run_postprocess`는 이미 빌드된 그래프 상태에서만 작동합니다. 소스 파일을 다시 파싱하지 않습니다.

### 입력 검증 및 하드 실패

| 조건 | 내부 동작 | 결과 |
|-----------|-------------------|--------|
| `community_depth`가 `1..8` 범위를 벗어남 | 요청 검증 실패 | 에러 |
| `postprocess_policy`가 `degraded`, `fail_closed` 중 하나가 아님 | 요청 검증 실패 | 에러 |

### 요청 단계 동작

| 요청 플래그 | 시도되는 단계 | 건너뛰는 단계 |
|---------------|-----------------|---------------|
| `flows=true` | `flows` | `FlowBuilder == nil`인 경우 제외하고 없음 |
| `communities=true` | `communities` | `CommunityBuilder == nil`인 경우 제외하고 없음 |
| `fts=true` | `search_documents`, `fts` | `SearchBackend == nil`인 경우 제외하고 없음 |
| `flows=false` | 없음 | `flows` |
| `communities=false` | 없음 | `communities` |
| `fts=false` | 없음 | `search_documents`, `fts` |

### 상태 표

| 상황 | 예시 | 반환 결과 |
|-----------|---------|-----------------|
| 요청된 모든 단계 성공 | `flows=true, communities=true, fts=true` 및 모든 재생성 성공 | `status="ok"` |
| 요청된 단계가 실패했으나 유효 정책이 `degraded`임 | `search_documents` 갱신 실패 | `status="degraded"`, 실패한 단계 보고됨 |
| 요청된 단계가 실패했고 유효 정책이 `fail_closed`임 | 명시적 또는 자동 fail-closed 정책 하에 `flows` 재생성 실패 | 도구가 해당 호출에 대해 에러 반환 |
| 요청된 단계를 사용할 수 없음 | `CommunityBuilder == nil` 또는 `SearchBackend == nil` | `skipped_steps`에 표시됨; 그 자체로 에러는 아님 |
| 호출자가 단계를 비활성화함 | `flows=false` 또는 `fts=false` | `skipped_steps`에 표시됨; 그 자체로 에러는 아님 |

### 단계별 실패 매핑

| 단계 | 실패 원인 | `degraded` 정책 결과 | `fail_closed` 정책 결과 |
|------|---------------|--------------------------|-----------------------------|
| `flows` | `FlowBuilder.Rebuild()` 에러 반환 | `failed_steps += ["flows"]`, JSON 결과는 반환됨 | 실패가 기록된 후 도구가 에러 반환 |
| `communities` | `CommunityBuilder.Rebuild()` 에러 반환 | `failed_steps += ["communities"]`, JSON 결과는 반환됨 | 실패가 기록된 후 도구가 에러 반환 |
| `search_documents` | 검색 문서 갱신 실패 | `failed_steps += ["search_documents"]`, JSON 결과는 반환됨 | 실패가 기록된 후 도구가 에러 반환 |
| `fts` | `SearchBackend.Rebuild()` 에러 반환 | `failed_steps += ["fts"]`, JSON 결과는 반환됨 | 실패가 기록된 후 도구가 에러 반환 |

### 확인해야 할 응답 필드

| 필드 | 의미 |
|-------|---------|
| `status` | 전체 결과: `ok` 또는 `degraded` |
| `postprocess_policy` | 호출에 사용된 유효 정책 |
| `policy_source` | 호출자가 정책을 제공한 경우 `explicit`, 그 외에는 `auto` |
| `failed_steps` | 실행되었으나 실패한 요청 단계들 |
| `skipped_steps` | 비활성화되었거나 사용할 수 없는 단계들 |
| `flows_count` | 이번 실행에서 재생성된 저장된 흐름의 수 |
| `communities_count` | 이번 실행에서 재생성된 커뮤니티의 수 |
| `fts_indexed` | FTS 재생성이 완료된 경우 `1`, 그렇지 않으면 `0` |

## 운영 관련 읽기 (Operational reading)

| 관찰 사항 | 의미 | 일반적인 다음 조치 |
|-------------|---------|---------------------|
| `status="degraded"` | 요청은 완료되었으나, 일부 파생 상태가 오래되었거나 부분적으로만 갱신되었을 수 있음 | `failed_steps`를 조사하고, 실패한 백엔드/빌더/설정을 수정한 후 도구 재실행 |
| `fail_closed` 하에서 도구가 에러 반환 | 유효 정책상 해당 호출에서 실패를 용납할 수 없는 것으로 판단함 | 근본적인 실패 원인을 수정하고 재실행; 필요한 경우 일시적으로 명시적인 `postprocess_policy="degraded"` 사용 |
| `skipped_steps`가 비어 있지 않음 | 요청된 작업이 의도적으로 시도되지 않음 | 호출자가 단계를 비활성화했는지 또는 필요한 백엔드/빌더가 누락되었는지 확인 |
| 자동 정책이 `fail_closed`로 전환됨 | 동일한 `(namespace, tool)`에 대한 최근 실패가 격상 임계값을 초과함 | 이를 일회성 경고가 아닌 지속적인 운영 문제로 취급하여 조치 |
