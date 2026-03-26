package observability

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestWithTracePrefix(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
	})

	ctx, span := provider.Tracer("test").Start(context.Background(), "span")
	defer span.End()

	msg := withTracePrefix(ctx, "hello world")
	spanContext := trace.SpanContextFromContext(ctx)

	assert.NotEqual(t, "hello world", msg)
	assert.True(t, strings.Contains(msg, spanContext.TraceID().String()))
	assert.True(t, strings.Contains(msg, spanContext.SpanID().String()))
}
