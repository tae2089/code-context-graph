// @index OpenTelemetry SDK tracer setup and trace-aware logging helpers for server paths.
package obs

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const defaultShutdownTimeout = 5 * time.Second

// Config describes how the shared telemetry provider should be initialized.
// @intent 서비스 이름, 버전, export endpoint 같은 텔레메트리 부트스트랩 설정을 묶는다.
type Config struct {
	ServiceName    string
	ServiceVersion string
	Mode           string
	Endpoint       string
	Logger         *slog.Logger
}

// Telemetry holds the active tracer provider and tracer used by CCG runtime code.
// @intent 서버 전역에서 재사용할 tracer provider 수명주기를 한 구조체로 묶는다.
type Telemetry struct {
	provider *sdktrace.TracerProvider
	tracer   oteltrace.Tracer
}

var (
	globalMu        sync.RWMutex
	globalTelemetry = &Telemetry{tracer: otel.Tracer("code-context-graph")}
)

// Setup builds the OpenTelemetry SDK provider and optional OTLP exporter.
// @intent endpoint 유무에 따라 local-only tracing 또는 OTLP export tracing을 초기화한다.
// @sideEffect 전역 tracer provider와 text map propagator를 설정한다.
func Setup(ctx context.Context, cfg Config) (*Telemetry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	res, err := resource.New(ctx, resource.WithAttributes(
		attribute.String("service.name", nonEmpty(cfg.ServiceName, "code-context-graph")),
		attribute.String("service.version", cfg.ServiceVersion),
		attribute.String("ccg.mode", cfg.Mode),
	))
	if err != nil {
		return nil, fmt.Errorf("build telemetry resource: %w", err)
	}

	options := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	if endpoint := strings.TrimSpace(cfg.Endpoint); endpoint != "" {
		exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint))
		if err != nil {
			return nil, fmt.Errorf("create otlp trace exporter: %w", err)
		}
		options = append(options, sdktrace.WithBatcher(exporter))
		logger(cfg.Logger).Info("OpenTelemetry trace export enabled", "endpoint", endpoint)
	}

	provider := sdktrace.NewTracerProvider(options...)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	return &Telemetry{
		provider: provider,
		tracer:   provider.Tracer(nonEmpty(cfg.ServiceName, "code-context-graph")),
	}, nil
}

// SetGlobal replaces the process-wide telemetry handle used by helper functions.
// @intent 서버 초기화 이후 어디서나 같은 tracer를 쓰도록 전역 핸들을 갱신한다.
// @sideEffect globalTelemetry를 교체한다.
func SetGlobal(t *Telemetry) {
	globalMu.Lock()
	defer globalMu.Unlock()
	if t == nil {
		globalTelemetry = &Telemetry{tracer: otel.Tracer("code-context-graph")}
		return
	}
	globalTelemetry = t
}

// Global returns the current process-wide telemetry handle.
// @intent helper 함수들이 명시적 의존성 주입 없이 현재 tracer를 가져오게 한다.
func Global() *Telemetry {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalTelemetry
}

// Shutdown flushes and stops the active tracer provider.
// @intent 서버 종료 시 export 대기 중인 span을 정리하고 provider를 닫는다.
// @sideEffect tracer provider의 종료 훅을 실행한다.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t == nil || t.provider == nil {
		return nil
	}
	if ctx == nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
		defer cancel()
		ctx = shutdownCtx
	}
	return t.provider.Shutdown(ctx)
}

// ContextWithHTTPTrace extracts inbound trace headers into the provided context.
// @intent HTTP 요청의 traceparent와 baggage를 downstream span 시작에 연결한다.
func ContextWithHTTPTrace(ctx context.Context, header http.Header) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if header == nil {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(header))
}

// ServerSpan starts a server-kind span from the current global telemetry.
// @intent inbound HTTP 요청 처리를 공통 helper 하나로 server span화한다.
func ServerSpan(ctx context.Context, name string, header http.Header, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	return Global().StartServerSpan(ctx, name, header, attrs...)
}

// StartSpan starts an internal span from the current global telemetry.
// @intent 런타임 내부 작업을 현재 trace 아래 새 span으로 감싼다.
func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	return Global().StartSpan(ctx, name, attrs...)
}

// StartChildSpan starts a child span from the current global telemetry.
// @intent 현재 컨텍스트 아래 후속 작업 span을 일관된 helper로 생성한다.
func StartChildSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	return Global().StartChildSpan(ctx, name, attrs...)
}

// StartServerSpan extracts inbound HTTP headers and starts a server-kind span.
// @intent telemetry 인스턴스에 묶인 tracer로 HTTP 진입 span을 시작한다.
func (t *Telemetry) StartServerSpan(ctx context.Context, name string, header http.Header, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	ctx = ContextWithHTTPTrace(ctx, header)
	return t.start(ctx, name, oteltrace.WithSpanKind(oteltrace.SpanKindServer), oteltrace.WithAttributes(attrs...))
}

// StartSpan starts an internal span with the telemetry instance tracer.
// @intent 개별 telemetry 인스턴스로 일반 내부 span을 생성한다.
func (t *Telemetry) StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	return t.start(ctx, name, oteltrace.WithAttributes(attrs...))
}

// StartChildSpan starts a child span with the telemetry instance tracer.
// @intent telemetry 인스턴스 기준으로 후속 작업 span을 생성한다.
func (t *Telemetry) StartChildSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	return t.start(ctx, name, oteltrace.WithAttributes(attrs...))
}

// TraceLogArgs extracts trace identifiers for structured logging.
// @intent span이 있는 컨텍스트를 slog 필드(trace_id, span_id, sampled)로 바꾼다.
func TraceLogArgs(ctx context.Context) []any {
	if ctx == nil {
		return nil
	}
	sc := oteltrace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return nil
	}
	return []any{
		"trace_id", sc.TraceID().String(),
		"span_id", sc.SpanID().String(),
		"trace_sampled", sc.IsSampled(),
	}
}

// start is the common span creation path shared by server/internal helpers.
// @intent nil-safe tracer fallback과 span 시작 옵션 적용을 한 곳으로 모은다.
func (t *Telemetry) start(ctx context.Context, name string, opts ...oteltrace.SpanStartOption) (context.Context, oteltrace.Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	if t == nil || t.tracer == nil {
		return otel.Tracer("code-context-graph").Start(ctx, name, opts...)
	}
	return t.tracer.Start(ctx, name, opts...)
}

// logger returns the configured logger or the process default.
// @intent telemetry 초기화 로그가 nil logger에서도 안전하게 남도록 한다.
func logger(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.Default()
}

// nonEmpty returns fallback when value is blank after trimming.
// @intent service name 같은 설정값이 비었을 때 안정적인 기본값을 사용하게 한다.
func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
