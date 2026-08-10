package mcp

import (
	"testing"
	"time"
)

// The database every handler test runs on is held in memory under a shared
// cache, and SQLite throws such a database away the instant its last connection
// closes. Today none of them close: database/sql keeps two idle connections and
// expires neither, because openSharedMemoryTestDB leaves the pool at its
// defaults. That is luck, not a promise — internal/db.ConfigurePool already
// hands postgres a five-minute idle timeout, and pointing these helpers at it,
// or setting any timeout at all, would empty the pool between two statements and
// leave the next query reading a database with no tables in it. Every count in
// this package would then be wrong rather than absent.
//
// So this test does to the pool what a timeout would do, and the fixture's rows
// have to still be there afterwards.
func TestSetupTestDeps_DatabaseOutlivesAnEmptiedConnectionPool(t *testing.T) {
	deps := coverageFixture(t)
	sqlDB, err := testDBFor(deps).DB()
	if err != nil {
		t.Fatal(err)
	}
	openedByPool := sqlDB.Stats().OpenConnections

	// Retiring every idle connection is what an idle timeout does, minus the
	// wait. A pooled connection in use is not idle, so anything the setup is
	// holding on purpose survives this and nothing else does.
	sqlDB.SetMaxIdleConns(0)
	sqlDB.SetConnMaxIdleTime(time.Millisecond)
	sqlDB.SetConnMaxLifetime(time.Millisecond)

	deadline := time.Now().Add(5 * time.Second)
	for sqlDB.Stats().Idle > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	stats := sqlDB.Stats()
	// Without this the test could pass by never having emptied anything.
	if closed := stats.MaxIdleClosed + stats.MaxIdleTimeClosed + stats.MaxLifetimeClosed; closed == 0 {
		t.Fatalf("the pool retired no connection, so nothing was tested: opened %d, stats %+v", openedByPool, stats)
	}
	if stats.Idle > 0 {
		t.Fatalf("the pool still holds %d idle connections after %v", stats.Idle, 5*time.Second)
	}

	payload := decodeSearchPayload(t, getTextContent(
		callTool(t, deps, "search", map[string]any{"query": "one"})))
	if got := payload.AnnotationCoverage.Declarations; got != 3 {
		t.Errorf("annotation_coverage.declarations = %d, want 3 — the database the fixture wrote to is gone", got)
	}
	if got := payload.AnnotationCoverage.WithReason; got != 2 {
		t.Errorf("annotation_coverage.with_reason = %d, want 2 — the database the fixture wrote to is gone", got)
	}
}
