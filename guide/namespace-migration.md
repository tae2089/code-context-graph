# Namespace Migration Guide

This document explains the operational impact of the namespace policy change and how to migrate your existing data.

## Summary of Changes

The default namespace handling has been unified to improve consistency across CLI and MCP tools.

### Literal "default" Namespace
The default namespace is now literally `"default"`. Previously, an unset namespace often implied an empty string (`""`) in the database, leading to inconsistent behavior between tools that assumed empty vs. those that assumed "default".

### No More Implicit Whole-Database Reads
Implicit whole-database reads (scanning all namespaces when none is specified) have been removed from the following paths:
- `status`
- `docs`
- `lint`
- `eval`
- All MCP-related tool paths

You must now specify a namespace if you wish to operate on non-default data.

### CLI Behavior
The global `--namespace` flag now defaults to `"default"`. An empty or unset namespace no longer triggers legacy empty-string behavior.

## Migration and Backfill

### Automatic Backfill
Upon startup, the system now automatically backfills legacy rows where `namespace = ''` to `namespace = 'default'`. This ensures that existing data remains accessible under the new default policy.

### Collision Handling
If a backfill operation detects a collision (e.g., both `''` and `'default'` namespaces exist for the same entity), the migration will fail fast. The system will not silently merge these rows to prevent data corruption. If this occurs, you must manually resolve the duplication in your database before restarting.
