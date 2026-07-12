// Package pathspec evaluates lexical path filters without filesystem access.
package pathspec

import (
	"path"
	"regexp"
	"strings"

	"path/filepath"
)

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
// @intent 설정과 CLI에서 받은 제외 패턴을 상대 경로에 일관되게 적용한다.
// @domainRule 슬래시가 없는 패턴은 파일 basename에만 매칭한다.
func MatchExcludes(patterns []string, relPath string) bool {
	if len(patterns) == 0 {
		return false
	}

	slashPath := strings.ReplaceAll(filepath.ToSlash(relPath), `\`, "/")
	base := path.Base(slashPath)

	for _, pat := range patterns {
		pat = filepath.ToSlash(strings.TrimSuffix(pat, "/"))

		if IsRegexPattern(pat) {
			re, err := regexp.Compile(pat)
			if err != nil {
				continue
			}
			if re.MatchString(slashPath) {
				return true
			}
			continue
		}

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

// IsRegexPattern detects whether a pattern uses regex syntax rather than glob.
// Checks for anchors ($) and escape sequences (\.) that are regex-specific.
// @intent glob과 정규표현식 패턴을 구분해 적절한 매칭 엔진을 선택한다.
func IsRegexPattern(pat string) bool {
	return strings.ContainsAny(pat, "$^+{}|") || strings.Contains(pat, `\.`) || strings.Contains(pat, ".*")
}

// MatchIncludePaths reports whether relPath falls inside or is an ancestor of any configured include path.
// @intent let walkers prune directories that lie outside user-selected include scopes while still descending into ancestors.
func MatchIncludePaths(relPath string, includePaths []string) bool {
	relPath = normalizeIncludePath(relPath)
	for _, inc := range includePaths {
		inc = normalizeIncludePath(inc)
		if relPath == inc || strings.HasPrefix(relPath, inc+"/") || strings.HasPrefix(inc, relPath+"/") {
			return true
		}
	}
	return false
}

// HasPathPrefix reports whether path is the same as prefix or is nested under it.
// Both inputs are normalized to slash-separated clean relative paths.
// @intent compare include path scopes after normalization so callers can test path containment reliably.
func HasPathPrefix(pathValue, prefix string) bool {
	pathValue = normalizeIncludePath(pathValue)
	prefix = normalizeIncludePath(prefix)
	if prefix == "" || prefix == "." {
		return true
	}
	return pathValue == prefix || strings.HasPrefix(pathValue, prefix+"/")
}

// normalizeIncludePath converts user-supplied include paths to a comparable slash form.
// @intent guarantee comparisons treat "./foo", "foo", and "foo/" as the same logical path.
func normalizeIncludePath(p string) string {
	clean := path.Clean(strings.ReplaceAll(filepath.ToSlash(strings.TrimSpace(p)), `\`, "/"))
	if clean == "." {
		return clean
	}
	return strings.TrimPrefix(clean, "./")
}
