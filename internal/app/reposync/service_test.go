package reposync

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

type checkoutStub struct {
	dir     string
	err     error
	request CheckoutRequest
}

func (s *checkoutStub) Sync(_ context.Context, request CheckoutRequest) (string, error) {
	s.request = request
	return s.dir, s.err
}

type buildScopeStub struct {
	scope BuildScope
	err   error
	calls int
}

func (s *buildScopeStub) Load(string) (BuildScope, error) {
	s.calls++
	return s.scope, s.err
}

type graphStub struct {
	request GraphRequest
	err     error
	calls   int
}

func (s *graphStub) Update(_ context.Context, request GraphRequest) (UpdateStats, error) {
	s.calls++
	s.request = request
	return UpdateStats{Added: 1}, s.err
}

type cacheStub struct{ calls int }

func (s *cacheStub) Invalidate() { s.calls++ }

func TestService_CheckoutFailureStopsBeforeOtherSideEffects(t *testing.T) {
	want := errors.New("checkout failed")
	checkout := &checkoutStub{err: want}
	graph := &graphStub{}
	cache := &cacheStub{}
	buildScope := &buildScopeStub{}
	err := (&Service{Checkout: checkout, BuildScope: buildScope, Graph: graph, Cache: cache}).Sync(context.Background(), "org/api", "url", "main")
	if !errors.Is(err, want) || buildScope.calls != 0 || graph.calls != 0 || cache.calls != 0 {
		t.Fatalf("err=%v buildScope=%d graph=%d cache=%d", err, buildScope.calls, graph.calls, cache.calls)
	}
}

func TestService_CheckoutFailureRetainsStageSpecificOperationalLog(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	svc := &Service{Checkout: &checkoutStub{err: errors.New("auth denied")}, BuildScope: &buildScopeStub{}, Graph: &graphStub{}, Logger: logger}
	if err := svc.Sync(context.Background(), "org/api", "trusted-url", "main"); err == nil {
		t.Fatal("expected checkout error")
	}
	got := logs.String()
	for _, want := range []string{"webhook clone/pull failed", "repo=org/api", "namespace=api", "branch=main", "auth denied"} {
		if !strings.Contains(got, want) {
			t.Fatalf("log %q missing %q", got, want)
		}
	}
}

func TestService_IncludeConfigFailureIsNonRetryable(t *testing.T) {
	want := errors.New("invalid include config")
	buildScope := &buildScopeStub{err: want}
	graph := &graphStub{}
	cache := &cacheStub{}
	err := (&Service{Checkout: &checkoutStub{dir: "/repos/api"}, BuildScope: buildScope, Graph: graph, Cache: cache}).Sync(context.Background(), "org/api", "url", "main")
	if !errors.Is(err, want) || !IsNonRetryable(err) || graph.calls != 0 || cache.calls != 0 {
		t.Fatalf("err=%v nonretryable=%v graph=%d cache=%d", err, IsNonRetryable(err), graph.calls, cache.calls)
	}
}

func TestService_SuccessPassesGraphContractAndInvalidatesCacheOnce(t *testing.T) {
	checkout := &checkoutStub{dir: "/repos/api"}
	buildScope := &buildScopeStub{scope: BuildScope{IncludePaths: []string{"cmd", "internal"}}}
	graph := &graphStub{}
	cache := &cacheStub{}
	svc := &Service{Checkout: checkout, BuildScope: buildScope, Graph: graph, Cache: cache, MaxFileBytes: 10, MaxTotalParsedBytes: 20, FailOnUnreadable: true}
	if err := svc.Sync(context.Background(), "org/api", "trusted-url", "develop"); err != nil {
		t.Fatal(err)
	}
	if checkout.request.Namespace != "api" || checkout.request.Branch != "develop" {
		t.Fatalf("checkout request=%+v", checkout.request)
	}
	if graph.calls != 1 || graph.request.RepoDir != "/repos/api" || graph.request.Namespace != "api" || graph.request.MaxFileBytes != 10 || graph.request.MaxTotalParsedBytes != 20 || !graph.request.FailOnUnreadable {
		t.Fatalf("graph request=%+v calls=%d", graph.request, graph.calls)
	}
	if got, want := graph.request.IncludePaths, []string{"cmd", "internal"}; !equalStrings(got, want) {
		t.Fatalf("IncludePaths = %v, want %v", got, want)
	}
	if cache.calls != 1 {
		t.Fatalf("cache calls=%d", cache.calls)
	}
}

func TestService_SuccessPassesRepositoryExcludePatternsToGraph(t *testing.T) {
	buildScope := &buildScopeStub{scope: BuildScope{IncludePaths: []string{"cmd", "internal"}, ExcludePatterns: []string{"vendor", "*_generated.go"}}}
	graph := &graphStub{}
	svc := &Service{Checkout: &checkoutStub{dir: "/repos/api"}, BuildScope: buildScope, Graph: graph}

	if err := svc.Sync(context.Background(), "org/api", "trusted-url", "main"); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if buildScope.calls != 1 {
		t.Fatalf("build scope loads = %d, want 1", buildScope.calls)
	}
	if got, want := graph.request.ExcludePatterns, []string{"vendor", "*_generated.go"}; !equalStrings(got, want) {
		t.Fatalf("ExcludePatterns = %v, want %v", got, want)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
