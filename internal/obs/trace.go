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

type Config struct {
	ServiceName    string
	ServiceVersion string
	Mode           string
	Endpoint       string
	Logger         *slog.Logger
}

type Telemetry struct {
	provider *sdktrace.TracerProvider
	tracer   oteltrace.Tracer
}

var (
	globalMu        sync.RWMutex
	globalTelemetry = &Telemetry{tracer: otel.Tracer("code-context-graph")}
)

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

func SetGlobal(t *Telemetry) {
	globalMu.Lock()
	defer globalMu.Unlock()
	if t == nil {
		globalTelemetry = &Telemetry{tracer: otel.Tracer("code-context-graph")}
		return
	}
	globalTelemetry = t
}

func Global() *Telemetry {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalTelemetry
}

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

func ContextWithHTTPTrace(ctx context.Context, header http.Header) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if header == nil {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(header))
}

func ServerSpan(ctx context.Context, name string, header http.Header, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	return Global().StartServerSpan(ctx, name, header, attrs...)
}

func StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	return Global().StartSpan(ctx, name, attrs...)
}

func StartChildSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	return Global().StartChildSpan(ctx, name, attrs...)
}

func (t *Telemetry) StartServerSpan(ctx context.Context, name string, header http.Header, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	ctx = ContextWithHTTPTrace(ctx, header)
	return t.start(ctx, name, oteltrace.WithSpanKind(oteltrace.SpanKindServer), oteltrace.WithAttributes(attrs...))
}

func (t *Telemetry) StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	return t.start(ctx, name, oteltrace.WithAttributes(attrs...))
}

func (t *Telemetry) StartChildSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	return t.start(ctx, name, oteltrace.WithAttributes(attrs...))
}

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

func (t *Telemetry) start(ctx context.Context, name string, opts ...oteltrace.SpanStartOption) (context.Context, oteltrace.Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	if t == nil || t.tracer == nil {
		return otel.Tracer("code-context-graph").Start(ctx, name, opts...)
	}
	return t.tracer.Start(ctx, name, opts...)
}

func logger(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.Default()
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
