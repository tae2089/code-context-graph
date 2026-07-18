package reposync

import "context"

// CheckoutRequest identifies one admitted remote branch and its local namespace.
// @intent carry only trusted admission output into the checkout adapter.
type CheckoutRequest struct{ RepoFullName, CloneURL, Namespace, Branch string }

// Checkout synchronizes a remote branch into its isolated repository directory.
// @intent isolate checkout locking and Git implementation from sync ordering policy.
type Checkout interface {
	Sync(ctx context.Context, request CheckoutRequest) (repoDir string, err error)
}

// BuildScope captures the repository-local source selection policy for one webhook sync.
// @intent carry include and exclude configuration together so every webhook update uses one coherent build scope.
type BuildScope struct {
	IncludePaths    []string
	ExcludePatterns []string
}

// BuildScopeLoader loads repository-local graph source selection configuration.
// @intent separate repository config file parsing from repository sync orchestration.
type BuildScopeLoader interface {
	Load(repoDir string) (BuildScope, error)
}

// GraphRequest carries the exact webhook update contract to the ingest adapter.
// @intent preserve namespace, source scope, replace limits, and readability policy across the app boundary.
type GraphRequest struct {
	RepoDir, Namespace                string
	IncludePaths                      []string
	ExcludePatterns                   []string
	MaxFileBytes, MaxTotalParsedBytes int64
	FailOnUnreadable                  bool
}

// UpdateStats is the sync result needed for repository operation logging.
// @intent report only update counts needed by repository sync observability.
type UpdateStats struct{ Added, Modified, Skipped, Deleted int }

// GraphUpdater replaces one repository namespace from its synchronized checkout.
// @intent adapt repository sync to the ingest application without importing workflow types.
type GraphUpdater interface {
	Update(ctx context.Context, request GraphRequest) (UpdateStats, error)
}

// CacheInvalidator drops query results only after a successful graph update.
// @intent keep derived query cache invalidation after successful repository graph commit.
type CacheInvalidator interface{ Invalidate() }

// CacheInvalidatorFunc adapts a runtime closure to the application port.
// @intent adapt runtime cache invalidation closures to the app-owned port.
type CacheInvalidatorFunc func()

// Invalidate adapts a closure to the cache invalidation port.
// @intent invoke the configured cache invalidation only when one exists.
func (f CacheInvalidatorFunc) Invalidate() {
	if f != nil {
		f()
	}
}
