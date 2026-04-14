# Code Context Graph - Code Quality & Architecture Refactoring Plan

## Background & Motivation
The `codebase_investigator` identified several critical areas for improvement in the current codebase:
1. **Performance Bottlenecks:** `N+1` queries exist in the GORM store (`UpsertNodes`) and BFS logic (`ImpactRadius`), which will degrade performance on larger code graphs.
2. **Architectural Coupling:** `internal/cli/build.go` handles file walking, parsing orchestration, and database syncing, violating the Single Responsibility Principle (SRP).
3. **Parser Maintainability (God Object):** `internal/parse/treesitter/walker.go` contains hard-coded language-specific logic across its methods, making it difficult to maintain or add new languages.
4. **Robustness:** Database operations during the file build process lack proper transaction boundaries, risking orphaned data on errors.

## Scope & Impact
This refactoring effort aims to systematically eliminate these technical debts. It touches core components (`gormstore`, `impact`, `build`, `treesitter`) without altering the expected external behavior of the CLI.

## Proposed Solution: Phased Refactoring Approach

We will tackle the refactoring in 3 distinct phases to ensure safe integration and review:

### Phase 1: Database Performance & Robustness (Data Layer)
- **Batch Upserts (via GORM):** Replace individual node `SELECT` and `UPDATE/CREATE` calls in `gormstore.UpsertNodes` with GORM's native batching and conflict resolution:
  ```go
  db.Clauses(clause.OnConflict{
      Columns:   []clause.Column{{Name: "qualified_name"}},
      UpdateAll: true,
  }).CreateInBatches(nodes, 100)
  ```
- **Transaction Blocks:** Enforce `db.Transaction(func(tx *gorm.DB) error { ... })` across all file-level mutations to ensure atomicity. If a file fails to index completely, it will automatically roll back.
- **BFS Optimization:** Refactor `ImpactRadius` in `impact.go` to query outgoing and incoming edges for the entire `frontier` in a single `IN (?)` query per depth iteration.

### Phase 2: CLI Decoupling & Service Layer (Orchestration)
- **Create `GraphService`:** Extract the orchestration logic from `cli/build.go` into a new `internal/service/indexer.go`. This service will handle file walking, parsing delegation, and DB upserts within transaction blocks.
- **Isolate CLI:** Simplify `build.go` to only handle argument parsing, flag binding, and invoking the new `GraphService`.

### Phase 3: Walker Refactoring (Parser Layer via Tree-sitter Queries)
- **Migrate to Query-based Pattern Matching:** Instead of maintaining a God Object (`Walker`) with hard-coded AST traversal and language-specific `if/else` logic, we will utilize **Tree-sitter's Query System (S-expressions)**.
- **Language Definitions (.scm):** Create `queries/<language>/tags.scm` files defining language-specific patterns for functions, classes, calls, and imports (e.g., `(function_declaration name: (identifier) @name)`).
- **Generic Query Executor:** Refactor `walker.go` to compile these queries via `sitter.NewQuery` and execute them using `sitter.NewQueryCursor`. This makes the Go code 100% language-agnostic and data-driven.

## Alternatives Considered
- **Strategy Pattern for Parser:** Creating `LanguageHandler` interfaces for 15+ languages in Go. Rejected because Context7 documentation shows that Tree-sitter's native Query API is the industry standard for cross-language AST pattern matching, requiring far less Go boilerplate.
- **Big Bang Refactoring:** Doing all of this in a single pass. Rejected because it introduces too much risk and makes bug isolation extremely difficult.

## Verification & Testing
- Ensure the existing test suite (especially `gormstore_test.go`, `impact_test.go`, `walker_test.go`, and `build_test.go`) continues to pass.
- Write new benchmark tests for `UpsertNodes` and `ImpactRadius` to empirically prove the elimination of N+1 overhead.

## Migration & Rollback
- No database schema migrations are necessary for this refactor. The graph and vectors will be recreated using the new optimized logic.
- Rollback can be performed via standard `git revert` since this does not fundamentally alter the storage schema or external API signatures.
