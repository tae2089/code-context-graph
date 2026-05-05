//go:build !windows

package workspace

import (
	"os"
	"syscall"
)

// writeFileNoFollow writes a file without following symlinks on Unix.
// @intent prevent workspace upload paths from escaping the allowed root through symlink traversal.
// @sideEffect creates or truncates the target file and fsyncs it to disk.
func writeFileNoFollow(path string, data []byte, perm os.FileMode) error {
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_TRUNC|syscall.O_NOFOLLOW, uint32(perm))
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}
