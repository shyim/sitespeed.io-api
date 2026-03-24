package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shyim/sitespeed-api/internal/handler"
	"github.com/shyim/sitespeed-api/internal/models"
	"github.com/shyim/sitespeed-api/internal/storage"
	"github.com/shyim/sitespeed-api/internal/testhelper"
)

// mockRunner implements runner.Runner for testing.
type mockRunner struct {
	runFunc func(ctx context.Context, id string, req models.ApiAnalyzeRequest) (string, error)
}

func (m *mockRunner) RunAnalysis(ctx context.Context, id string, req models.ApiAnalyzeRequest) (string, error) {
	if m.runFunc != nil {
		return m.runFunc(ctx, id, req)
	}
	return "", fmt.Errorf("not implemented")
}

func (m *mockRunner) CleanupOrphaned(ctx context.Context) error { return nil }
func (m *mockRunner) CleanupStaleResultDirs(maxAgeMinutes int)  {}
func (m *mockRunner) Close() error                              { return nil }

// createFakeSitespeedResult creates a directory structure mimicking sitespeed.io output.
func createFakeSitespeedResult(t *testing.T, dir string) {
	t.Helper()

	// Create pages directory with a domain subdirectory
	pagesDir := filepath.Join(dir, "pages", "example_com")
	screenshotDir := filepath.Join(pagesDir, "data", "screenshots", "1")
	if err := os.MkdirAll(screenshotDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a fake screenshot
	if err := os.WriteFile(filepath.Join(screenshotDir, "afterPageCompleteCheck.png"), []byte("fake-png-data"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create data directory with browsertime and pagexray summaries
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}

	browsertime := models.BrowserTime{
		GoogleWebVitals: &models.GoogleWebVitals{
			Ttfb:                   &models.Metric{Median: 123.45},
			LargestContentfulPaint: &models.Metric{Median: 1200.5},
			FirstContentfulPaint:   &models.Metric{Median: 800.3},
			CumulativeLayoutShift:  &models.Metric{Median: 0.05},
		},
		Timings: &models.Timings{
			FullyLoaded: &models.Metric{Median: 2567.89},
		},
	}
	btData, _ := json.Marshal(browsertime)
	if err := os.WriteFile(filepath.Join(dataDir, "browsertime.summary-total.json"), btData, 0644); err != nil {
		t.Fatal(err)
	}

	pagexray := models.PageXray{
		TransferSize: &models.Metric{Median: 524288},
	}
	pxData, _ := json.Marshal(pagexray)
	if err := os.WriteFile(filepath.Join(dataDir, "pagexray.summary-total.json"), pxData, 0644); err != nil {
		t.Fatal(err)
	}

	// Create a simple index.html for result browsing
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><body>Results</body></html>"), 0644); err != nil {
		t.Fatal(err)
	}
}

func setupTestServer(t *testing.T, runner *mockRunner) (*httptest.Server, *storage.Service) {
	t.Helper()
	ctx := context.Background()
	cfg := testhelper.StartMinio(t, ctx)
	svc, err := storage.NewServiceWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create storage service: %v", err)
	}

	h := handler.NewHandler(svc, runner)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/result/{id}", h.HandleAnalyze)
	mux.HandleFunc("DELETE /api/result/{id}", h.HandleDeleteResult)
	mux.HandleFunc("GET /result/{id}/{path...}", h.HandleGetResult)
	mux.HandleFunc("GET /screenshot/{id}", h.HandleGetScreenshot)

	srv := httptest.NewServer(h.AuthMiddleware(mux))
	t.Cleanup(srv.Close)
	return srv, svc
}

func TestHandleAnalyze(t *testing.T) {
	mock := &mockRunner{
		runFunc: func(ctx context.Context, id string, req models.ApiAnalyzeRequest) (string, error) {
			dir := t.TempDir()
			createFakeSitespeedResult(t, dir)
			return dir, nil
		},
	}

	srv, _ := setupTestServer(t, mock)

	body := `{"urls": ["https://example.com"]}`
	resp, err := http.Post(srv.URL+"/api/result/test-123", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result models.AnalyzeResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if result.Ttfb != 123.45 {
		t.Errorf("expected TTFB 123.45, got %f", result.Ttfb)
	}
	if result.FullyLoaded != 2567.89 {
		t.Errorf("expected FullyLoaded 2567.89, got %f", result.FullyLoaded)
	}
	if result.LargestContentfulPaint != 1200.5 {
		t.Errorf("expected LCP 1200.5, got %f", result.LargestContentfulPaint)
	}
	if result.FirstContentfulPaint != 800.3 {
		t.Errorf("expected FCP 800.3, got %f", result.FirstContentfulPaint)
	}
	if result.CumulativeLayoutShift != 0.05 {
		t.Errorf("expected CLS 0.05, got %f", result.CumulativeLayoutShift)
	}
	if result.TransferSize != 524288 {
		t.Errorf("expected TransferSize 524288, got %f", result.TransferSize)
	}
}

func TestHandleAnalyzeInvalidID(t *testing.T) {
	srv, _ := setupTestServer(t, &mockRunner{})

	tests := []struct {
		name       string
		id         string
		wantStatus int
	}{
		{"path traversal", "..%2F..", http.StatusBadRequest},
		{"slash", "foo/bar", http.StatusNotFound}, // router doesn't match this pattern
		{"backslash", "foo\\bar", http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"urls": ["https://example.com"]}`
			resp, err := http.Post(srv.URL+"/api/result/"+tc.id, "application/json", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("expected %d, got %d", tc.wantStatus, resp.StatusCode)
			}
		})
	}
}

func TestHandleAnalyzeInvalidBody(t *testing.T) {
	srv, _ := setupTestServer(t, &mockRunner{})

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"invalid json", "{invalid", http.StatusBadRequest},
		{"no urls", `{"urls": []}`, http.StatusBadRequest},
		{"too many urls", `{"urls": ["http://a","http://b","http://c","http://d","http://e","http://f"]}`, http.StatusBadRequest},
		{"invalid url", `{"urls": ["not-a-url"]}`, http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Post(srv.URL+"/api/result/valid-id", "application/json", strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("expected %d, got %d", tc.wantStatus, resp.StatusCode)
			}
		})
	}
}

func TestHandleAnalyzeRunnerError(t *testing.T) {
	mock := &mockRunner{
		runFunc: func(ctx context.Context, id string, req models.ApiAnalyzeRequest) (string, error) {
			return "", fmt.Errorf("container failed")
		},
	}

	srv, _ := setupTestServer(t, mock)

	body := `{"urls": ["https://example.com"]}`
	resp, err := http.Post(srv.URL+"/api/result/test-err", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", resp.StatusCode)
	}
}

func TestHandleGetResultAndDelete(t *testing.T) {
	mock := &mockRunner{
		runFunc: func(ctx context.Context, id string, req models.ApiAnalyzeRequest) (string, error) {
			dir := t.TempDir()
			createFakeSitespeedResult(t, dir)
			return dir, nil
		},
	}

	srv, _ := setupTestServer(t, mock)

	// First, analyze to upload results
	body := `{"urls": ["https://example.com"]}`
	resp, err := http.Post(srv.URL+"/api/result/browse-test", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("analyze failed: %d", resp.StatusCode)
	}

	// Browse the result (index.html)
	resp, err = http.Get(srv.URL + "/result/browse-test/index.html")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for result browse, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("expected text/html content type, got %s", ct)
	}

	// Delete the result
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/result/browse-test", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for delete, got %d", resp.StatusCode)
	}
}

func TestHandleGetResultNotFound(t *testing.T) {
	srv, _ := setupTestServer(t, &mockRunner{})

	resp, err := http.Get(srv.URL + "/result/nonexistent/index.html")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestHandleGetScreenshot(t *testing.T) {
	mock := &mockRunner{
		runFunc: func(ctx context.Context, id string, req models.ApiAnalyzeRequest) (string, error) {
			dir := t.TempDir()
			createFakeSitespeedResult(t, dir)
			return dir, nil
		},
	}

	srv, _ := setupTestServer(t, mock)

	// Analyze first to upload screenshot
	body := `{"urls": ["https://example.com"]}`
	resp, err := http.Post(srv.URL+"/api/result/screenshot-test", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Get the screenshot
	resp, err = http.Get(srv.URL + "/screenshot/screenshot-test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("expected image/png, got %s", ct)
	}
}

func TestHandleGetScreenshotNotFound(t *testing.T) {
	srv, _ := setupTestServer(t, &mockRunner{})

	resp, err := http.Get(srv.URL + "/screenshot/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestAuthMiddleware(t *testing.T) {
	srv, _ := setupTestServer(t, &mockRunner{})

	t.Setenv("AUTH_TOKEN", "secret-token")

	// No token - should fail
	body := `{"urls": ["https://example.com"]}`
	resp, err := http.Post(srv.URL+"/api/result/auth-test", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}

	// Wrong token
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/result/auth-test", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", resp.StatusCode)
	}

	// Correct token
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/result/auth-test", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// Should not be 401 (could be 400/500 depending on runner, but not 401)
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatal("expected auth to pass with correct token")
	}

	// Non-API path should not require auth
	resp, err = http.Get(srv.URL + "/result/something/index.html")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatal("non-API path should not require auth")
	}
}

func TestHandleAnalyzeWithHeaders(t *testing.T) {
	var capturedReq models.ApiAnalyzeRequest
	mock := &mockRunner{
		runFunc: func(ctx context.Context, id string, req models.ApiAnalyzeRequest) (string, error) {
			capturedReq = req
			dir := t.TempDir()
			createFakeSitespeedResult(t, dir)
			return dir, nil
		},
	}

	srv, _ := setupTestServer(t, mock)

	body := `{"urls": ["https://example.com"], "headers": {"Authorization": "Bearer test", "X-Custom": "value"}}`
	resp, err := http.Post(srv.URL+"/api/result/headers-test", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if len(capturedReq.Headers) != 2 {
		t.Fatalf("expected 2 headers, got %d", len(capturedReq.Headers))
	}
	if capturedReq.Headers["Authorization"] != "Bearer test" {
		t.Errorf("expected Authorization header, got %q", capturedReq.Headers["Authorization"])
	}
	if capturedReq.Headers["X-Custom"] != "value" {
		t.Errorf("expected X-Custom header, got %q", capturedReq.Headers["X-Custom"])
	}
}
