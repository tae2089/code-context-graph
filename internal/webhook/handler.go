package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

type SyncFunc func(repoFullName, cloneURL string)

type WebhookHandler struct {
	secret    []byte
	allowlist *RepoAllowlist
	onSync    SyncFunc
}

func NewWebhookHandler(secret []byte, allowlist *RepoAllowlist, onSync SyncFunc) *WebhookHandler {
	return &WebhookHandler{secret: secret, allowlist: allowlist, onSync: onSync}
}

type pushEvent struct {
	Ref        string `json:"ref"`
	Repository struct {
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
}

// maxWebhookPayload limits the webhook request body to 10 MB.
const maxWebhookPayload = 10 << 20

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxWebhookPayload)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body failed", http.StatusBadRequest)
		return
	}

	if !h.verifySignature(body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	if eventType != "push" {
		slog.Info("skipping non-push event", "event", eventType)
		w.WriteHeader(http.StatusOK)
		return
	}

	var event pushEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if !isMainBranch(event.Ref) {
		slog.Info("skipping non-main branch", "ref", event.Ref)
		w.WriteHeader(http.StatusOK)
		return
	}

	if !h.allowlist.IsAllowed(event.Repository.FullName) {
		slog.Info("skipping disallowed repo", "repo", event.Repository.FullName)
		w.WriteHeader(http.StatusOK)
		return
	}

	slog.Info("processing push event", "repo", event.Repository.FullName, "ref", event.Ref)
	h.onSync(event.Repository.FullName, event.Repository.CloneURL)
	w.WriteHeader(http.StatusOK)
}

func (h *WebhookHandler) verifySignature(payload []byte, signature string) bool {
	if len(h.secret) == 0 {
		return true
	}

	mac := hmac.New(sha256.New, h.secret)
	mac.Write(payload)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

func isMainBranch(ref string) bool {
	return ref == "refs/heads/main" || ref == "refs/heads/master"
}

func ExtractNamespace(repoFullName string) string {
	idx := strings.Index(repoFullName, "/")
	if idx < 0 {
		return repoFullName
	}
	rest := repoFullName[idx+1:]
	return strings.ReplaceAll(rest, "/", "-")
}
