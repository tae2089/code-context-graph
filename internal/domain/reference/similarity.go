package reference

import "strings"

// CommonSuffixDepth returns the number of matching slash-separated path segments
// counted from the end of both inputs.
// @intent provide one deterministic import-reference similarity score for graph lookup and ingest resolution.
func CommonSuffixDepth(a, b string) int {
	a = strings.Trim(a, "/")
	b = strings.Trim(b, "/")
	if a == "" || b == "" {
		return 0
	}
	depth := 0
	for {
		aSlash := strings.LastIndexByte(a, '/')
		bSlash := strings.LastIndexByte(b, '/')
		if a[aSlash+1:] != b[bSlash+1:] {
			return depth
		}
		depth++
		if aSlash < 0 || bSlash < 0 {
			return depth
		}
		a = a[:aSlash]
		b = b[:bSlash]
	}
}
