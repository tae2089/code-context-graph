//go:build !windows

package mcp

import (
	"os"
	"syscall"
)

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
