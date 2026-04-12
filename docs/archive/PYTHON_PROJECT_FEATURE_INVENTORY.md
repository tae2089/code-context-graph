# Code Review Graph — Complete Feature Inventory

**Project**: https://github.com/tirth8205/code-review-graph  
**Current Version**: 2.2.1 (April 2026)  
**Type**: Python MCP Server for LLM-powered code review  

---

## 📊 1. MCP TOOLS (24 TOTAL)

### 1.1 Core Graph & Build Tools (3 tools)

| Tool | Parameters | Description | Output |
|------|-----------|-------------|--------|
| **`build_or_update_graph_tool`** | `full_rebuild` (bool), `repo_root` (str), `base` (str), `postprocess` (str), `recurse_submodules` (bool) | Build or incrementally update the knowledge graph. Full rebuild re-parses all files; incremental updates only changed files. | `{"status": "ok", "elapsed_ms": int, "nodes_created": int, "edges_created": int, "build_phase": str}` |
| **`run_postprocess_tool`** | `flows` (bool), `communities` (bool), `fts` (bool), `repo_root` (str) | Run post-processing steps independently: execution flows, community detection, full-text search. | `{"status": "ok", "flows_count": int, "communities_count": int, "fts_indexed": int}` |
| **`get_minimal_context_tool`** | `task` (str), `changed_files` (list[str]), `repo_root` (str), `base` (str) | Ultra-compact entry point (~100 tokens). Returns risk score, top communities/flows, and suggested next tools. | Minimal JSON with stats, risk, key entities, next tool suggestions |

### 1.2 Impact & Review Context Tools (4 tools)

| Tool | Parameters | Description | Output |
|------|-----------|-------------|--------|
| **`get_impact_radius_tool`** | `changed_files` (list[str]), `max_depth` (int=2), `repo_root` (str), `base` (str), `detail_level` (str="standard") | Blast radius analysis: shows all impacted functions, classes, files from changed code. Conservative (100% recall). | Changed/impacted nodes, edges, affected test files, detail_level="minimal" for compact output |
| **`get_review_context_tool`** (deprecated) | `changed_files`, `max_depth`, `include_source`, `max_lines_per_file`, `repo_root`, `base`, `detail_level` | Token-optimized subgraph + source snippets. Superseded by get_minimal_context for efficiency. | Focused review context, source code excerpts, review guidance |
| **`detect_changes_tool`** | `base`, `changed_files`, `include_source`, `max_depth`, `repo_root` | Risk-scored change impact analysis. Maps git diffs to affected functions, flows, communities, test gaps. | Risk scores, changed entities, impacted flows, test coverage gaps, priority ordering |
| **`get_affected_flows_tool`** | `changed_files`, `base`, `repo_root` | Find execution flows affected by changed files. | List of affected flow IDs with call chain details |

### 1.3 Query & Search Tools (5 tools)

| Tool | Parameters | Description | Output |
|------|-----------|-------------|--------|
| **`query_graph_tool`** | `pattern` (str), `target` (str), `repo_root` (str) | Predefined graph queries: `callers_of`, `callees_of`, `imports_of`, `importers_of`, `children_of`, `tests_for`, `inheritors_of`, `file_summary` | Query results with nodes and edges |
| **`semantic_search_nodes_tool`** | `query` (str), `kind` (str), `limit` (int=20), `repo_root` (str), `model` (str) | Keyword + vector search across code entities. Searches by name or semantic meaning. | Ranked search results (MRR 0.35) |
| **`list_graph_stats_tool`** | `repo_root` (str) | Aggregate graph statistics: node/edge counts, language distribution, test coverage %. | Graph health metrics |
| **`find_large_functions_tool`** | `min_lines` (int=50), `kind` (str), `file_path_pattern` (str), `limit` (int=50), `repo_root` (str) | Find oversized functions/classes exceeding line-count threshold. | List of large entities with line counts |
| **`embed_graph_tool`** | `repo_root` (str), `model` (str) | Compute vector embeddings for semantic search. Requires `pip install code-review-graph[embeddings]`. | Embedding stats, dimension, provider info |

### 1.4 Flow Analysis Tools (3 tools)

| Tool | Parameters | Description | Output |
|------|-----------|-------------|--------|
| **`list_flows_tool`** | `sort_by` (str="criticality"), `limit` (int=50), `kind` (str), `repo_root` (str) | List execution flows sorted by criticality, depth, node count, or name. Entry points: HTTP handlers, CLI commands, tests, main(). | Flows with criticality scores, entry points, call depths |
| **`get_flow_tool`** | `flow_id` (int), `flow_name` (str), `include_source` (bool), `repo_root` (str) | Get details of a single execution flow with full call chain. | Call chain, line numbers, source snippets (optional) |
| **`get_affected_flows_tool`** | (see Impact & Review above) | Find flows affected by changed files | Affected flow IDs with impact analysis |

### 1.5 Community & Architecture Tools (3 tools)

| Tool | Parameters | Description | Output |
|------|-----------|-------------|--------|
| **`list_communities_tool`** | `sort_by` (str="size"), `min_size` (int=0), `repo_root` (str) | List detected code communities. Communities are clusters of related entities (Leiden algorithm or file-based grouping). | Communities sorted by size/cohesion/name, member counts |
| **`get_community_tool`** | `community_name` (str), `community_id` (int), `include_members` (bool), `repo_root` (str) | Get details of a single community. | Community metadata, members, cohesion score, imports/exports |
| **`get_architecture_overview_tool`** | `repo_root` (str) | Auto-generated architecture map from community structure. Shows module summaries and cross-community coupling warnings. | Architecture with communities, dependencies, risk areas |

### 1.6 Refactoring Tools (2 tools)

| Tool | Parameters | Description | Output |
|------|-----------|-------------|--------|
| **`refactor_tool`** | `mode` ("rename"\|"dead_code"\|"suggest"), `old_name`, `new_name`, `kind`, `file_pattern`, `repo_root` | Unified refactoring: (1) rename preview with edit list, (2) dead code detection, (3) community-driven suggestions. | Preview edits, file-by-file changes, confidence scores |
| **`apply_refactor_tool`** | `refactor_id` (str), `repo_root` (str) | Apply a previously previewed refactoring. | Applied changes summary, affected files |

### 1.7 Wiki Tools (2 tools)

| Tool | Parameters | Description | Output |
|------|-----------|-------------|--------|
| **`generate_wiki_tool`** | `repo_root` (str), `force` (bool) | Generate markdown wiki from community structure. Each community becomes a wiki page with optional LLM summaries (requires ollama). | Wiki pages in `.code-review-graph/wiki/`, status |
| **`get_wiki_page_tool`** | `community_name` (str), `repo_root` (str) | Retrieve a specific wiki page. | Markdown content for a community |

### 1.8 Multi-Repository Tools (2 tools)

| Tool | Parameters | Description | Output |
|------|-----------|-------------|--------|
| **`list_repos_tool`** | (none) | List all registered repositories in the multi-repo registry. | Registered repos with aliases, paths, indexed entity counts |
| **`cross_repo_search_tool`** | `query` (str), `kind` (str), `limit` (int=20) | Search across all registered repositories. | Results from all repos with repo context |

### 1.9 Documentation Tools (1 tool)

| Tool | Parameters | Description | Output |
|------|-----------|-------------|--------|
| **`get_docs_section_tool`** | `section_name` (str) | Retrieve token-optimized documentation sections: `usage`, `review-delta`, `review-pr`, `commands`, `legal`, `watch`, `embeddings`, `languages`, `troubleshooting` | Section content |

---

## 📋 2. MCP PROMPTS (5 WORKFLOW TEMPLATES)

Pre-built prompt workflows with tool routing and context optimization:

| Prompt | Parameters | Purpose | Tools Called |
|--------|-----------|---------|--------------|
| **`review_changes`** | `base` (str="HEAD~1") | Pre-commit review workflow | `detect_changes`, `get_affected_flows`, test gap detection |
| **`architecture_map`** | (none) | Architecture documentation | `get_architecture_overview`, `list_communities`, Mermaid diagrams |
| **`debug_issue`** | `description` (str) | Guided debugging workflow | `semantic_search_nodes`, `get_flow`, recent changes |
| **`onboard_developer`** | (none) | New developer orientation | `list_graph_stats`, `get_architecture_overview`, critical flows |
| **`pre_merge_check`** | `base` (str="HEAD~1") | PR readiness check | `detect_changes`, risk scoring, test gaps, dead code |

---

## 🔧 3. CLI COMMANDS (17 TOTAL)

### 3.1 Setup & Configuration

```bash
code-review-graph install [--platform codex|cursor|claude-code|windsurf|zed|continue|opencode]
code-review-graph install [--dry-run]  # Preview without writing
```

**Detects**: Codex, Claude Code, Cursor, Windsurf, Zed, Continue, OpenCode, Antigravity
**Writes**: `.mcp.json` or platform-specific config

### 3.2 Build & Update

```bash
code-review-graph build                      # Full parse (all files)
code-review-graph update [--base origin/main]  # Incremental (changed files only)
code-review-graph postprocess [--flows|--communities|--fts]  # Run post-processing independently
```

### 3.3 Monitoring & Inspection

```bash
code-review-graph status              # Graph statistics (node/edge counts, languages)
code-review-graph watch               # Auto-update on file changes (debounced)
code-review-graph visualize           # Generate interactive D3.js HTML graph
```

### 3.4 Analysis

```bash
code-review-graph detect-changes [--base HEAD~3] [--brief]  # Risk-scored change analysis
```

### 3.5 Documentation & Knowledge

```bash
code-review-graph wiki                # Generate markdown wiki from communities
```

### 3.6 Multi-Repository Registry

```bash
code-review-graph register <path> [--alias name]  # Register a repository
code-review-graph unregister <path_or_alias>      # Remove from registry
code-review-graph repos               # List registered repositories
```

### 3.7 Evaluation & Server

```bash
code-review-graph eval [--all]        # Run evaluation benchmarks
code-review-graph serve               # Start MCP server (stdio)
```

---

## 📊 4. ANALYSIS CAPABILITIES

### 4.1 Blast-Radius / Impact Analysis
- **What**: Trace every caller, dependent, and test affected by a code change
- **How**: SQLite recursive CTE or NetworkX BFS (configurable via `CRG_BFS_ENGINE`)
- **Output**: 
  - Changed nodes (files, functions, classes)
  - Impacted nodes (2-hop default, configurable via `max_depth`)
  - Affected edges with types (CALLS, IMPORTS_FROM, etc.)
  - Test coverage analysis
- **Accuracy**: 100% recall, 0.54 average F1 (conservative: over-predicts to avoid missing)
- **Performance**: SQLite-native for large graphs (~2s on 2,900-file project)

### 4.2 Flow Detection & Tracing
- **Entry Points Detected**: 
  - HTTP handlers (framework-specific: `@app.route`, `@router.get`, etc.)
  - CLI commands (`click.command()`, `argparse`, etc.)
  - Test functions (`test_*`, `*_test`, etc.)
  - `main()` functions
- **Criticality Scoring**: Based on call depth, entry point frequency, and security keywords
- **Performance**: 33% recall (works well for Python frameworks, less for JS/Go)
- **Output**: Call chains with line numbers, criticality scores, affected flows by changes

### 4.3 Community Detection
- **Algorithm**: Leiden (igraph, `pip install code-review-graph[communities]`) or file-based grouping
- **Naming**: Generated from dominant classes, file prefixes, or keywords
- **Cohesion Scoring**: Internal edge density vs. external edges
- **Use**: Architecture overview, wiki generation, refactoring suggestions

### 4.4 Change Impact Analysis (`detect_changes_tool`)
- **Maps**: Git diffs → affected functions → impacted flows → test gaps
- **Risk Scoring**: Low/Medium/High based on impacted node count
- **Test Gap Detection**: Functions without test coverage in changed code
- **Priority Ordering**: Risk-ranked review items
- **Output**: Structured JSON with guidance for code review

### 4.5 Semantic Search
- **Providers**: 
  - Local: sentence-transformers (all-MiniLM-L6-v2 default)
  - Cloud: Google Gemini embeddings (`CRG_ACCEPT_CLOUD_EMBEDDINGS=1`)
  - Cloud: MiniMax embeddings (API key required)
- **Storage**: SQLite with FTS5 + vector similarity
- **Hybrid Search**: Keyword + vector similarity ranking
- **Performance**: MRR 0.35 (top-4 ranking for most queries)

### 4.6 Test Coverage Analysis
- **Detection**: Functions marked as `is_test=true` (test naming patterns)
- **Mapping**: TESTED_BY edges link functions to test functions
- **Gap Detection**: Functions without test coverage in changed code
- **Output**: Test coverage % per community/file

### 4.7 Dead Code Detection
- **Method**: Find functions/classes with no incoming edges (callers, inheritance, imports)
- **Filter**: By kind (Function/Class) or file pattern
- **Confidence**: No incoming edges = truly unused (high confidence)
- **Output**: Dead code candidates with analysis

### 4.8 Dependency Chain Analysis
- **Multi-hop Dependents**: N-hop dependent discovery (default 2 hops, configurable via `CRG_DEPENDENT_HOPS`)
- **500-file cap**: Performance limit to prevent runaway queries
- **Output**: Qualified dependency chains with types

### 4.9 Architecture Coupling Analysis
- **Detects**: Cross-community imports/calls (potential architectural violations)
- **Scoring**: Coupling warnings with severity
- **Output**: Module dependency graph with risk indicators

---

## 💾 5. DATA STORAGE & SCHEMA

### 5.1 Database Schema (SQLite v6)

**Tables**:
- `nodes` — File, Class, Function, Type, Test entities
- `edges` — CALLS, IMPORTS_FROM, INHERITS, IMPLEMENTS, CONTAINS, TESTED_BY, DEPENDS_ON, REFERENCES
- `metadata` — Graph version, build timestamps
- `flows` — Execution flow definitions and criticality scores
- `communities` — Detected code communities
- `community_summaries` — Pre-computed summaries (v2.2.1+)
- `flow_snapshots` — Snapshot data for incremental updates (v2.2.1+)
- `risk_index` — Pre-computed risk metrics (v2.2.1+)
- `embeddings` — Vector embeddings for semantic search (optional)
- `fts_nodes` — Full-text search index (FTS5 virtual table)

**Indexes**: file_path, kind, qualified_name, source/target edges, language

### 5.2 Node Types

| Type | Description | Examples |
|------|-------------|----------|
| **File** | Source code file | `.py`, `.ts`, `.go`, etc. |
| **Class** | Class, struct, interface, enum | Python `class`, Go `type T struct`, Java `class` |
| **Function** | Function or method | Python `def`, TypeScript `function`, Go `func` |
| **Type** | Type alias, interface | TypeScript `type`, Go `interface` |
| **Test** | Test function | `test_*.py`, `*.test.ts`, `*_test.go` |

### 5.3 Edge Types

| Type | Description | Example |
|------|-------------|---------|
| **CALLS** | Function calls function | `login()` calls `verify_token()` |
| **IMPORTS_FROM** | File imports module | `import authenticate` |
| **INHERITS** | Class inherits from class | `class Admin extends User` |
| **IMPLEMENTS** | Class implements interface | `class Service implements IService` |
| **CONTAINS** | Structural containment | File contains Class, Class contains Method |
| **TESTED_BY** | Function tested by test | `login()` tested_by `test_login()` |
| **DEPENDS_ON** | General dependency | Module depends on module |
| **REFERENCES** | Symbol is referenced | Potential import/call target |

### 5.4 Qualified Name Format

```
/absolute/path/to/file.py                    # File node
/absolute/path/to/file.py::function_name     # Top-level function
/absolute/path/to/file.py::ClassName         # Class
/absolute/path/to/file.py::ClassName.method  # Method
/absolute/path/to/file.py::OuterClass.Inner.method  # Nested
```

---

## 🌍 6. LANGUAGE SUPPORT (20+ LANGUAGES)

### 6.1 Full Support (Tree-sitter parsing)

| Category | Languages | Node Types |
|----------|-----------|-----------|
| **Web** | Python, TypeScript/TSX, JavaScript, Vue, Astro | Classes, functions, imports, calls, inheritance |
| **Backend** | Go, Rust, Java, Scala, C#, Ruby, Kotlin, Swift, PHP | Functions, structs/classes, modules, inheritance |
| **Systems** | C, C++, Rust, Solidity, Perl | Functions, structs, includes, forward decls |
| **Data** | R, Lua, Bash, Elixir, Objective-C | Functions, definitions, imports |
| **Special** | Jupyter/Databricks (`.ipynb`), Perl XS (`.xs`) | Python/R/SQL cells, C functions |

### 6.2 Language Detection

Automatic via file extension:
```
.py, .js, .jsx, .ts, .tsx, .go, .rs, .java, .cs, .rb, .cpp, .c, .h, .kt, .swift, .php, .scala, .sol, .vue, .dart, .r, .pl, .pm, .t, .xs, .lua, .luau, .ex, .exs, .ipynb, .m, .sh, .bash, .zsh
```

---

## ⚙️ 7. CONFIGURATION OPTIONS

### 7.1 Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `CRG_MAX_IMPACT_NODES` | 500 | Max nodes in blast-radius analysis |
| `CRG_MAX_IMPACT_DEPTH` | 2 | Max hops for impact radius (2 = callers + their callers) |
| `CRG_MAX_BFS_DEPTH` | 15 | Max traversal depth (flows, tracing) |
| `CRG_MAX_SEARCH_RESULTS` | 20 | Max search results returned |
| `CRG_BFS_ENGINE` | "sql" | "sql" (SQLite CTE) or "networkx" (legacy) |
| `CRG_PARSE_WORKERS` | `min(cpu_count, 8)` | Parallel parsing threads |
| `CRG_REPO_ROOT` | (auto-detect) | Override repository root |
| `CRG_DATA_DIR` | `.code-review-graph` | Override graph storage directory |
| `CRG_GIT_TIMEOUT` | 30 | Git command timeout (seconds) |
| `CRG_RECURSE_SUBMODULES` | "" | Include git submodules (set to "1") |
| `CRG_DEPENDENT_HOPS` | 2 | Multi-hop dependent discovery |
| `CRG_EMBEDDING_MODEL` | "all-MiniLM-L6-v2" | Sentence-transformers model for local embeddings |
| `CRG_ACCEPT_CLOUD_EMBEDDINGS` | "" | Set to "1" to use Google/MiniMax embeddings |
| `NO_COLOR` | (unset) | Disable ANSI colors in CLI output |

### 7.2 Ignore Patterns (`.code-review-graphignore`)

```
# Example .code-review-graphignore
generated/**
vendor/**
*.generated.ts
__pycache__/**
node_modules/**
dist/**
.next/**
target/**
build/**
```

**Default exclusions**: `.git/**`, `node_modules/**`, `.venv/**`, `*.pyc`, `*.min.js`, `*.lock`, `*.db`, etc.

**Note**: In git repos, only tracked files are indexed (`git ls-files`), so `.gitignore` is respected automatically.

### 7.3 CLI Flags

| Flag | Command | Purpose |
|------|---------|---------|
| `--dry-run` | `install` | Preview changes without writing |
| `--platform <name>` | `install` | Target specific platform instead of auto-detect |
| `--base <ref>` | `update`, `detect-changes` | Custom git base ref (default: HEAD~1) |
| `--brief` | `detect-changes` | Compact output |
| `--skip-postprocess` | `build`, `update` | Skip flows/communities/FTS to speed up build |
| `--all` | `eval` | Run all evaluation benchmarks |
| `--force` | `generate_wiki_tool` | Regenerate all wiki pages |
| `--alias <name>` | `register` | Alias for registered repository |

### 7.4 MCP Tool Configuration

**`postprocess` parameter** (all build/update tools):
- `"full"` — Run flows, communities, FTS indexing (default)
- `"minimal"` — Signatures + FTS only (40-60% faster)
- `"none"` — Skip all post-processing (for large repos, run later with `postprocess` command)

**`detail_level` parameter** (8 tools for token efficiency):
- `"standard"` — Full output (default)
- `"minimal"` — Compact summary (~40-60% fewer tokens)

---

## 📈 8. OUTPUT FORMATS & VISUALIZATIONS

### 8.1 Interactive D3.js Graph Visualization
- **Generated by**: `code-review-graph visualize`
- **Location**: `.code-review-graph/graph.html`
- **Features**:
  - Force-directed graph layout
  - Node types color-coded (File, Class, Function, Type, Test)
  - Edge type toggles (CALLS, IMPORTS_FROM, INHERITS, etc.)
  - Search bar with live filtering
  - Click-to-expand/collapse
  - Starts collapsed (File nodes only) for 5k+ node graphs
  - Drill-down support for large repositories
  - ARIA labels for accessibility
  - XSS hardening (JSON escaping)

### 8.2 Markdown Wiki
- **Generated by**: `code-review-graph wiki`
- **Location**: `.code-review-graph/wiki/`
- **Structure**: One markdown page per community
- **Content**: 
  - Community description
  - Member list (functions, classes, files)
  - Dependencies (imports, calls)
  - Test coverage % (optional: LLM summaries via ollama)

### 8.3 JSON Output (MCP Tools)
- **Structured responses** with:
  - Status indicators
  - Metadata (timestamps, hashes, counts)
  - Nodes/edges with full details
  - Subgraph snippets
  - Risk scores and guidance
  - Token counts (for optimization)

### 8.4 CLI Text Output
- **Colored output** (ANSI) with:
  - Progress bars for long operations
  - Tabular statistics
  - Change impact summaries
  - Next-step recommendations

---

## 🎯 9. TOKEN EFFICIENCY FEATURES

### 9.1 Baseline Metrics
- **Average token reduction**: 8.2x vs. naive full-codebase review
- **Range**: 0.7x to 27.3x depending on repo and change type
- **Sweet spot**: Multi-file changes in large codebases (27.3x on Next.js monorepo)

### 9.2 Optimization Techniques
1. **Blast-radius pruning**: Only review impacted code (~15 files vs. 27,732 in Next.js)
2. **Structural summary**: Qualified names + edge types instead of full source
3. **Lazy post-processing**: Skip expensive flows/communities when not needed
4. **Token-efficient output** (`detail_level="minimal"`): 40-60% reduction on 8 tools
5. **Get-minimal-context**: ~100-token entry point with next-tool routing

### 9.3 Benchmarks (Real Repositories)

| Repository | Commits | Avg Naive | Avg Graph | Reduction | F1 Score |
|------------|---------|-----------|-----------|-----------|----------|
| express | 2 | 693 | 983 | 0.7x | 0.67 |
| fastapi | 2 | 4,944 | 614 | **8.1x** | 0.58 |
| flask | 2 | 44,751 | 4,252 | **9.1x** | 0.48 |
| gin | 3 | 21,972 | 1,153 | **16.4x** | 0.43 |
| httpx | 2 | 12,044 | 1,728 | **6.9x** | 0.76 |
| nextjs | 2 | 9,882 | 1,249 | **8.0x** | 0.33 |

---

## 🚀 10. PERFORMANCE CHARACTERISTICS

### 10.1 Build Performance

| Repository | Files | Nodes | Edges | Flow Detection | Search Latency |
|------------|------:|------:|------:|---------------:|---------------:|
| express | 141 | 1,910 | 17,553 | 106ms | 0.7ms |
| fastapi | 1,122 | 6,285 | 27,117 | 128ms | 1.5ms |
| flask | 83 | 1,446 | 7,974 | 95ms | 0.7ms |
| gin | 99 | 1,286 | 16,762 | 111ms | 0.5ms |
| httpx | 60 | 1,253 | 7,896 | 96ms | 0.4ms |

### 10.2 Incremental Update Speed
- **Performance**: < 2 seconds for typical commits
- **Large project**: 2,900 files re-indexed in under 2 seconds
- **Method**: Diff-only parsing + SHA-256 change detection

### 10.3 Parallel Parsing
- **Speed boost**: 3-5x faster on large repos
- **Default workers**: `min(cpu_count, 8)`
- **Override**: `CRG_PARSE_WORKERS` env var
- **Technology**: Python `ProcessPoolExecutor`

### 10.4 Query Latency
- **Semantic search**: < 1.5ms for most queries
- **Impact radius**: < 100ms on 6,285-node graph
- **Flow detection**: ~100-130ms per project

---

## 🔐 11. KNOWN LIMITATIONS

| Limitation | Impact | Workaround |
|-----------|--------|-----------|
| **Small single-file changes** | Graph context can exceed naive file reads (see express: 0.7x) | Overhead pays off on multi-file changes |
| **Search quality (MRR 0.35)** | Keyword search finds top result in top-4 for most queries | Use semantic search or broader queries |
| **Flow detection (33% recall)** | Only reliably detects Python framework patterns (fastapi, httpx) | JS/Go flow detection needs improvement; use entry points manually |
| **Precision vs. recall trade-off** | Impact analysis flags files that *might* be affected (false positives) | Conservative choice: better to flag than miss a dependency |
| **Limited dead code detection** | Only finds functions with zero incoming edges | Doesn't catch unused private methods in large classes |

---

## 📦 12. OPTIONAL DEPENDENCIES

Install feature groups as needed:

```bash
pip install code-review-graph[embeddings]        # sentence-transformers (local embeddings)
pip install code-review-graph[google-embeddings] # Google Gemini embeddings
pip install code-review-graph[communities]       # igraph (Leiden community detection)
pip install code-review-graph[eval]              # matplotlib (evaluation benchmarks)
pip install code-review-graph[wiki]              # ollama (LLM wiki summaries)
pip install code-review-graph[all]               # All optional dependencies
pip install code-review-graph[dev]               # Development: pytest, mypy, etc.
```

---

## 🎯 13. SUPPORTED AI CODING PLATFORMS

Auto-detected and configured by `code-review-graph install`:

1. **Codex** — Config: `~/.codex/config.toml`
2. **Claude Code** — Config: `.mcp.json`
3. **Cursor** — Config: `.cursor/mcp.json`
4. **Windsurf** — Config: `.windsurf/mcp.json`
5. **Zed** — Config: `.zed/settings.json`
6. **Continue** — Config: `.continue/config.json`
7. **OpenCode** — Config: `.opencode/config.json`
8. **Antigravity** — (if detected)

---

## 📝 14. SLASH COMMANDS (Claude Code)

Use these in Claude Code / other supported platforms:

| Command | Purpose |
|---------|---------|
| `/code-review-graph:build-graph` | Build or rebuild the knowledge graph |
| `/code-review-graph:review-delta` | Review changes since last commit (with blast radius) |
| `/code-review-graph:review-pr` | Review a PR/branch with full impact analysis |

---

## 🔄 15. INCREMENTAL UPDATES & HOOKS

### 15.1 Automatic Triggers
- **Git hooks**: `.git/hooks/post-commit`, `.git/hooks/post-merge` (if enabled)
- **File watch**: `code-review-graph watch` (debounced, 500ms default)
- **Manual**: `code-review-graph update [--base ref]`

### 15.2 What Updates
- Only changed files are re-parsed (SHA-256 change detection)
- Dependent files are found via SQLite queries (not re-parsed, just re-indexed)
- Flows and communities are incrementally re-traced (not fully recomputed)

### 15.3 Stored Updates
- Metadata: `schema_version`, `last_build_timestamp`, `language_versions`
- Summary tables: `community_summaries`, `flow_snapshots`, `risk_index` (v2.2.1+)

---

## 🛠️ 16. DEVELOPMENT & EXTENSION

### 16.1 Adding a New Language

Edit `code_review_graph/parser.py`:

1. Add extension to `EXTENSION_TO_LANGUAGE`:
   ```python
   ".newlang": "newlang"
   ```

2. Define node type mappings in `_CLASS_TYPES`, `_FUNCTION_TYPES`, `_IMPORT_TYPES`, `_CALL_TYPES`

3. Add test fixture in `tests/fixtures/`

4. Open a PR

### 16.2 Custom MCP Tools

Extend `code_review_graph/tools/` with new tool modules:
- Follow `@mcp.tool()` decorator pattern
- Re-export in `tools/__init__.py`
- Use `_get_store()` and `_validate_repo_root()` helpers

### 16.3 Hooks & Plugins

Post-tool-use hooks in Claude Code for automatic background updates:
- **Write** hook: Re-parse modified files
- **Edit** hook: Update graph incrementally
- **Bash** hook: Auto-commit if needed

---

## 🚀 17. QUICK START CHECKLIST

```bash
# 1. Install
pip install code-review-graph
code-review-graph install --platform claude-code  # or auto-detect

# 2. Build
code-review-graph build                           # ~10s for 500 files

# 3. Optional: Watch mode
code-review-graph watch &                         # Auto-update on save

# 4. Optional: Visualize
code-review-graph visualize                       # Opens graph.html

# 5. Use in Claude Code
# Ask: "Build the code review graph for this project"
# Then: "Review my recent changes"
# Or: "/code-review-graph:review-delta"
```

---

## 📊 18. DATABASE SCHEMA VERSIONS

| Version | Changes |
|---------|---------|
| v1 | Initial nodes/edges tables |
| v2 | Added metadata table |
| v3 | Added flows, communities, FTS index |
| v4 | Added community_summaries |
| v5 | Added flow_snapshots, risk_index |
| v6 | Current (v2.2.1) |

Auto-migrated on startup.

---

## 🎓 19. HIDDEN FEATURES (Not in README)

Features implemented but not prominently documented:

1. **`get_minimal_context_tool`** — Ultra-compact entry point (~100 tokens) — v2.2.1
2. **`run_postprocess_tool`** — Independent post-processing (flows, communities, FTS) — v2.2.1
3. **Parallel parsing** — ProcessPoolExecutor for 3-5x faster builds — v2.2.1
4. **Lazy post-processing** — `postprocess="minimal"|"none"` for faster builds — v2.2.1
5. **SQLite-native BFS** — Recursive CTE replaces NetworkX (faster on large graphs) — v2.2.1
6. **Token-efficient output** — `detail_level="minimal"` on 8 tools — v2.2.1
7. **Multi-hop dependents** — N-hop discovery (default 2) with 500-file cap — v2.2.1
8. **Pre-computed summaries** — community_summaries, flow_snapshots, risk_index tables — v2.2.1
9. **Configurable limits** — 14 environment variables for fine-tuning
10. **TypeScript path resolution** — tsconfig.json paths/baseUrl alias support
11. **Jupyter notebook parsing** — Multi-language cell support (.ipynb)
12. **Perl XS parsing** — C parsing of .xs files
13. **Git submodule support** — `CRG_RECURSE_SUBMODULES=1`
14. **Security keyword detection** — 32 hardcoded keywords for risk scoring
15. **Ollama integration** — LLM summaries in wiki generation

---

## 📞 20. KEY STATS

- **615 tests** across 22 test files
- **18,205 lines** of Python code
- **Version**: 2.2.1 (April 2026)
- **Python requirement**: 3.10+
- **License**: MIT
- **Primary author**: tirth8205
- **MCP standard**: FastMCP 3.0+ compatible

---

**End of Feature Inventory**

Generated April 2026 from analysis of code-review-graph v2.2.1
