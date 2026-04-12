package cli

import (
	"testing"
)

func TestServeCommand_AcceptsDBFlags(t *testing.T) {
	deps, stdout, stderr := newTestDeps()

	var received ServeConfig
	deps.ServeFunc = func(cfg ServeConfig) error {
		received = cfg
		return nil
	}

	err := executeCmd(deps, stdout, stderr, "serve", "--db", "postgres", "--dsn", "host=localhost dbname=ccg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if received.DBDriver != "postgres" {
		t.Fatalf("expected db=postgres, got %s", received.DBDriver)
	}
	if received.DSN != "host=localhost dbname=ccg" {
		t.Fatalf("expected dsn='host=localhost dbname=ccg', got %s", received.DSN)
	}
}
