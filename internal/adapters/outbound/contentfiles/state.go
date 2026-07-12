// @index Atomic persistence for explicitly addressed lint history and generated-rule state.
package contentfiles

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// StateFiles reads and atomically replaces explicit state-file paths.
// @intent keep lint history and auto-rule persistence outside application policy.
type StateFiles struct{}

// NewStateFiles constructs the stateless state-file adapter.
// @intent provide one persistence capability for lint history and generated rules.
func NewStateFiles() StateFiles { return StateFiles{} }

// ReadPath distinguishes a missing optional state file from a read failure.
// @intent restore optional lint state without treating first-run absence as an error.
// @sideEffect reads the requested file path.
func (StateFiles) ReadPath(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}

// WritePath atomically replaces one explicitly addressed state file.
// @intent durably persist lint history or generated rules without exposing partial content.
// @sideEffect creates parent directories and renames a synced temporary file.
func (StateFiles) WritePath(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Chmod(0o644)
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}
