package observability

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestAttributesFromContext(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
	})

	ctx, span := provider.Tracer("test").Start(context.Background(), "span")
	defer span.End()

	attrs := AttributesFromContext(ctx)
	require.Len(t, attrs, 2)

	spanContext := trace.SpanContextFromContext(ctx)
	assert.Equal(t, spanContext.TraceID().String(), attrs[0].Value.AsString())
	assert.Equal(t, spanContext.SpanID().String(), attrs[1].Value.AsString())
}

func TestWithTraceContext(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
	})

	ctx, span := provider.Tracer("test").Start(context.Background(), "span")
	defer span.End()

	msg := withTraceContext(ctx, "hello world")
	spanContext := trace.SpanContextFromContext(ctx)

	assert.NotEqual(t, "hello world", msg)
	assert.True(t, strings.Contains(msg, spanContext.TraceID().String()))
	assert.True(t, strings.Contains(msg, spanContext.SpanID().String()))
}
