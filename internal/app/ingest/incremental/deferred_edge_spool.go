// @index Session-local parsed-edge spool for staged incremental reconciliation.
package incremental

import (
	"encoding/gob"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/domain/graph"
)

// deferredEdgeFile holds one file's parsed edges after its nodes are persisted.
// @intent retain only the edge-resolution input needed after source bytes are released.
type deferredEdgeFile struct {
	FilePath string
	Edges    []graph.Edge
}

// deferredEdgeRecord is one bounded disk record used between node and edge phases.
// @intent keep large staged updates independent of the total parsed edge count in memory.
type deferredEdgeRecord struct {
	Files []deferredEdgeFile
}

// deferredEdgeSpool owns temporary edge records for one staged reconciliation invocation.
// @intent preserve parsed cross-batch edges until every changed node has been applied.
type deferredEdgeSpool struct {
	dir     string
	records []string
}

// newDeferredEdgeSpool creates an invocation-local directory for parsed edge records.
// @intent isolate temporary staged-update data so cleanup cannot affect persistent graph state.
// @sideEffect creates a temporary directory under the operating system temp root.
func newDeferredEdgeSpool() (*deferredEdgeSpool, error) {
	dir, err := os.MkdirTemp("", "ccg-deferred-edge-spool-*")
	if err != nil {
		return nil, trace.Wrap(err, "create deferred edge spool")
	}
	return &deferredEdgeSpool{dir: dir}, nil
}

// writeRecord persists a bounded parsed-edge batch for later resolution.
// @intent defer cross-file edge resolution until all batch-local node replacements are complete.
// @sideEffect creates a gob record inside the invocation-local spool directory.
func (s *deferredEdgeSpool) writeRecord(record deferredEdgeRecord) error {
	if len(record.Files) == 0 {
		return nil
	}
	path := filepath.Join(s.dir, fmt.Sprintf("%06d.gob", len(s.records)))
	file, err := os.Create(path)
	if err != nil {
		return trace.Wrap(err, "create deferred edge spool record")
	}
	encodeErr := gob.NewEncoder(file).Encode(record)
	closeErr := file.Close()
	if encodeErr != nil {
		return trace.Wrap(encodeErr, "encode deferred edge spool record")
	}
	if closeErr != nil {
		return trace.Wrap(closeErr, "close deferred edge spool record")
	}
	s.records = append(s.records, path)
	return nil
}

// readRecord streams one parsed-edge batch back into the resolution phase.
// @intent let edge resolution remain bounded by the original source batch size.
func (s *deferredEdgeSpool) readRecord(path string) (deferredEdgeRecord, error) {
	var record deferredEdgeRecord
	file, err := os.Open(path)
	if err != nil {
		return record, trace.Wrap(err, "open deferred edge spool record")
	}
	decodeErr := gob.NewDecoder(file).Decode(&record)
	closeErr := file.Close()
	if decodeErr != nil {
		return record, trace.Wrap(decodeErr, "decode deferred edge spool record")
	}
	if closeErr != nil {
		return record, trace.Wrap(closeErr, "close deferred edge spool record")
	}
	return record, nil
}

// cleanup removes all temporary parsed-edge records for this invocation.
// @intent ensure successful and failed staged updates do not retain temporary source-derived data.
// @sideEffect deletes the invocation-local spool directory.
func (s *deferredEdgeSpool) cleanup(logger *slog.Logger) {
	if s == nil || s.dir == "" {
		return
	}
	if err := os.RemoveAll(s.dir); err != nil && logger != nil {
		logger.Warn("cleanup deferred edge spool failed", "dir", s.dir, "error", err)
	}
}
