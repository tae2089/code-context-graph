# Eval

[English](../eval.md)

`ccg eval`은 로컬 평가 코퍼스를 기준으로 파서 정확도와 검색 품질을 측정합니다. 라이브 서비스 메트릭이 아니라 오프라인 품질 게이트로 취급하십시오.

## 측정 대상

| Suite | 측정 대상 | 주요 지표 |
|-------|-----------|-----------|
| `parser` | 소스 파일에서 기대한 그래프 노드와 엣지를 만드는지 | Node/edge precision, recall, F1 |
| `search` | 검색 결과가 기대 결과를 상위 순위에 노출하는지 | P@1, P@3, P@5, R@5, MRR, nDCG@5 |

Parser eval은 현재 파서 출력과 `*.golden.json` 파일을 비교합니다. Search eval은 `queries.json`의 쿼리를 실행하고 ranked result를 `relevant` 목록과 비교합니다.

## 코퍼스 구조

기본 코퍼스는 `testdata/eval`입니다.

```text
testdata/eval/
  go/sample.go
  go/sample.go.golden.json
  python/sample.py
  python/sample.py.golden.json
  queries.json
```

용어:

| 용어 | 의미 |
|------|------|
| corpus | 평가에 사용하는 데이터셋 |
| golden | 정답 기준으로 사용하는 기대 출력 |
| golden corpus | 입력 소스와 기대 출력이 포함된 평가 데이터셋 |
| expected | golden 파일 또는 relevant 검색 목록의 기대 결과 |
| actual | 현재 파서 또는 검색 백엔드가 만든 결과 |

## Parser 지표

Parser eval은 node를 `kind:name@file`, edge를 `kind:from->to` 형태로 정규화한 뒤 set 기반 classification metric을 계산합니다.

| 지표 | 의미 |
|------|------|
| true positive | expected와 actual 모두에 있음 |
| false positive | actual에는 있지만 expected에는 없음 |
| false negative | expected에는 있지만 actual에는 없음 |
| precision | `true_positive / (true_positive + false_positive)` |
| recall | `true_positive / (true_positive + false_negative)` |
| F1 | precision과 recall의 조화 평균 |

Precision이 낮으면 파서가 불필요한 node/edge를 만든다는 뜻에 가깝습니다. Recall이 낮으면 기대한 node/edge를 놓친다는 뜻에 가깝습니다.

## Search 지표

Search eval은 `queries.json`을 사용합니다.

```json
{
  "queries": [
    {
      "query": "impact analysis",
      "relevant": ["function:ImpactRadius@internal/analysis/impact/impact.go"],
      "k": 5
    }
  ]
}
```

| 지표 | 의미 |
|------|------|
| P@1 / P@3 / P@5 | 상위 k개 결과 중 relevant 결과의 비율 |
| R@5 | 전체 relevant 결과 중 상위 5개 안에 들어온 비율 |
| MRR | 첫 번째 relevant 결과 순위의 역수 |
| nDCG@5 | 상위 5개에서 relevant 결과가 얼마나 위쪽에 정렬됐는지 |

## 명령어

```bash
# parser와 search eval 실행
ccg eval

# parser만 실행
ccg eval --suite parser

# search만 실행
ccg eval --suite search

# 다른 코퍼스 사용
ccg eval --corpus testdata/eval

# 기계가 읽기 쉬운 JSON 출력
ccg eval --format json
```

Golden 파일은 parser diff를 확인하고 새 출력이 의도한 결과임을 확인한 뒤 업데이트하십시오.

```bash
ccg eval --suite parser --update
```

`--update`는 parser golden 파일용입니다. 현재 parser 출력을 새 expected result로 기록합니다.

## 결과 해석

- `parser` precision 하락: 새로 생긴 node/edge가 false positive인지 확인합니다.
- `parser` recall 하락: 누락된 node/edge가 false negative인지 확인합니다.
- `search` P@1/MRR 하락: 정답은 있지만 ranking이 나빠졌을 가능성이 큽니다.
- `search` R@5 하락: relevant 결과가 상위 검색 창에 들어오지 못한 상태입니다.
- eval 점수가 좋아도 코퍼스 범위 안의 품질만 보장합니다. 파서나 검색을 고칠 때는 대표 케이스를 코퍼스에 추가하십시오.
