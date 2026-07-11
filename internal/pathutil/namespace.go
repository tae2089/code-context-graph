package pathutil

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ValidateNamespacePath rejects namespace and file inputs that could escape the namespace root.
// @intent block path traversal against namespace-scoped filesystem reads.
// @domainRule namespace must be a single safe segment (no "/", "\\", "..", ".", or absolute path);
// filePath, when set, must be relative and free of parent-references.
// @param filePath relative path inside the namespace; "" validates the namespace name alone.
func ValidateNamespacePath(namespace, filePath string) error {
	if namespace == "" {
		return fmt.Errorf("namespace must not be empty")
	}
	cleanNS := filepath.Clean(namespace)
	if cleanNS == "." || cleanNS == ".." || filepath.IsAbs(cleanNS) || strings.HasPrefix(cleanNS, "..") || strings.ContainsAny(cleanNS, `/\`) {
		return fmt.Errorf("invalid namespace: must be a single safe name")
	}
	if filePath != "" {
		cleanFP := filepath.Clean(filePath)
		if filepath.IsAbs(cleanFP) || strings.HasPrefix(cleanFP, "..") {
			return fmt.Errorf("invalid file_path: path traversal not allowed")
		}
	}
	return nil
}
