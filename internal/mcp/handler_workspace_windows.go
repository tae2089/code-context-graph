//go:build windows

// @index Windows workspace file writes for MCP handlers.
package mcp

import "os"

// writeFileNoFollow writes workspace files using the Windows-safe fallback path.
// @intent provide the same workspace write entry point on Windows where O_NOFOLLOW is unavailable.
// @sideEffect truncates or creates the target file.
func writeFileNoFollow(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}
