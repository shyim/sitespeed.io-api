package handler

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/shyim/sitespeed-api/internal/models"
	"github.com/shyim/sitespeed-api/internal/observability"
	"github.com/shyim/sitespeed-api/internal/runner"
	"github.com/shyim/sitespeed-api/internal/storage"
	"github.com/shyim/sitespeed-api/internal/utils"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

type Handler struct {
	storage *storage.Service
	runner  runner.Runner
}

func NewHandler(storage *storage.Service, r runner.Runner) *Handler {
	return &Handler{storage: storage, runner: r}
}

func (h *Handler) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api") {
			authToken := os.Getenv("AUTH_TOKEN")
			if authToken != "" {
				authHeader := r.Header.Get("Authorization")
				if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") || authHeader[7:] != authToken {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) HandleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) HandleAnalyze(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, span := observability.Tracer("handler").Start(r.Context(), "handler.HandleAnalyze")
	defer span.End()

	span.SetAttributes(attribute.String("analysis.id", id))

	if id == "" || strings.Contains(id, "..") || strings.Contains(id, "/") || strings.Contains(id, "\\") {
		span.SetStatus(codes.Error, "invalid analysis id")
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	var req models.ApiAnalyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "invalid request body")
		http.Error(w, "Invalid Request Body", http.StatusBadRequest)
		return
	}
	span.SetAttributes(attribute.Int("analysis.url_count", len(req.URLs)))

	if len(req.URLs) == 0 || len(req.URLs) > 5 {
		span.SetStatus(codes.Error, "invalid url count")
		renderError(w, "URLs must be between 1 and 5 items", nil, http.StatusBadRequest)
		return
	}

	for _, u := range req.URLs {
		if _, err := url.ParseRequestURI(u); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "invalid request url")
			renderError(w, fmt.Sprintf("Invalid URL: %s", u), nil, http.StatusBadRequest)
			return
		}
	}

	observability.Printf(ctx, "Starting sitespeed analysis for %s with URLs: %v", id, req.URLs)

	resultDir, err := h.runner.RunAnalysis(ctx, id, req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "sitespeed analysis failed")
		observability.Errorf(ctx, "Sitespeed failed: %v", err)
		renderError(w, "Failed to run sitespeed analysis", awsString(err.Error()), http.StatusInternalServerError)
		return
	}
	defer removeAllQuietly(resultDir)

	observability.Printf(ctx, "Sitespeed analysis completed for %s", id)

	tempPath := os.TempDir()

	pagesDir := filepath.Join(resultDir, "pages")
	pages, err := os.ReadDir(pagesDir)
	if err != nil || len(pages) == 0 {
		if err != nil {
			span.RecordError(err)
		}
		span.SetStatus(codes.Error, "web vital data not found")
		renderError(w, "Web vital data not found", nil, http.StatusInternalServerError)
		return
	}

	// Find the first directory in pages
	var firstPage string
	for _, p := range pages {
		if p.IsDir() {
			firstPage = p.Name()
			break
		}
	}
	if firstPage == "" {
		span.SetStatus(codes.Error, "web vital page directory missing")
		renderError(w, "Web vital data not found", nil, http.StatusInternalServerError)
		return
	}

	webvitalDataPath := filepath.Join(resultDir, "data", "browsertime.summary-total.json")
	pagexrayDataPath := filepath.Join(resultDir, "data", "pagexray.summary-total.json")

	browsertimeFile, err := os.Open(webvitalDataPath)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "web vital data missing")
		renderError(w, "Web vital data not found", nil, http.StatusInternalServerError)
		return
	}
	defer closeQuietly(browsertimeFile)

	var browsertimeData models.BrowserTime
	if err := json.NewDecoder(browsertimeFile).Decode(&browsertimeData); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to parse web vital data")
		renderError(w, "Failed to parse web vital data", nil, http.StatusInternalServerError)
		return
	}

	var pagexrayData models.PageXray
	if pagexrayFile, err := os.Open(pagexrayDataPath); err == nil {
		defer closeQuietly(pagexrayFile)
		if err := json.NewDecoder(pagexrayFile).Decode(&pagexrayData); err != nil {
			span.RecordError(err)
			observability.Errorf(ctx, "Failed to parse pagexray data: %v", err)
		}
	}

	screenshotPath := filepath.Join(resultDir, "pages", firstPage, "data", "screenshots", "1", "afterPageCompleteCheck.png")
	s3ScreenshotPath := fmt.Sprintf("results/%s/screenshot.png", id)

	if _, err := os.Stat(screenshotPath); err == nil {
		if err := h.storage.UploadFile(ctx, s3ScreenshotPath, screenshotPath); err != nil {
			span.RecordError(err)
			observability.Errorf(ctx, "Failed to upload screenshot: %v", err)
		}
	}

	zipPath := filepath.Join(tempPath, fmt.Sprintf("%s.zip", id))
	removeQuietly(zipPath) // Ensure it doesn't exist

	if err := utils.ZipDirectory(resultDir, zipPath); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to create result zip")
		renderError(w, "Failed to create zip", awsString(err.Error()), http.StatusInternalServerError)
		return
	}
	defer removeQuietly(zipPath)

	if err := h.storage.UploadFile(ctx, fmt.Sprintf("results/%s/result.zip", id), zipPath); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to upload result zip")
		renderError(w, "Failed to upload zip", awsString(err.Error()), http.StatusInternalServerError)
		return
	}

	// Clean cache if exists
	cacheZipPath := filepath.Join(tempPath, "sitespeed-cache", fmt.Sprintf("%s.zip", id))
	removeQuietly(cacheZipPath)

	resp := models.AnalyzeResponse{}
	if browsertimeData.GoogleWebVitals != nil {
		if browsertimeData.GoogleWebVitals.Ttfb != nil {
			resp.Ttfb = browsertimeData.GoogleWebVitals.Ttfb.Median
		}
		if browsertimeData.GoogleWebVitals.LargestContentfulPaint != nil {
			resp.LargestContentfulPaint = browsertimeData.GoogleWebVitals.LargestContentfulPaint.Median
		}
		if browsertimeData.GoogleWebVitals.FirstContentfulPaint != nil {
			resp.FirstContentfulPaint = browsertimeData.GoogleWebVitals.FirstContentfulPaint.Median
		}
		if browsertimeData.GoogleWebVitals.CumulativeLayoutShift != nil {
			resp.CumulativeLayoutShift = browsertimeData.GoogleWebVitals.CumulativeLayoutShift.Median
		}
	}
	if browsertimeData.Timings != nil && browsertimeData.Timings.FullyLoaded != nil {
		resp.FullyLoaded = browsertimeData.Timings.FullyLoaded.Median
	}
	if pagexrayData.TransferSize != nil {
		resp.TransferSize = pagexrayData.TransferSize.Median
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to write response")
	}
}

func (h *Handler) HandleGetResult(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	path := r.PathValue("path")
	ctx, span := observability.Tracer("handler").Start(r.Context(), "handler.HandleGetResult")
	defer span.End()

	span.SetAttributes(attribute.String("analysis.id", id))

	if id == "" || strings.Contains(id, "..") || strings.Contains(id, "/") || strings.Contains(id, "\\") {
		span.SetStatus(codes.Error, "invalid analysis id")
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	tempPath := os.TempDir()
	zipPath := filepath.Join(tempPath, "sitespeed-cache", fmt.Sprintf("%s.zip", id))

	if _, err := os.Stat(zipPath); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(zipPath), 0755); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to create cache dir")
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		err := h.storage.DownloadFile(ctx, fmt.Sprintf("results/%s/result.zip", id), zipPath)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "failed to download result zip")
			http.NotFound(w, r)
			return
		}
	}

	if path == "" {
		path = "index.html"
	}
	path = strings.ReplaceAll(path, "\\", "/")

	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to open result zip")
		http.NotFound(w, r)
		return
	}
	defer closeQuietly(archive)

	var file *zip.File
	for _, f := range archive.File {
		if f.Name == path {
			file = f
			break
		}
	}

	if file == nil && !strings.HasSuffix(path, "/") {
		// Try adding index.html
		indexPath := path + "/index.html"
		for _, f := range archive.File {
			if f.Name == indexPath {
				file = f
				break
			}
		}
	}

	if file == nil {
		http.NotFound(w, r)
		return
	}

	rc, err := file.Open()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to open result file")
		http.Error(w, "Failed to open file", http.StatusInternalServerError)
		return
	}
	defer closeQuietly(rc)

	contentType := mime.TypeByExtension(filepath.Ext(file.Name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Header().Set("Last-Modified", file.Modified.UTC().Format(http.TimeFormat))

	if _, err := io.Copy(w, rc); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to stream result file")
	}
}

func (h *Handler) HandleDeleteResult(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, span := observability.Tracer("handler").Start(r.Context(), "handler.HandleDeleteResult")
	defer span.End()

	span.SetAttributes(attribute.String("analysis.id", id))
	if id == "" || strings.Contains(id, "..") || strings.Contains(id, "/") || strings.Contains(id, "\\") {
		span.SetStatus(codes.Error, "invalid analysis id")
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	if err := h.storage.DeleteFile(ctx, fmt.Sprintf("results/%s/result.zip", id)); err != nil {
		span.RecordError(err)
		observability.Errorf(ctx, "Failed to delete result zip for %s: %v", id, err)
	}
	if err := h.storage.DeleteFile(ctx, fmt.Sprintf("results/%s/screenshot.png", id)); err != nil {
		span.RecordError(err)
		observability.Errorf(ctx, "Failed to delete screenshot for %s: %v", id, err)
	}

	tempPath := os.TempDir()
	zipPath := filepath.Join(tempPath, "sitespeed-cache", fmt.Sprintf("%s.zip", id))
	removeQuietly(zipPath)

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) HandleGetScreenshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ctx, span := observability.Tracer("handler").Start(r.Context(), "handler.HandleGetScreenshot")
	defer span.End()

	span.SetAttributes(attribute.String("analysis.id", id))
	if id == "" || strings.Contains(id, "..") || strings.Contains(id, "/") || strings.Contains(id, "\\") {
		span.SetStatus(codes.Error, "invalid analysis id")
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	stream, _, lastModified, etag, err := h.storage.GetFile(ctx, fmt.Sprintf("results/%s/screenshot.png", id))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to get screenshot")
		http.NotFound(w, r)
		return
	}
	defer closeQuietly(stream)

	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Header().Set("Content-Type", "image/png")
	if etag != nil {
		w.Header().Set("ETag", *etag)
	}
	if lastModified != nil {
		w.Header().Set("Last-Modified", lastModified.UTC().Format(http.TimeFormat))
	}

	if _, err := io.Copy(w, stream); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to stream screenshot")
	}
}

func renderError(w http.ResponseWriter, msg string, details *string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(models.ErrorResponse{
		Error:   msg,
		Details: details,
	})
}

func awsString(v string) *string {
	return &v
}

func closeQuietly(c io.Closer) {
	if c != nil {
		_ = c.Close()
	}
}

func removeQuietly(path string) {
	_ = os.Remove(path)
}

func removeAllQuietly(path string) {
	_ = os.RemoveAll(path)
}
