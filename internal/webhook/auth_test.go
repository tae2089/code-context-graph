package webhook

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

func generateTestRSAKey(t *testing.T) ([]byte, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	pemData := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return pemData, key
}

func TestSSHAuth_FromKeyFile(t *testing.T) {
	pemData, _ := generateTestRSAKey(t)
	keyFile := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(keyFile, pemData, 0600); err != nil {
		t.Fatal(err)
	}

	ga := &GitAuth{SSHKeyPath: keyFile}
	method, err := ga.Resolve()
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if method == nil {
		t.Fatal("expected non-nil AuthMethod")
	}

	if _, ok := method.(*ssh.PublicKeys); !ok {
		t.Errorf("expected *ssh.PublicKeys, got %T", method)
	}
}

func TestSSHAuth_FromKeyData(t *testing.T) {
	pemData, _ := generateTestRSAKey(t)

	ga := &GitAuth{SSHKeyData: pemData}
	method, err := ga.Resolve()
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if method == nil {
		t.Fatal("expected non-nil AuthMethod")
	}

	if _, ok := method.(*ssh.PublicKeys); !ok {
		t.Errorf("expected *ssh.PublicKeys, got %T", method)
	}
}

func TestGitHubAppAuth_JWTGeneration(t *testing.T) {
	pemData, _ := generateTestRSAKey(t)
	appID := int64(12345)

	token, err := GenerateAppJWT(appID, pemData)
	if err != nil {
		t.Fatalf("GenerateAppJWT error: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty JWT")
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d", len(parts))
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}

	var claims struct {
		Iss string `json:"iss"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}

	if claims.Iss != "12345" {
		t.Errorf("iss = %q, want %q", claims.Iss, "12345")
	}

	expDuration := time.Duration(claims.Exp-claims.Iat) * time.Second
	if expDuration < 9*time.Minute || expDuration > 11*time.Minute {
		t.Errorf("exp-iat = %v, want ~10 minutes", expDuration)
	}
}

func TestGitHubAppAuth_TokenAuth(t *testing.T) {
	ga := &GitAuth{InstallToken: "ghs_abc123"}
	method, err := ga.Resolve()
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if method == nil {
		t.Fatal("expected non-nil AuthMethod")
	}

	basic, ok := method.(*http.BasicAuth)
	if !ok {
		t.Fatalf("expected *http.BasicAuth, got %T", method)
	}
	if basic.Password != "ghs_abc123" {
		t.Errorf("password = %q, want %q", basic.Password, "ghs_abc123")
	}
}

func TestGitAuth_ResolveMethod(t *testing.T) {
	pemData, _ := generateTestRSAKey(t)
	keyFile := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(keyFile, pemData, 0600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		auth     GitAuth
		wantType string
	}{
		{
			name:     "ssh_key_file",
			auth:     GitAuth{SSHKeyPath: keyFile},
			wantType: fmt.Sprintf("%T", &ssh.PublicKeys{}),
		},
		{
			name:     "ssh_key_data",
			auth:     GitAuth{SSHKeyData: pemData},
			wantType: fmt.Sprintf("%T", &ssh.PublicKeys{}),
		},
		{
			name:     "install_token",
			auth:     GitAuth{InstallToken: "ghs_test"},
			wantType: fmt.Sprintf("%T", &http.BasicAuth{}),
		},
		{
			name:     "empty_returns_nil",
			auth:     GitAuth{},
			wantType: "<nil>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			method, err := tt.auth.Resolve()
			if err != nil {
				t.Fatalf("Resolve() error: %v", err)
			}

			var gotType string
			if method == nil {
				gotType = "<nil>"
			} else {
				gotType = fmt.Sprintf("%T", method)
			}

			if gotType != tt.wantType {
				t.Errorf("Resolve() type = %q, want %q", gotType, tt.wantType)
			}
		})
	}
}
