package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

type SyncFunc func(ctx context.Context, repoFullName, cloneURL string)

type SyncHandlerFunc func(ctx context.Context, repoFullName, cloneURL string) error

type WebhookHandler struct {
	secret []byte
	filter *RepoFilter
	onSync SyncFunc
}

func NewWebhookHandler(secret []byte, filter *RepoFilter, onSync SyncFunc) *WebhookHandler {
	return &WebhookHandler{secret: secret, filter: filter, onSync: onSync}
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

	sig := r.Header.Get("X-Hub-Signature-256")
	if sig == "" {
		sig = r.Header.Get("X-Gitea-Signature")
	}
	if !h.verifySignature(body, sig) {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	if eventType == "" {
		eventType = r.Header.Get("X-Gitea-Event")
	}
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

	if !h.filter.IsAllowedRef(event.Repository.FullName, event.Ref) {
		slog.Info("skipping disallowed repo or branch", "repo", event.Repository.FullName, "ref", event.Ref)
		w.WriteHeader(http.StatusOK)
		return
	}

	slog.Info("processing push event", "repo", event.Repository.FullName, "ref", event.Ref)
	h.onSync(r.Context(), event.Repository.FullName, event.Repository.CloneURL)
	w.WriteHeader(http.StatusOK)
}

func (h *WebhookHandler) verifySignature(payload []byte, signature string) bool {
	if len(h.secret) == 0 {
		return true
	}
	if signature == "" {
		return false
	}

	mac := hmac.New(sha256.New, h.secret)
	mac.Write(payload)
	expectedHex := hex.EncodeToString(mac.Sum(nil))

	// GitHub: "sha256=<hex>", Gitea: "<hex>"
	sig := strings.TrimPrefix(signature, "sha256=")
	return hmac.Equal([]byte(expectedHex), []byte(sig))
}

func ExtractNamespace(repoFullName string) string {
	idx := strings.Index(repoFullName, "/")
	if idx < 0 {
		return repoFullName
	}
	rest := repoFullName[idx+1:]
	return strings.ReplaceAll(rest, "/", "-")
}
