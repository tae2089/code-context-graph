package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleHealth_OK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
	body := rec.Body.String()
	if body != `{"status":"ok"}` {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestHandleHealth_MethodNotAllowed(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/health", nil)
			rec := httptest.NewRecorder()

			handleHealth(rec, req)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("expected 405, got %d", rec.Code)
			}
		})
	}
}

func TestValidBearerToken(t *testing.T) {
	if !validBearerToken("Bearer secret", "secret") {
		t.Fatal("expected bearer token to validate")
	}
	if validBearerToken("Bearer wrong", "secret") {
		t.Fatal("expected wrong token to fail")
	}
	if validBearerToken("secret", "secret") {
		t.Fatal("expected missing bearer prefix to fail")
	}
}

func TestIsLoopbackHTTPAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{addr: "127.0.0.1:8080", want: true},
		{addr: "localhost:8080", want: true},
		{addr: ":8080", want: true},
		{addr: "0.0.0.0:8080", want: false},
		{addr: "192.168.0.10:8080", want: false},
	}
	for _, tt := range tests {
		if got := isLoopbackHTTPAddr(tt.addr); got != tt.want {
			t.Fatalf("isLoopbackHTTPAddr(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}
