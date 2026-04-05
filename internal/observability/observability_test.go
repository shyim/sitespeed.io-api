package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestTraceHandlerAddsTraceContext(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
	})

	var buf bytes.Buffer
	handler := newTraceHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: replaceDatadogAttr,
	}))
	logger := slog.New(handler)

	ctx, span := provider.Tracer("test").Start(context.Background(), "span")
	defer span.End()

	logger.InfoContext(ctx, "hello world")

	output := buf.Bytes()
	spanCtx := trace.SpanContextFromContext(ctx)
	var record map[string]any
	assert.NoError(t, json.Unmarshal(output, &record))

	assert.Equal(t, "hello world", record["message"])
	assert.Equal(t, "info", record["status"])
	assert.Equal(t, spanCtx.TraceID().String(), record["trace_id"])
	assert.Equal(t, spanCtx.SpanID().String(), record["span_id"])
	assert.Contains(t, record, "timestamp")
}

func TestTraceHandlerWithoutTrace(t *testing.T) {
	var buf bytes.Buffer
	handler := newTraceHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: replaceDatadogAttr,
	}))
	logger := slog.New(handler)

	logger.InfoContext(context.Background(), "no trace")

	output := buf.Bytes()
	var record map[string]any
	assert.NoError(t, json.Unmarshal(output, &record))

	assert.Equal(t, "no trace", record["message"])
	assert.Equal(t, "info", record["status"])
	assert.NotContains(t, record, "trace_id")
	assert.NotContains(t, record, "span_id")
}

func TestDatadogStatus(t *testing.T) {
	assert.Equal(t, "debug", datadogStatus(slog.AnyValue(slog.LevelDebug)))
	assert.Equal(t, "info", datadogStatus(slog.AnyValue(slog.LevelInfo)))
	assert.Equal(t, "warn", datadogStatus(slog.AnyValue(slog.LevelWarn)))
	assert.Equal(t, "error", datadogStatus(slog.AnyValue(slog.LevelError)))
}
