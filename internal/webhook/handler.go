// @index HTTP webhook intake for repository sync dispatch.
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

	"go.opentelemetry.io/otel/attribute"

	"github.com/tae2089/code-context-graph/internal/obs"
)

// @intent define the callback signature webhook intake invokes to trigger repository sync.
type SyncFunc func(ctx context.Context, repoFullName, cloneURL, branch string) error

// @intent share the same sync callback shape between direct handler wiring and queue-based processing.
type SyncHandlerFunc func(ctx context.Context, repoFullName, cloneURL, branch string) error

// @intent bundle webhook validation policy and dispatch dependencies into one reusable HTTP handler.
type WebhookHandler struct {
	secret        []byte
	filter        *RepoFilter
	onSync        SyncFunc
	insecure      bool
	cloneBaseURLs []string
}

// NewWebhookHandler wires a webhook handler from the common secret/filter/sync callback inputs.
// @intent keep the default construction path small while routing all configuration through the shared config builder.
// @param secret is the shared webhook secret used for signature validation.
// @param filter decides which repo and branch combinations are eligible for sync.
// @param onSync dispatches the validated sync request.
// @ensures returns a handler configured with the default secure validation path.
func NewWebhookHandler(secret []byte, filter *RepoFilter, onSync SyncFunc) *WebhookHandler {
	return NewWebhookHandlerWithConfig(WebhookHandlerConfig{Secret: secret, Filter: filter, OnSync: onSync})
}

// @intent carry all constructor options for webhook validation, clone URL policy, and sync dispatch.
type WebhookHandlerConfig struct {
	Secret        []byte
	Filter        *RepoFilter
	OnSync        SyncFunc
	Insecure      bool
	CloneBaseURL  string
	CloneBaseURLs []string
}

// NewWebhookHandlerWithOptions builds a handler with the legacy option-style constructor.
// @intent preserve older call sites while the config-based constructor owns the actual assembly logic.
// @param insecure allows payload delivery without signature validation when true.
// @ensures returns a handler configured equivalently to the legacy constructor inputs.
func NewWebhookHandlerWithOptions(secret []byte, filter *RepoFilter, onSync SyncFunc, insecure bool) *WebhookHandler {
	return NewWebhookHandlerWithConfig(WebhookHandlerConfig{Secret: secret, Filter: filter, OnSync: onSync, Insecure: insecure})
}

// NewWebhookHandlerWithConfig assembles webhook validation and clone URL policy into one handler.
// @intent make webhook intake configurable without duplicating constructor logic across CLI and tests.
// @param cfg carries webhook secret, filtering, sync callback, and clone URL policy.
// @ensures returns a handler whose clone base URLs preserve config ordering with CloneBaseURL first when provided.
func NewWebhookHandlerWithConfig(cfg WebhookHandlerConfig) *WebhookHandler {
	cloneBaseURLs := append([]string(nil), cfg.CloneBaseURLs...)
	if cfg.CloneBaseURL != "" {
		cloneBaseURLs = append([]string{cfg.CloneBaseURL}, cloneBaseURLs...)
	}
	return &WebhookHandler{secret: cfg.Secret, filter: cfg.Filter, onSync: cfg.OnSync, insecure: cfg.Insecure, cloneBaseURLs: cloneBaseURLs}
}

// @intent decode the subset of GitHub/Gitea push payload fields the handler needs for filtering and dispatch.
type pushEvent struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Deleted    bool   `json:"deleted"`
	Repository struct {
		FullName string `json:"full_name"`
		CloneURL string `json:"clone_url"`
	} `json:"repository"`
}

// @intent cap webhook request body size so a malformed or hostile payload cannot exhaust server memory.
// maxWebhookPayload limits the webhook request body to 10 MB.
const maxWebhookPayload = 10 << 20

// ServeHTTP validates a webhook push event and dispatches repository sync when it passes policy checks.
// @intent turn GitHub or Gitea push deliveries into safe, filtered sync requests for the build pipeline.
// @sideEffect reads the request body and invokes the configured sync callback.
// @domainRule only signed push events for allowed repository/branch pairs are dispatched.
// @ensures writes an HTTP status describing acceptance, rejection, or sync backpressure for the delivery.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, span := obs.ServerSpan(r.Context(), "webhook.push", r.Header,
		attribute.String("ccg.component", "webhook"),
		attribute.String("http.method", r.Method),
	)
	defer span.End()
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
		slog.InfoContext(ctx, "skipping non-push event", append(obs.TraceLogArgs(ctx), "event", eventType)...)
		w.WriteHeader(http.StatusOK)
		return
	}

	var event pushEvent
	if err := json.Unmarshal(body, &event); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	branch, ok := NormalizeBranchRef(event.Ref)
	if !ok {
		slog.InfoContext(ctx, "skipping non-branch ref", append(obs.TraceLogArgs(ctx), "repo", event.Repository.FullName, "ref", event.Ref)...)
		w.WriteHeader(http.StatusOK)
		return
	}

	if isDeletedBranchPush(event) {
		slog.InfoContext(ctx, "skipping deleted branch push", append(obs.TraceLogArgs(ctx), "repo", event.Repository.FullName, "ref", event.Ref, "branch", branch)...)
		w.WriteHeader(http.StatusOK)
		return
	}

	if !h.filter.IsAllowedBranch(event.Repository.FullName, branch) {
		slog.InfoContext(ctx, "skipping disallowed repo or branch", append(obs.TraceLogArgs(ctx), "repo", event.Repository.FullName, "ref", event.Ref)...)
		w.WriteHeader(http.StatusOK)
		return
	}

	cloneURL, err := ResolveCloneURL(event.Repository.FullName, event.Repository.CloneURL, h.cloneBaseURLs, h.insecure)
	if err != nil {
		http.Error(w, "invalid clone target", http.StatusForbidden)
		return
	}

	slog.InfoContext(ctx, "processing push event", append(obs.TraceLogArgs(ctx), "repo", event.Repository.FullName, "ref", event.Ref, "branch", branch)...)
	if err := h.onSync(ctx, event.Repository.FullName, cloneURL, branch); err != nil {
		if err == ErrSyncQueueFull {
			http.Error(w, "sync queue full", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "sync dispatch failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// NormalizeBranchRef extracts the branch name from a refs/heads/* git ref.
// @intent ignore tag and non-branch webhook refs before repository policy evaluation.
// @param ref is the raw git reference from the webhook payload.
// @return returns the branch name and true only for refs/heads/* values.
func NormalizeBranchRef(ref string) (string, bool) {
	branch, ok := strings.CutPrefix(ref, "refs/heads/")
	if !ok || branch == "" {
		return "", false
	}
	return branch, true
}

// @intent skip webhook pushes that only report branch deletion instead of a syncable commit head.
func isDeletedBranchPush(event pushEvent) bool {
	if event.Deleted {
		return true
	}
	return event.After != "" && strings.Trim(event.After, "0") == ""
}

// @intent authenticate webhook payloads before the sync pipeline trusts their repository metadata.
// @param payload is the raw webhook request body.
// @param signature is the GitHub or Gitea signature header value.
// @domainRule insecure mode bypasses signature verification entirely.
// @ensures returns true only when the payload matches the configured shared secret format for the sender.
func (h *WebhookHandler) verifySignature(payload []byte, signature string) bool {
	if h.insecure {
		return true
	}
	if len(h.secret) == 0 {
		return false
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

// ExtractNamespace derives a workspace-safe namespace from a repository full name.
// @intent keep repo-backed namespaces predictable when organizations contain nested path segments.
// @param repoFullName is the full repository name, typically org/repo.
// @return returns the repository portion after the first slash, with any remaining slashes replaced by dashes.
func ExtractNamespace(repoFullName string) string {
	idx := strings.Index(repoFullName, "/")
	if idx < 0 {
		return repoFullName
	}
	rest := repoFullName[idx+1:]
	return strings.ReplaceAll(rest, "/", "-")
}
