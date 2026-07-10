// @index Disk-backed spool records used to bound memory during build and update.
package service

import (
	"encoding/gob"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/analysis/incremental"
	"github.com/tae2089/code-context-graph/internal/model"
	"github.com/tae2089/code-context-graph/internal/parse/treesitter"
)

// spooledBuildRecord is the on-disk representation of one parsed file used by the build spool.
// @intent let the build transaction stream parsed input from disk instead of holding all files in memory.
type spooledBuildRecord struct {
	RelPath     string
	Nodes       []model.Node
	PackageName string
	Interfaces  []treesitter.PackageInterfaceInfo
	Comments    []treesitter.CommentBlock
	Language    string
	SourceLines []string
	Edges       []model.Edge
	Bytes       int64
}

// buildSpool is the temporary on-disk staging area for parsed build records.
// @intent decouple parsing from the build transaction so the DB tx only opens once parsing succeeds.
type buildSpool struct {
	dir      string
	records  []string
	packages map[string]languagePackageInfo
	stats    BuildStats
}

// spooledUpdateRecord is one batch of file inputs persisted before the incremental update transaction starts.
// @intent stream incremental sync inputs from disk to bound peak memory.
type spooledUpdateRecord struct {
	Files map[string]incremental.FileInfo
	Bytes int64
}

// updateSpool is the temporary on-disk staging area for an incremental update pass.
// @intent capture the current file set, hashes, and force-reparse decisions before the update transaction begins.
type updateSpool struct {
	dir           string
	records       []string
	currentFiles  map[string]struct{}
	currentHashes map[string]string
	packages      map[string]languagePackageInfo
	forceFiles    map[string]struct{}
}

// writeRecord encodes one parsed file as a gob-serialized spool record on disk.
// @intent persist parsed input for later transactional replay without holding it in memory.
// @sideEffect creates a file under the spool directory.
func (b *buildSpool) writeRecord(seq int, record spooledBuildRecord) error {
	path := filepath.Join(b.dir, fmt.Sprintf("%06d.gob", seq))
	file, err := os.Create(path)
	if err != nil {
		return trace.Wrap(err, "create build spool record")
	}
	encErr := gob.NewEncoder(file).Encode(record)
	closeErr := file.Close()
	if encErr != nil {
		return trace.Wrap(encErr, "encode build spool record")
	}
	if closeErr != nil {
		return trace.Wrap(closeErr, "close build spool record")
	}
	b.records = append(b.records, path)
	return nil
}

// readRecord decodes a previously-written build spool record from disk.
// @intent stream parsed input back into the build transaction one file at a time.
func (b *buildSpool) readRecord(path string) (spooledBuildRecord, error) {
	var record spooledBuildRecord
	file, err := os.Open(path)
	if err != nil {
		return record, trace.Wrap(err, "open build spool record")
	}
	decodeErr := gob.NewDecoder(file).Decode(&record)
	closeErr := file.Close()
	if decodeErr != nil {
		return record, trace.Wrap(decodeErr, "decode build spool record")
	}
	if closeErr != nil {
		return record, trace.Wrap(closeErr, "close build spool record")
	}
	return record, nil
}

// cleanup removes the spool directory and logs a warning on failure.
// @intent reclaim spool disk space whether the build succeeded or failed.
// @sideEffect deletes the temporary spool directory.
func (b *buildSpool) cleanup(logger *slog.Logger) {
	if b == nil || b.dir == "" {
		return
	}
	if err := os.RemoveAll(b.dir); err != nil && logger != nil {
		logger.Warn("cleanup build spool failed", "dir", b.dir, "error", err)
	}
}

// writeRecord encodes one batch of update inputs as a gob-serialized spool record on disk.
// @intent persist update inputs for transactional replay without holding all batches in memory.
// @sideEffect creates a file under the spool directory.
func (u *updateSpool) writeRecord(seq int, record spooledUpdateRecord) error {
	path := filepath.Join(u.dir, fmt.Sprintf("%06d.gob", seq))
	file, err := os.Create(path)
	if err != nil {
		return trace.Wrap(err, "create update spool record")
	}
	encErr := gob.NewEncoder(file).Encode(record)
	closeErr := file.Close()
	if encErr != nil {
		return trace.Wrap(encErr, "encode update spool record")
	}
	if closeErr != nil {
		return trace.Wrap(closeErr, "close update spool record")
	}
	u.records = append(u.records, path)
	return nil
}

// readRecord decodes a previously-written update spool record from disk.
// @intent stream update inputs back into the update transaction in batches.
func (u *updateSpool) readRecord(path string) (spooledUpdateRecord, error) {
	var record spooledUpdateRecord
	file, err := os.Open(path)
	if err != nil {
		return record, trace.Wrap(err, "open update spool record")
	}
	decodeErr := gob.NewDecoder(file).Decode(&record)
	closeErr := file.Close()
	if decodeErr != nil {
		return record, trace.Wrap(decodeErr, "decode update spool record")
	}
	if closeErr != nil {
		return record, trace.Wrap(closeErr, "close update spool record")
	}
	return record, nil
}

// cleanup removes the update spool directory and logs a warning on failure.
// @intent reclaim spool disk space whether the update succeeded or failed.
// @sideEffect deletes the temporary spool directory.
func (u *updateSpool) cleanup(logger *slog.Logger) {
	if u == nil || u.dir == "" {
		return
	}
	if err := os.RemoveAll(u.dir); err != nil && logger != nil {
		logger.Warn("cleanup update spool failed", "dir", u.dir, "error", err)
	}
}
