package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/shyim/sitespeed-api/internal/cleanup"
	"github.com/shyim/sitespeed-api/internal/docker"
	"github.com/shyim/sitespeed-api/internal/handler"
	"github.com/shyim/sitespeed-api/internal/runner"
	"github.com/shyim/sitespeed-api/internal/storage"
)

func main() {
	ctx := context.Background()

	// Initialize Sentry if DSN is configured
	if dsn := os.Getenv("SENTRY_DSN"); dsn != "" {
		err := sentry.Init(sentry.ClientOptions{
			Dsn:              dsn,
			EnableTracing:    true,
			TracesSampleRate: 1.0,
		})
		if err != nil {
			log.Fatalf("Failed to initialize Sentry: %v", err)
		}
		defer sentry.Flush(2 * time.Second)
		log.Println("Sentry initialized")
	}

	storageService, err := storage.NewService(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize storage service: %v", err)
	}

	r, err := createRunner(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize runner: %v", err)
	}
	defer r.Close()

	h := handler.NewHandler(storageService, r)

	// Start background cleanup
	cleanup.Start(r)

	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/result/{id}", h.HandleAnalyze)
	mux.HandleFunc("DELETE /api/result/{id}", h.HandleDeleteResult)
	mux.HandleFunc("GET /result/{id}/{path...}", h.HandleGetResult)

	mux.HandleFunc("GET /screenshot/{id}", h.HandleGetScreenshot)

	finalHandler := h.AuthMiddleware(mux)
	finalHandler = recoverMiddleware(finalHandler)
	finalHandler = loggingMiddleware(finalHandler)

	log.Println("Server starting on port 8080")
	if err := http.ListenAndServe(":8080", finalHandler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func createRunner(ctx context.Context) (runner.Runner, error) {
	runnerType := os.Getenv("RUNNER_TYPE")

	switch runnerType {
	case "kubernetes":
		log.Println("Using Kubernetes runner")
		return createKubernetesRunner(ctx)
	default:
		log.Println("Using Docker runner")
		return createDockerRunner(ctx)
	}
}

func createDockerRunner(ctx context.Context) (runner.Runner, error) {
	r, err := docker.NewRunner()
	if err != nil {
		return nil, err
	}

	if err := r.EnsureImage(ctx); err != nil {
		r.Close()
		return nil, err
	}

	return r, nil
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("Started %s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
		log.Printf("Completed %s %s in %v", r.Method, r.URL.Path, time.Since(start))
	})
}

func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("Panic: %v", err)
				if hub := sentry.GetHubFromContext(r.Context()); hub != nil {
					hub.RecoverWithContext(r.Context(), err)
				} else {
					sentry.CurrentHub().RecoverWithContext(r.Context(), err)
				}
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
