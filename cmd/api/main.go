package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/shyim/sitespeed-api/internal/cleanup"
	"github.com/shyim/sitespeed-api/internal/docker"
	"github.com/shyim/sitespeed-api/internal/handler"
	"github.com/shyim/sitespeed-api/internal/observability"
	"github.com/shyim/sitespeed-api/internal/runner"
	"github.com/shyim/sitespeed-api/internal/storage"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	ctx := context.Background()

	shutdownObservability, err := observability.Setup(ctx)
	if err != nil {
		slog.Error("Failed to initialize OpenTelemetry", "error", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownObservability(shutdownCtx); err != nil {
			slog.Error("Failed to shutdown OpenTelemetry", "error", err)
		}
	}()

	storageService, err := storage.NewService(ctx)
	if err != nil {
		slog.Error("Failed to initialize storage service", "error", err)
		os.Exit(1)
	}

	r, err := createRunner(ctx)
	if err != nil {
		slog.Error("Failed to initialize runner", "error", err)
		os.Exit(1)
	}
	defer func() {
		_ = r.Close()
	}()

	h := handler.NewHandler(storageService, r)

	// Start background cleanup
	cleanup.Start(r)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", h.HandleHealth)
	mux.HandleFunc("POST /api/result/{id}", h.HandleAnalyze)
	mux.HandleFunc("DELETE /api/result/{id}", h.HandleDeleteResult)
	mux.HandleFunc("GET /result/{id}/{path...}", h.HandleGetResult)

	mux.HandleFunc("GET /screenshot/{id}", h.HandleGetScreenshot)

	finalHandler := h.AuthMiddleware(mux)
	finalHandler = recoverMiddleware(finalHandler)
	finalHandler = loggingMiddleware(finalHandler)
	finalHandler = otelhttp.NewHandler(finalHandler, "http.server",
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			pattern := r.Pattern
			if pattern == "" {
				pattern = r.URL.Path
			}
			return fmt.Sprintf("%s %s", r.Method, pattern)
		}),
	)

	server := &http.Server{
		Addr:    ":8080",
		Handler: finalHandler,
	}

	// Graceful shutdown on SIGINT/SIGTERM
	shutdownCh := make(chan os.Signal, 1)
	signal.Notify(shutdownCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("Server starting", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server failed", "error", err)
			os.Exit(1)
		}
	}()

	sig := <-shutdownCh
	slog.Info("Shutdown signal received", "signal", sig.String())

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Server shutdown failed", "error", err)
		os.Exit(1)
	}
	slog.Info("Server stopped gracefully")
}

func createRunner(ctx context.Context) (runner.Runner, error) {
	runnerType := os.Getenv("RUNNER_TYPE")

	switch runnerType {
	case "kubernetes":
		observability.Printf(ctx, "Using Kubernetes runner")
		return createKubernetesRunner(ctx)
	default:
		observability.Printf(ctx, "Using Docker runner")
		return createDockerRunner(ctx)
	}
}

func createDockerRunner(ctx context.Context) (runner.Runner, error) {
	r, err := docker.NewRunner()
	if err != nil {
		return nil, err
	}

	if err := r.EnsureImage(ctx); err != nil {
		_ = r.Close()
		return nil, err
	}

	return r, nil
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		observability.Printf(r.Context(), "Started %s %s", r.Method, r.URL.Path)
		next.ServeHTTP(recorder, r)
		observability.Printf(r.Context(), "Completed %s %s status=%d duration=%v", r.Method, r.URL.Path, recorder.statusCode, time.Since(start))
	})
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				observability.Errorf(r.Context(), "panic: %v", err)
				span := trace.SpanFromContext(r.Context())
				if span.SpanContext().IsValid() {
					span.RecordError(fmt.Errorf("panic: %v", err))
					span.SetStatus(codes.Error, "panic")
				}
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}
