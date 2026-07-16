// @index GORM persistence for unresolved-edge reverse lookup and readiness state.
package graphgorm

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/tae2089/trace"

	requestctx "github.com/tae2089/code-context-graph/internal/ctx"
	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

const unresolvedQueryChunkSize = 400

// UpsertUnresolvedEdges stores reverse-index rows without exposing them as traversable graph edges.
// @intent retain unresolved syntax candidates until a future symbol addition can resolve them.
// @sideEffect inserts unresolved_edge_candidates rows.
func (s *Store) UpsertUnresolvedEdges(ctx context.Context, candidates []graph.UnresolvedEdgeCandidate) error {
	if len(candidates) == 0 {
		return nil
	}
	ns := requestctx.FromContext(ctx)
	for i := range candidates {
		candidates[i].Namespace = ns
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "namespace"}, {Name: "fingerprint"}},
		DoNothing: true,
	}).CreateInBatches(candidates, 500).Error; err != nil {
		return trace.Wrap(err, "upsert unresolved edges")
	}
	return nil
}

// FindUnresolvedEdgesByLookupKeys finds deduplicated syntax edges matching newly added symbol keys.
// @intent use the reverse index to identify affected unchanged source files.
func (s *Store) FindUnresolvedEdgesByLookupKeys(ctx context.Context, keys []string) ([]graph.Edge, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	ns := requestctx.FromContext(ctx)
	seen := make(map[string]struct{})
	var out []graph.Edge
	for start := 0; start < len(keys); start += unresolvedQueryChunkSize {
		end := min(start+unresolvedQueryChunkSize, len(keys))
		var rows []graph.UnresolvedEdgeCandidate
		if err := s.db.WithContext(ctx).
			Where("namespace = ? AND lookup_key IN ?", ns, keys[start:end]).
			Order("file_path ASC").Order("line ASC").Order("fingerprint ASC").
			Find(&rows).Error; err != nil {
			return nil, trace.Wrap(err, "find unresolved edges by lookup keys")
		}
		for _, row := range rows {
			if _, ok := seen[row.Fingerprint]; ok {
				continue
			}
			seen[row.Fingerprint] = struct{}{}
			out = append(out, row.Edge())
		}
	}
	return out, nil
}

// FindUnresolvedEdgesByFiles loads every candidate edge owned by the selected affected files.
// @intent replay import warmup and related edges together after reverse-index selection narrows source files.
func (s *Store) FindUnresolvedEdgesByFiles(ctx context.Context, filePaths []string) ([]graph.Edge, error) {
	if len(filePaths) == 0 {
		return nil, nil
	}
	ns := requestctx.FromContext(ctx)
	seen := make(map[string]struct{})
	var out []graph.Edge
	for start := 0; start < len(filePaths); start += unresolvedQueryChunkSize {
		end := min(start+unresolvedQueryChunkSize, len(filePaths))
		var rows []graph.UnresolvedEdgeCandidate
		if err := s.db.WithContext(ctx).
			Where("namespace = ? AND file_path IN ?", ns, filePaths[start:end]).
			Order("file_path ASC").Order("line ASC").Order("fingerprint ASC").
			Find(&rows).Error; err != nil {
			return nil, trace.Wrap(err, "find unresolved edges by files")
		}
		for _, row := range rows {
			if _, ok := seen[row.Fingerprint]; ok {
				continue
			}
			seen[row.Fingerprint] = struct{}{}
			out = append(out, row.Edge())
		}
	}
	return out, nil
}

// DeleteUnresolvedEdgesByFingerprints removes every lookup-key row for resolved syntax edges.
// @intent keep the reverse index limited to relationships that still lack endpoints.
func (s *Store) DeleteUnresolvedEdgesByFingerprints(ctx context.Context, fingerprints []string) error {
	if len(fingerprints) == 0 {
		return nil
	}
	ns := requestctx.FromContext(ctx)
	for start := 0; start < len(fingerprints); start += unresolvedQueryChunkSize {
		end := min(start+unresolvedQueryChunkSize, len(fingerprints))
		if err := s.db.WithContext(ctx).
			Where("namespace = ? AND fingerprint IN ?", ns, fingerprints[start:end]).
			Delete(&graph.UnresolvedEdgeCandidate{}).Error; err != nil {
			return trace.Wrap(err, "delete resolved unresolved edges")
		}
	}
	return nil
}

// UnresolvedIndexReady reports whether a compatible full build populated the namespace's reverse index.
// @intent gate semi-naive update on complete historical unresolved-edge coverage produced by the expected algorithm and parsers.
func (s *Store) UnresolvedIndexReady(ctx context.Context, version string) (bool, error) {
	if version == "" {
		return false, nil
	}
	var state graph.UnresolvedIndexState
	ns := requestctx.FromContext(ctx)
	result := s.db.WithContext(ctx).Where("namespace = ? AND version = ?", ns, version).First(&state)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if result.Error != nil {
		return false, trace.Wrap(result.Error, "load unresolved index state")
	}
	return true, nil
}

// MarkUnresolvedIndexReady marks the current namespace and producer version after a successful full candidate pass.
// @intent distinguish a compatible legitimately empty reverse index from stale or uninitialized state.
// @sideEffect inserts or updates unresolved_index_states.
func (s *Store) MarkUnresolvedIndexReady(ctx context.Context, version string) error {
	if version == "" {
		return trace.New("unresolved index version is empty")
	}
	state := graph.UnresolvedIndexState{Namespace: requestctx.FromContext(ctx), Version: version}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "namespace"}},
		DoUpdates: clause.AssignmentColumns([]string{"version", "updated_at"}),
	}).Create(&state).Error; err != nil {
		return trace.Wrap(err, "mark unresolved index ready")
	}
	return nil
}
