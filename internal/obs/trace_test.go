package obs

import (
	"context"
	"net/http"
	"testing"

	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestServerSpan_ExtractsTraceparent(t *testing.T) {
	tel, err := Setup(context.Background(), Config{ServiceName: "ccg-test", Mode: "test"})
	if err != nil {
		t.Fatalf("setup telemetry: %v", err)
	}
	SetGlobal(tel)
	t.Cleanup(func() {
		_ = tel.Shutdown(context.Background())
		SetGlobal(nil)
	})

	header := http.Header{}
	header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	ctx, span := ServerSpan(context.Background(), "incoming", header)
	defer span.End()
	sc := oteltrace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		t.Fatal("expected valid span context")
	}
	if got := sc.TraceID().String(); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace id = %q", got)
	}
}

func TestStartChildSpan_PreservesTraceAndChangesSpan(t *testing.T) {
	tel, err := Setup(context.Background(), Config{ServiceName: "ccg-test", Mode: "test"})
	if err != nil {
		t.Fatalf("setup telemetry: %v", err)
	}
	SetGlobal(tel)
	t.Cleanup(func() {
		_ = tel.Shutdown(context.Background())
		SetGlobal(nil)
	})

	ctx, parent := StartSpan(context.Background(), "parent")
	defer parent.End()
	parentSC := parent.SpanContext()
	childCtx, child := StartChildSpan(ctx, "child")
	defer child.End()
	childSC := oteltrace.SpanContextFromContext(childCtx)
	if childSC.TraceID() != parentSC.TraceID() {
		t.Fatal("expected same trace id")
	}
	if childSC.SpanID() == parentSC.SpanID() {
		t.Fatal("expected different span id")
	}
}
