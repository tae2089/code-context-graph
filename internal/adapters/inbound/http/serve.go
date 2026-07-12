package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/server"
	"github.com/tae2089/trace"

	"github.com/tae2089/code-context-graph/internal/adapters/inbound/mcp"
	"github.com/tae2089/code-context-graph/internal/app/reposync"
	ccgobs "github.com/tae2089/code-context-graph/internal/obs"
)

// HostDeps contains already-composed handlers and lifecycle hooks for the HTTP host.
// @intent keep protocol hosting independent of runtime, persistence, Wiki, and webhook construction.
type HostDeps struct {
	Logger       *slog.Logger
	MCPServer    *mcpgo.MCPServer
	DBReady      func(*http.Request) error
	WikiAPI      http.Handler
	WikiStatic   http.Handler
	Webhook      http.Handler
	SyncQueue    *reposync.SyncQueue
	CleanupQueue func()
}

// RunStreamableHTTP serves the MCP server over streamable HTTP.
// @intent MCP, health, readiness, status, webhook 엔드포인트를 하나의 HTTP 런타임으로 노출한다.
// @sideEffect HTTP 서버, 시그널 핸들러, 웹훅 동기화 큐를 생성하고 종료 시 drain한다.
func RunStreamableHTTP(deps HostDeps, cfg Config) error {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("serving MCP over streamable-http", "addr", cfg.HTTPAddr, "stateless", cfg.Stateless)

	if err := ValidateHTTPExposure(cfg); err != nil {
		return err
	}

	opts := []mcpgo.StreamableHTTPOption{
		mcpgo.WithEndpointPath("/mcp"),
	}
	if cfg.Stateless {
		opts = append(opts, mcpgo.WithStateLess(true))
	}

	httpSrv := mcpgo.NewStreamableHTTPServer(deps.MCPServer, opts...)

	mux := http.NewServeMux()
	mux.Handle("/mcp", MCPAuthMiddleware(cfg.HTTPBearerToken, WithHTTPTraceContext(mcp.LimitHTTPBody(httpSrv))))
	mux.HandleFunc("/health", HandleHealth)
	dbReadyCheck := deps.DBReady
	if dbReadyCheck == nil {
		dbReadyCheck = func(*http.Request) error { return fmt.Errorf("database not configured") }
	}

	syncQueue := deps.SyncQueue
	cleanupSyncQueue := onceCleanup(deps.CleanupQueue)
	defer cleanupSyncQueue()

	mux.Handle("/ready", ReadyHandler(func(r *http.Request) error {
		if err := dbReadyCheck(r); err != nil {
			return err
		}
		if err := WebhookBlockingReadyCheck(syncQueue, cfg.WebhookAttemptTimeout); err != nil {
			return err
		}
		return nil
	}))
	// /status exposes repo names, branches, and raw error strings from the sync queue,
	// so it requires the same bearer auth as /mcp; /health and /ready stay open for probes.
	mux.Handle("/status", MCPAuthMiddleware(cfg.HTTPBearerToken, StatusHandler(dbReadyCheck, cfg.WebhookAttemptTimeout, func() *reposync.SyncQueue {
		return syncQueue
	})))

	if deps.WikiAPI != nil && deps.WikiStatic != nil {
		mux.Handle("/wiki/api/", MCPAuthMiddleware(cfg.HTTPBearerToken, WithHTTPTraceContext(deps.WikiAPI)))
		mux.Handle("/wiki/", deps.WikiStatic)
		mux.HandleFunc("/wiki", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/wiki/", http.StatusMovedPermanently)
		})
		logger.Info("wiki endpoint registered", "path", "/wiki", "dir", cfg.WikiDir)
	}

	if deps.Webhook != nil {
		mux.Handle("/webhook", deps.Webhook)
		logger.Info("webhook endpoint registered", "path", "/webhook", "allowedRepos", cfg.AllowRepo)
	}

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("HTTP server goroutine panicked", "panic", r)
				errCh <- fmt.Errorf("HTTP server panicked: %v", r)
			}
		}()
		err := httpServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return trace.Wrap(err, "HTTP server")
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutting down HTTP server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.WebhookShutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return trace.Wrap(err, "HTTP server shutdown")
		}
		cleanupSyncQueue()
		return nil
	}
}

// @intent guard optional runtime cleanup so signal, listener, and deferred paths may safely converge.
func onceCleanup(cleanup func()) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			if cleanup != nil {
				cleanup()
			}
		})
	}
}

// ValidateHTTPExposure ensures non-loopback streamable-http requires authentication.
// @intent 외부 바인딩된 HTTP MCP 서버가 인증 없이 노출되는 구성을 사전에 차단한다.
// @domainRule loopback이 아닌 주소는 bearer token 또는 insecure override가 필요하다.
func ValidateHTTPExposure(cfg Config) error {
	if cfg.Transport != "streamable-http" {
		return nil
	}
	if cfg.InsecureHTTP {
		return nil
	}
	if IsLoopbackHTTPAddr(cfg.HTTPAddr) {
		return nil
	}
	if cfg.HTTPBearerToken == "" {
		return fmt.Errorf("non-loopback streamable-http requires --http-bearer-token or --insecure-http")
	}
	return nil
}

// MCPAuthMiddleware provides bearer token authentication for MCP HTTP endpoints.
// @intent /mcp 요청에 선택적 bearer 인증을 적용해 외부 접근을 제한한다.
// @domainRule token이 비어 있으면 인증을 강제하지 않는다.
func MCPAuthMiddleware(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ValidateBearerToken(r.Header.Get("Authorization"), token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// WithHTTPTraceContext injects HTTP trace data into request context.
// @intent inbound traceparent를 MCP 요청 컨텍스트에 주입해 downstream 로그 상관관계를 유지한다.
// @sideEffect 요청 컨텍스트를 추출한 trace 정보로 교체한다.
func WithHTTPTraceContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := ccgobs.ContextWithHTTPTrace(r.Context(), r.Header)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ValidateBearerToken validates a bearer token against an expected value.
// @intent Authorization 헤더가 기대한 bearer 토큰과 정확히 일치하는지만 판단한다.
// @domainRule 접두사나 길이가 다르면 constant-time 비교 전에 실패 처리한다.
func ValidateBearerToken(header, expected string) bool {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || header[:len(prefix)] != prefix {
		return false
	}
	token := header[len(prefix):]
	if len(token) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

// IsLoopbackHTTPAddr checks if an address is a loopback address.
// @intent HTTP listen 주소가 로컬 테스트 전용인지 판별해 보안 규칙에 재사용한다.
func IsLoopbackHTTPAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// HandleHealth responds to HTTP health checks.
// @intent 가장 가벼운 liveness probe로 프로세스 응답 가능 여부만 반환한다.
// @sideEffect JSON 응답을 기록한다.
func HandleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, err := w.Write([]byte(`{"status":"ok"}`))
	if err != nil {
		slog.Error("health check write failed", "error", err)
	}
}

// ReadyHandler handles HTTP ready checks.
// @intent 호출자가 제공한 readiness 조건을 HTTP probe 응답으로 변환한다.
// @sideEffect ready 또는 not_ready JSON 응답을 기록한다.
func ReadyHandler(check func(*http.Request) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := check(r); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, writeErr := w.Write([]byte(`{"status":"not_ready"}`)); writeErr != nil {
				slog.Error("ready check write failed", "error", writeErr)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"status":"ready"}`)); err != nil {
			slog.Error("ready check write failed", "error", err)
		}
	})
}

// statusResponse defines the response structure for the status endpoint.
// @intent /status가 DB와 webhook 상태를 한 payload로 반환하게 한다.
type statusResponse struct {
	Status  string                   `json:"status"`
	DB      string                   `json:"db"`
	Webhook *reposync.SyncQueueStats `json:"webhook,omitempty"`
}

// StatusHandler provides detailed system status including DB and webhook state.
// @intent 운영 진단용 상태를 종합해 HTTP 상태 코드와 JSON payload로 노출한다.
// @sideEffect DB 상태와 webhook 큐 상태를 읽고 JSON 응답을 기록한다.
func StatusHandler(dbCheck func(*http.Request) error, webhookTimeout time.Duration, queue func() *reposync.SyncQueue) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		resp := statusResponse{Status: "ok", DB: "ready"}
		code := http.StatusOK
		if err := dbCheck(r); err != nil {
			resp.Status = "not_ready"
			resp.DB = "not_ready"
			code = http.StatusServiceUnavailable
		}
		if queue != nil {
			if q := queue(); q != nil {
				stats := q.Stats()
				resp.Webhook = &stats
				if err := WebhookStatsBlockingReady(stats, webhookTimeout); err != nil {
					resp.Status = "not_ready"
					code = http.StatusServiceUnavailable
				} else if code == http.StatusOK && WebhookStatsDegraded(stats) {
					resp.Status = "degraded"
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			slog.Error("status check write failed", "error", err)
		}
	})
}

// WebhookBlockingReadyCheck checks if the webhook queue is blocked.
// @intent readiness 판단에서 웹훅 큐가 트래픽 차단 상태인지 빠르게 판정한다.
func WebhookBlockingReadyCheck(q *reposync.SyncQueue, timeout time.Duration) error {
	if q == nil {
		return nil
	}
	return WebhookStatsBlockingReady(q.Stats(), timeout)
}

// WebhookStatsBlockingReady checks if webhook stats indicate a blocked state.
// @intent 큐 포화나 장시간 지연이 readiness 실패 조건인지 공통 규칙으로 판단한다.
// @domainRule tracked_repos가 max_tracked_repos에 도달하면 not_ready로 본다.
func WebhookStatsBlockingReady(stats reposync.SyncQueueStats, timeout time.Duration) error {
	if stats.MaxTrackedRepos > 0 && stats.TrackedRepos >= stats.MaxTrackedRepos {
		return fmt.Errorf("webhook sync queue full")
	}
	if timeout > 0 {
		if stats.OldestQueuedAge > timeout {
			return fmt.Errorf("webhook sync queue delayed for %s", stats.OldestQueuedAge)
		}
		if stats.OldestProcessingAge > timeout {
			return fmt.Errorf("webhook sync processing delayed for %s", stats.OldestProcessingAge)
		}
	}
	return nil
}

// WebhookStatsDegraded checks if webhook stats indicate a degraded state.
// @intent 최근 성공보다 최신 실패가 남아 있는 큐 상태를 degraded로 분류한다.
func WebhookStatsDegraded(stats reposync.SyncQueueStats) bool {
	if !stats.LastErrorTime.IsZero() && (stats.LastSuccessTime.IsZero() || stats.LastSuccessTime.Before(stats.LastErrorTime)) {
		return true
	}
	for _, repo := range stats.RecentRepos {
		if WebhookRepoStatsDegraded(repo) {
			return true
		}
	}
	return false
}

// WebhookRepoStatsDegraded checks if a specific repo's stats indicate a degraded state.
// @intent 저장소별 최근 실패가 아직 성공으로 덮이지 않았는지 판정한다.
func WebhookRepoStatsDegraded(stats reposync.RepoStats) bool {
	return !stats.LastErrorTime.IsZero() && (stats.LastSuccessTime.IsZero() || stats.LastSuccessTime.Before(stats.LastErrorTime))
}
