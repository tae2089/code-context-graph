package server

import "testing"

func TestOnceCleanupRunsAtMostOnce(t *testing.T) {
	calls := 0
	cleanup := onceCleanup(func() { calls++ })

	cleanup()
	cleanup()

	if calls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", calls)
	}
}
