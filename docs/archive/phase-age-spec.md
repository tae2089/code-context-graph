# Apache AGE 그래프 DB 통합 — Tech Spec

> **목표**: 기존 GORM BFS 분석을 Apache AGE Cypher 쿼리로 대체.
> PostgreSQL 하나로 관계형 + 그래프를 모두 처리.
> SQLite 모드는 유지 (로컬 개발용 fallback).

## 아키텍처

```
[ccg build] → Parser → GORM (Node/Edge 테이블) + AGE (그래프)
                                                    ↓
[ccg search] → FTS5/tsvector ──────────────────────→ 결과
[ccg query]  → Cypher ─────────────────────────────→ 경로/영향분석
```

- PostgreSQL + AGE: 그래프 쿼리 (Cypher) + 관계형 (GORM)
- SQLite: 기존 Go BFS 유지 (AGE 없이 동작)

## Docker

```yaml
services:
  age:
    image: apache/age
    environment:
      POSTGRES_USER: ccg
      POSTGRES_PASSWORD: ccg
      POSTGRES_DB: ccg
    ports:
      - "5455:5432"
```

## Go Driver

`github.com/rhizome-ai/apache-age-go` — AGType 파서 + Cypher 실행

## 핵심 Cypher 쿼리

### 그래프 생성
```sql
SELECT * FROM ag_catalog.create_graph('code_graph');
```

### 노드 생성
```cypher
CREATE (:Function {node_id: 1, qualified_name: 'auth.Login', name: 'Login', kind: 'function'})
```

### 엣지 생성
```cypher
MATCH (a {node_id: 1}), (b {node_id: 2})
CREATE (a)-[:CALLS {line: 10}]->(b)
```

### 영향 분석 (BFS → Cypher)
```cypher
MATCH (start {qualified_name: 'auth.Login'})-[*1..3]-(affected)
RETURN DISTINCT affected
```

### 호출 경로
```cypher
MATCH path = (a {qualified_name: 'handler.Login'})-[:CALLS*]->(b {qualified_name: 'db.Save'})
RETURN path
```

### 미사용 코드
```cypher
MATCH (n:Function)
WHERE NOT ()-[:CALLS]->(n) AND n.kind <> 'test' AND n.kind <> 'file'
RETURN n
```

## 구현 순서

1. docker-compose.yml 업데이트 (apache/age)
2. AGE Go 드라이버 추가
3. `internal/store/agestore/` 패키지 생성
4. build 시 AGE 그래프 동기화
5. Cypher 기반 분석 쿼리 구현
6. MCP 도구에서 AGE 쿼리 사용
