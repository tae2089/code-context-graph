//go:build windows

package mcp

import "os"

func writeFileNoFollow(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}
