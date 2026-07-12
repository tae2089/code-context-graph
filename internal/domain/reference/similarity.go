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
	aParts := strings.Split(a, "/")
	bParts := strings.Split(b, "/")
	depth := 0
	for i, j := len(aParts)-1, len(bParts)-1; i >= 0 && j >= 0; i, j = i-1, j-1 {
		if aParts[i] != bParts[j] {
			break
		}
		depth++
	}
	return depth
}
