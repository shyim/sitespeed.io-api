package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/shyim/sitespeed-api/internal/cleanup"
	"github.com/shyim/sitespeed-api/internal/handler"
	"github.com/shyim/sitespeed-api/internal/storage"
)

func main() {
	ctx := context.Background()

	storageService, err := storage.NewService(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize storage service: %v", err)
	}

	h := handler.NewHandler(storageService)

	// Start background cleanup
	cleanup.Start()

	mux := http.NewServeMux()

	// Register routes with Go 1.22+ patterns
	mux.HandleFunc("POST /api/result/{id}", h.HandleAnalyze)
	mux.HandleFunc("DELETE /api/result/{id}", h.HandleDeleteResult)
	
	// Wildcard matching for results
	mux.HandleFunc("GET /result/{id}/{path...}", h.HandleGetResult)
	
	mux.HandleFunc("GET /screenshot/{id}", h.HandleGetScreenshot)

	// Apply middleware: Logger -> Recoverer -> Auth -> Mux
	// Note: AuthMiddleware in the handler only checks /api paths, so wrapping the whole mux is fine.
	
	finalHandler := h.AuthMiddleware(mux)
	finalHandler = recoverMiddleware(finalHandler)
	finalHandler = loggingMiddleware(finalHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, finalHandler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		// Wrap ResponseWriter to capture status code could be added here, 
		// but keeping it simple for now.
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
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}