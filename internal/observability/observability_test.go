package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestTraceAttrs(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
	})

	ctx, span := provider.Tracer("test").Start(context.Background(), "span")
	defer span.End()

	attrs := traceAttrs(ctx)
	spanContext := trace.SpanContextFromContext(ctx)

	assert.Len(t, attrs, 4)
	assert.Equal(t, "trace_id", attrs[0])
	assert.Equal(t, spanContext.TraceID().String(), attrs[1])
	assert.Equal(t, "span_id", attrs[2])
	assert.Equal(t, spanContext.SpanID().String(), attrs[3])
}

func TestTraceAttrsNoSpan(t *testing.T) {
	attrs := traceAttrs(context.Background())
	assert.Nil(t, attrs)
}
