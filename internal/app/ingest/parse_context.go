package ingest

import "context"

// importPackagesContextKey isolates import-path hints from unrelated context values.
// @intent provide a collision-free key for parser-neutral import package context.
type importPackagesContextKey struct{}

// filePackagesContextKey isolates file-package hints from unrelated context values.
// @intent provide a collision-free key for parser-neutral file package context.
type filePackagesContextKey struct{}

// WithImportPackages stores repository import-path normalization hints for parser adapters.
// @intent thread parser-neutral package names through build and update calls without adapter-specific APIs.
func WithImportPackages(ctx context.Context, packages map[string]string) context.Context {
	return withStringMap(ctx, importPackagesContextKey{}, packages)
}

// ImportPackagesFromContext loads repository import-path normalization hints.
// @intent let parser adapters consume application-owned package context without reversing dependencies.
func ImportPackagesFromContext(ctx context.Context) map[string]string {
	return stringMapFromContext(ctx, importPackagesContextKey{})
}

// WithFilePackages stores canonical import paths keyed by repository-relative file path.
// @intent provide deterministic package prefixes for languages without package declarations.
func WithFilePackages(ctx context.Context, packages map[string]string) context.Context {
	return withStringMap(ctx, filePackagesContextKey{}, packages)
}

// FilePackagesFromContext loads canonical file package hints for parser adapters.
// @intent let parser adapters seed qualified names from application-owned file context.
func FilePackagesFromContext(ctx context.Context) map[string]string {
	return stringMapFromContext(ctx, filePackagesContextKey{})
}

// withStringMap stores a defensive copy after dropping unusable entries.
// @intent prevent callers from mutating parser context maps after injection.
func withStringMap(ctx context.Context, key any, values map[string]string) context.Context {
	if len(values) == 0 {
		return ctx
	}
	cloned := make(map[string]string, len(values))
	for name, value := range values {
		if name == "" || value == "" {
			continue
		}
		cloned[name] = value
	}
	if len(cloned) == 0 {
		return ctx
	}
	return context.WithValue(ctx, key, cloned)
}

// stringMapFromContext reads a typed string map without panicking on nil or mismatched values.
// @intent centralize safe retrieval for ingest-owned parser context hints.
func stringMapFromContext(ctx context.Context, key any) map[string]string {
	if ctx == nil {
		return nil
	}
	values, _ := ctx.Value(key).(map[string]string)
	return values
}
