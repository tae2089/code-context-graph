package runtime

import "testing"

func TestRuntimeCloseIsIdempotent(t *testing.T) {
	rt := NewRuntime(nil)
	closed := 0
	rt.closeHook = func() { closed++ }

	rt.Close()
	rt.Close()

	if closed != 1 {
		t.Fatalf("close hook calls = %d, want 1", closed)
	}
}
