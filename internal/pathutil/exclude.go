// Package pathutil provides path-related utilities for the ccg CLI.
package pathutil

import (
	"path"
	"regexp"
	"strings"

	"path/filepath"
)

// ShouldSkipDir는 디렉토리 이름이 기본 제외 대상인지 반환한다.
// .git, vendor, node_modules, 그리고 "."으로 시작하는 숨김 디렉토리를 제외한다.
// @intent 공통적으로 분석 대상에서 빼야 하는 디렉터리를 빠르게 걸러낸다.
// @domainRule 숨김 디렉터리는 현재 디렉터리 표기 "."를 제외하고 모두 스킵한다.
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
