// Package pathutil provides path-related utilities for the ccg CLI.
package pathutil

import (
	"path"
	"strings"

	"path/filepath"
)

// ShouldSkipDir는 디렉토리 이름이 기본 제외 대상인지 반환한다.
// .git, vendor, node_modules, 그리고 "."으로 시작하는 숨김 디렉토리를 제외한다.
func ShouldSkipDir(name string) bool {
	switch name {
	case ".git", "vendor", "node_modules":
		return true
	}
	return name != "." && strings.HasPrefix(name, ".")
}

// MatchExcludes reports whether relPath matches any of the given exclude patterns.
//
// Pattern forms:
//   - Path prefix: "vendor" or "internal/legacy" — excludes everything under that path.
//   - Filename glob: "*.pb.go" — matches files by name anywhere in the tree.
//     Patterns without "/" are applied against the base file name only.
//   - Full-path glob: "internal/gen/*.go" — matched against the full slash-separated relPath.
//
// relPath should be relative to the project root. OS-specific separators are
// normalized to forward slashes before matching.
func MatchExcludes(patterns []string, relPath string) bool {
	if len(patterns) == 0 {
		return false
	}

	slashPath := strings.ReplaceAll(filepath.ToSlash(relPath), `\`, "/")
	base := path.Base(slashPath)

	for _, pat := range patterns {
		pat = filepath.ToSlash(strings.TrimSuffix(pat, "/"))

		// 1. Exact match or path prefix match (directory exclusion).
		if slashPath == pat || strings.HasPrefix(slashPath, pat+"/") {
			return true
		}

		// 2. Full-path glob (pattern contains "/").
		if strings.Contains(pat, "/") {
			if ok, _ := path.Match(pat, slashPath); ok {
				return true
			}
			continue
		}

		// 3. Filename-only glob (no "/" in pattern).
		if ok, _ := path.Match(pat, base); ok {
			return true
		}
	}

	return false
}
