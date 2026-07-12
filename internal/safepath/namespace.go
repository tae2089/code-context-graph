package safepath

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ValidateNamespacePath rejects namespace and file inputs that could escape a namespace root.
// @intent keep lexical namespace traversal validation beside symlink and canonical containment safeguards.
// @domainRule namespace must be one safe segment; filePath must be relative and free of parent references.
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
