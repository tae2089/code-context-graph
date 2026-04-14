---
name: ccg-analyze
description: code-context-graph — code analysis & architecture. Impact analysis, flow tracing, dead code detection, community structure.
user-invocable: true
---

# code-context-graph — Code Analysis & Architecture

Advanced code analysis tools for impact assessment, flow tracing, dead code detection, and architecture understanding.

## When to use

| User intent | MCP tool |
|-------------|----------|
| "What calls this function?" | `query_graph` (pattern: callers_of) — in `/ccg` |
| "Impact of changing X" | `get_impact_radius` (depth: 3) |
| "Trace the call chain" | `trace_flow` |
| "Large functions" | `find_large_functions` |
| "Dead code" | `find_dead_code` |
| "What changed?" | `detect_changes` |
| "Affected flows" | `get_affected_flows` |
| "Module structure" | `list_communities` |
| "Architecture overview" | `get_architecture_overview` |
| "Test coverage gaps" | `get_community` (include coverage) |

## MCP Tools (10)

| Tool | Description |
|------|-------------|
| `get_impact_radius` | BFS blast-radius analysis — find all nodes affected within N hops |
| `trace_flow` | Call-chain flow tracing from a starting node |
| `find_large_functions` | Functions exceeding line threshold (default: 50 lines) |
| `find_dead_code` | Unused code detection — functions with zero callers |
| `detect_changes` | Git diff risk scoring — changed files with impact assessment |
| `get_affected_flows` | Flows affected by code changes |
| `list_flows` | List all traced flows in the graph |
| `list_communities` | List module communities (Louvain algorithm) |
| `get_community` | Community details including coverage metrics |
| `get_architecture_overview` | Architecture summary with coupling analysis |

## Usage Examples

### Impact analysis before refactoring
```
User: "이 함수 변경하면 영향 범위가 어떻게 돼?"
→ get_impact_radius(name: "pkg.FunctionName", depth: 3)
→ Returns: affected nodes, edge count, blast radius visualization
```

### Dead code cleanup
```
User: "사용 안 하는 코드 찾아줘"
→ find_dead_code()
→ Returns: functions with 0 callers, grouped by file
```

### Architecture review
```
User: "모듈 구조 보여줘"
→ list_communities() + get_architecture_overview()
→ Returns: community clusters, inter-module coupling, dependency graph
```

### Change risk assessment
```
User: "이번 변경 위험도 체크해줘"
→ detect_changes() + get_affected_flows()
→ Returns: changed files with risk scores, affected call flows
```

## Prerequisites

Graph must be built first. If `ccg.db` doesn't exist, run `ccg build .` (see `/ccg` skill).
