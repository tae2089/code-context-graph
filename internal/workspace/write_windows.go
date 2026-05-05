//go:build windows

package workspace

import "os"

// writeFileNoFollow writes a file using the Windows-safe fallback path where O_NOFOLLOW is unavailable.
// @intent provide the same workspace write entry point on Windows where O_NOFOLLOW is unavailable.
// @sideEffect truncates or creates the target file.
func writeFileNoFollow(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}
