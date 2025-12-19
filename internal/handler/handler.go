package handler

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/shyim/sitespeed-api/internal/models"
	"github.com/shyim/sitespeed-api/internal/storage"
	"github.com/shyim/sitespeed-api/internal/utils"
)

type Handler struct {
	storage *storage.Service
}

func NewHandler(storage *storage.Service) *Handler {
	return &Handler{storage: storage}
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

func (h *Handler) HandleAnalyze(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || strings.Contains(id, "..") || strings.Contains(id, "/") || strings.Contains(id, "\\") {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	var req models.ApiAnalyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid Request Body", http.StatusBadRequest)
		return
	}

	if len(req.URLs) == 0 || len(req.URLs) > 5 {
		renderError(w, "URLs must be between 1 and 5 items", nil, http.StatusBadRequest)
		return
	}

	for _, u := range req.URLs {
		if _, err := url.ParseRequestURI(u); err != nil {
			renderError(w, fmt.Sprintf("Invalid URL: %s", u), nil, http.StatusBadRequest)
			return
		}
	}

	tempPath := os.TempDir()
	resultDir := filepath.Join(tempPath, "sitespeed", id)

	if err := os.RemoveAll(resultDir); err != nil {
		log.Printf("Failed to clean result dir: %v", err)
	}
	if err := os.MkdirAll(resultDir, 0755); err != nil {
		renderError(w, "Failed to create directory", nil, http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(resultDir)

	log.Printf("Starting sitespeed analysis for %s with URLs: %v", id, req.URLs)

	sitespeedBin := os.Getenv("SITESPEED_BIN")
	if sitespeedBin == "" {
		sitespeedBin = "sitespeed.io"
	}

	args := []string{
		sitespeedBin,
		"--outputFolder", resultDir,
		"--plugins.add", "analysisstorer",
		"--visualMetrics",
		"--video",
		"--viewPort", "1920x1080",
		"--browsertime.chrome.cleanUserDataDir=true",
		"--browsertime.iterations", "1",
	}
	args = append(args, req.URLs...)

	cmd := exec.Command("node", args...)

	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		log.Printf("Sitespeed failed: %s", stderr.String())
		renderError(w, "Failed to run sitespeed analysis", awsString(stderr.String()), http.StatusInternalServerError)
		return
	}

	log.Printf("Sitespeed analysis completed for shop %s", id)

	pagesDir := filepath.Join(resultDir, "pages")
	pages, err := os.ReadDir(pagesDir)
	if err != nil || len(pages) == 0 {
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
		renderError(w, "Web vital data not found", nil, http.StatusInternalServerError)
		return
	}

	webvitalDataPath := filepath.Join(resultDir, "data", "browsertime.summary-total.json")
	pagexrayDataPath := filepath.Join(resultDir, "data", "pagexray.summary-total.json")

	browsertimeFile, err := os.Open(webvitalDataPath)
	if err != nil {
		renderError(w, "Web vital data not found", nil, http.StatusInternalServerError)
		return
	}
	defer browsertimeFile.Close()

	var browsertimeData models.BrowserTime
	if err := json.NewDecoder(browsertimeFile).Decode(&browsertimeData); err != nil {
		renderError(w, "Failed to parse web vital data", nil, http.StatusInternalServerError)
		return
	}

	var pagexrayData models.PageXray
	if pagexrayFile, err := os.Open(pagexrayDataPath); err == nil {
		defer pagexrayFile.Close()
		json.NewDecoder(pagexrayFile).Decode(&pagexrayData)
	}

	screenshotPath := filepath.Join(resultDir, "pages", firstPage, "data", "screenshots", "1", "afterPageCompleteCheck.png")
	s3ScreenshotPath := fmt.Sprintf("results/%s/screenshot.png", id)

	if _, err := os.Stat(screenshotPath); err == nil {
		if err := h.storage.UploadFile(r.Context(), s3ScreenshotPath, screenshotPath); err != nil {
			log.Printf("Failed to upload screenshot: %v", err)
		}
	}

	zipPath := filepath.Join(tempPath, fmt.Sprintf("%s.zip", id))
	os.Remove(zipPath) // Ensure it doesn't exist

	if err := utils.ZipDirectory(resultDir, zipPath); err != nil {
		renderError(w, "Failed to create zip", awsString(err.Error()), http.StatusInternalServerError)
		return
	}
	defer os.Remove(zipPath)

	if err := h.storage.UploadFile(r.Context(), fmt.Sprintf("results/%s/result.zip", id), zipPath); err != nil {
		renderError(w, "Failed to upload zip", awsString(err.Error()), http.StatusInternalServerError)
		return
	}

	// Clean cache if exists
	cacheZipPath := filepath.Join(tempPath, "sitespeed-cache", fmt.Sprintf("%s.zip", id))
	os.Remove(cacheZipPath)

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
	json.NewEncoder(w).Encode(resp)
}

func (h *Handler) HandleGetResult(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	path := r.PathValue("path")

	if id == "" || strings.Contains(id, "..") || strings.Contains(id, "/") || strings.Contains(id, "\\") {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	tempPath := os.TempDir()
	zipPath := filepath.Join(tempPath, "sitespeed-cache", fmt.Sprintf("%s.zip", id))

	if _, err := os.Stat(zipPath); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(zipPath), 0755); err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		err := h.storage.DownloadFile(r.Context(), fmt.Sprintf("results/%s/result.zip", id), zipPath)
		if err != nil {
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
		http.NotFound(w, r)
		return
	}
	defer archive.Close()

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
		http.Error(w, "Failed to open file", http.StatusInternalServerError)
		return
	}
	defer rc.Close()

	contentType := mime.TypeByExtension(filepath.Ext(file.Name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Header().Set("Last-Modified", file.Modified.UTC().Format(http.TimeFormat))

	io.Copy(w, rc)
}

func (h *Handler) HandleDeleteResult(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || strings.Contains(id, "..") || strings.Contains(id, "/") || strings.Contains(id, "\\") {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	h.storage.DeleteFile(r.Context(), fmt.Sprintf("results/%s/result.zip", id))
	h.storage.DeleteFile(r.Context(), fmt.Sprintf("results/%s/screenshot.png", id))

	tempPath := os.TempDir()
	zipPath := filepath.Join(tempPath, "sitespeed-cache", fmt.Sprintf("%s.zip", id))
	os.Remove(zipPath)

	w.WriteHeader(http.StatusOK)
}

func (h *Handler) HandleGetScreenshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || strings.Contains(id, "..") || strings.Contains(id, "/") || strings.Contains(id, "\\") {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	stream, _, lastModified, etag, err := h.storage.GetFile(r.Context(), fmt.Sprintf("results/%s/screenshot.png", id))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer stream.Close()

	w.Header().Set("Cache-Control", "public, max-age=604800")
	w.Header().Set("Content-Type", "image/png")
	if etag != nil {
		w.Header().Set("ETag", *etag)
	}
	if lastModified != nil {
		w.Header().Set("Last-Modified", lastModified.UTC().Format(http.TimeFormat))
	}

	io.Copy(w, stream)
}

func renderError(w http.ResponseWriter, msg string, details *string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(models.ErrorResponse{
		Error:   msg,
		Details: details,
	})
}

func awsString(v string) *string {
	return &v
}
