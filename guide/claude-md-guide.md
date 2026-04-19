# CLAUDE.md Guide for CCG Users

Copy and adapt the template below into the CLAUDE.md of any project that uses CCG.

---

## Template

````markdown
## Code Knowledge Graph (CCG)

This project uses the [code-context-graph](https://github.com/tae2089/code-context-graph) MCP server registered in `.mcp.json`.

### Code Analysis Flow

```
get_minimal_context          ← always start here (graph state + recommended tools)
        │
        ├─ no graph → build_or_update_graph(path: ".")
        │
        ├─ find code → search(query: "keyword")
        │               → query_graph(pattern: "callers_of", target: "pkg.Func")
        │
        ├─ change impact → detect_changes(repo_root: ".")
        │                   → get_impact_radius(qualified_name: "...", depth: 3)
        │                   → get_affected_flows(repo_root: ".")
        │
        ├─ understand structure → get_architecture_overview()
        │                          → list_communities()
        │
        └─ after code change → build_or_update_graph(path: ".", full_rebuild: false)
```

### Tips

- `search` covers annotations (`@intent`, `@domainRule`, etc.) as well as code
- Narrow the scope with a path: `search(path: "internal/auth")`
- In MSA environments, use the `workspace` parameter on all tools to isolate services
````

---

## Minimal Version

```markdown
## CCG

Call `get_minimal_context` first when analyzing code. It reports graph state and suggests next steps.
```
