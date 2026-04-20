package sample

// @intent 인라인 금지 핫 패스
//go:noinline
func HotPath() int {
	return 42
}
