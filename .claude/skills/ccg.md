---
name: ccg
description: code-context-graph CLI — 코드 그래프 빌드, 검색, Cypher 쿼리 실행
user-invocable: true
---

# code-context-graph CLI

코드베이스를 파싱하여 지식 그래프를 구축하고 검색/분석하는 도구입니다.

## 사용법

사용자가 `/ccg` 뒤에 서브커맨드를 입력하면 해당 CLI 명령을 실행합니다.

### 서브커맨드

| 커맨드 | 설명 | 예시 |
|--------|------|------|
| `build [dir]` | 디렉토리를 파싱하여 그래프 빌드 | `/ccg build .` |
| `build --graph [dir]` | 그래프 빌드 + Apache AGE 동기화 | `/ccg build --graph .` |
| `build --embed [dir]` | 그래프 빌드 + 벡터 임베딩 | `/ccg build --embed .` |
| `update [dir]` | 변경된 파일만 증분 업데이트 | `/ccg update .` |
| `status` | 그래프 통계 (노드/엣지/파일 수) | `/ccg status` |
| `search <query>` | FTS 키워드 검색 | `/ccg search "인증"` |
| `search --semantic <q>` | 벡터 시맨틱 검색 | `/ccg search --semantic "결제 관련 코드"` |
| `query <cypher>` | Cypher 쿼리 직접 실행 | `/ccg query "MATCH (n:Function) RETURN n LIMIT 5"` |

## 실행 방법

사용자 입력에서 서브커맨드와 인자를 파싱하여 Bash로 실행합니다:

```bash
./ccg {subcommand} {args}
```

## 인자가 없으면

`/ccg`만 입력된 경우 다음을 안내합니다:

```
사용 가능한 ccg 커맨드:
  /ccg build [dir]     — 코드 그래프 빌드
  /ccg status          — 그래프 통계
  /ccg search <query>  — 키워드 검색
  /ccg query <cypher>  — Cypher 쿼리 실행
```

## Cypher 쿼리 예시

유용한 Cypher 쿼리를 사용자에게 제안할 수 있습니다:

```cypher
-- 모든 함수 호출 관계
MATCH (a:Function)-[:CALLS]->(b:Function) RETURN a.name, b.name

-- 특정 함수의 blast-radius (3홉)
MATCH (start {name: 'Login'})-[*1..3]-(affected) RETURN DISTINCT affected.name

-- 호출 경로 찾기
MATCH path = (a {name: 'Handler'})-[:CALLS*]->(b {name: 'Save'}) RETURN path

-- 미사용 코드 탐지
MATCH (n:Function) WHERE NOT ()-[:CALLS]->(n) RETURN n.qualified_name

-- 가장 많이 호출되는 함수
MATCH ()-[:CALLS]->(n:Function) RETURN n.name, count(*) AS calls ORDER BY calls DESC LIMIT 10
```
