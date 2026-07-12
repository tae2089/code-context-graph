package reference

import "testing"

func TestParseSymbolRef(t *testing.T) {
	ref, err := Parse("ccg://auth-svc/internal/auth/token.go#ValidateToken")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ref.Namespace != "auth-svc" || ref.Path != "internal/auth/token.go" || ref.Symbol != "ValidateToken" || ref.Scope != "symbol" {
		t.Fatalf("unexpected ref: %+v", ref)
	}
	if ref.Display() != "auth-svc/internal/auth/token.go#ValidateToken" {
		t.Fatalf("Display = %q", ref.Display())
	}
}

func TestParseNamespaceRef(t *testing.T) {
	ref, err := Parse("ccg://common-lib/")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ref.Namespace != "common-lib" || ref.Path != "" || ref.Symbol != "" || ref.Scope != "namespace" {
		t.Fatalf("unexpected ref: %+v", ref)
	}
}

func TestParseRejectsTraversal(t *testing.T) {
	for _, raw := range []string{
		"ccg://../internal/auth.go",
		"ccg://auth-svc/../secret.go",
		"ccg://auth-svc/internal/../../secret.go",
		"ccg://auth-svc/internal\\auth.go",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := Parse(raw); err == nil {
				t.Fatal("expected parse error")
			}
		})
	}
}
