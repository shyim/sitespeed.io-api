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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, os.MkdirAll(screenshotDir, 0755))

	// Create a fake screenshot
	require.NoError(t, os.WriteFile(filepath.Join(screenshotDir, "afterPageCompleteCheck.png"), []byte("fake-png-data"), 0644))

	// Create data directory with browsertime and pagexray summaries
	dataDir := filepath.Join(dir, "data")
	require.NoError(t, os.MkdirAll(dataDir, 0755))

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
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "browsertime.summary-total.json"), btData, 0644))

	pagexray := models.PageXray{
		TransferSize: &models.Metric{Median: 524288},
	}
	pxData, _ := json.Marshal(pagexray)
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "pagexray.summary-total.json"), pxData, 0644))

	// Create a simple index.html for result browsing
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><body>Results</body></html>"), 0644))
}

func setupTestServer(t *testing.T, runner *mockRunner) (*httptest.Server, *storage.Service) {
	t.Helper()
	ctx := context.Background()
	cfg := testhelper.StartMinio(t, ctx)
	svc, err := storage.NewServiceWithConfig(ctx, cfg)
	require.NoError(t, err)

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
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result models.AnalyzeResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

	assert.Equal(t, 123.45, result.Ttfb)
	assert.Equal(t, 2567.89, result.FullyLoaded)
	assert.Equal(t, 1200.5, result.LargestContentfulPaint)
	assert.Equal(t, 800.3, result.FirstContentfulPaint)
	assert.Equal(t, 0.05, result.CumulativeLayoutShift)
	assert.Equal(t, 524288.0, result.TransferSize)
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
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Equal(t, tc.wantStatus, resp.StatusCode)
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
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			assert.Equal(t, tc.wantStatus, resp.StatusCode)
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
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
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
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Browse the result (index.html)
	resp, err = http.Get(srv.URL + "/result/browse-test/index.html")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/html")

	// Delete the result
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/result/browse-test", nil)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestHandleGetResultNotFound(t *testing.T) {
	srv, _ := setupTestServer(t, &mockRunner{})

	resp, err := http.Get(srv.URL + "/result/nonexistent/index.html")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
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
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	// Get the screenshot
	resp, err = http.Get(srv.URL + "/screenshot/screenshot-test")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
}

func TestHandleGetScreenshotNotFound(t *testing.T) {
	srv, _ := setupTestServer(t, &mockRunner{})

	resp, err := http.Get(srv.URL + "/screenshot/nonexistent")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAuthMiddleware(t *testing.T) {
	srv, _ := setupTestServer(t, &mockRunner{})

	t.Setenv("AUTH_TOKEN", "secret-token")

	// No token - should fail
	body := `{"urls": ["https://example.com"]}`
	resp, err := http.Post(srv.URL+"/api/result/auth-test", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// Wrong token
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/result/auth-test", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// Correct token
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/result/auth-test", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	// Should not be 401 (could be 400/500 depending on runner, but not 401)
	assert.NotEqual(t, http.StatusUnauthorized, resp.StatusCode)

	// Non-API path should not require auth
	resp, err = http.Get(srv.URL + "/result/something/index.html")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.NotEqual(t, http.StatusUnauthorized, resp.StatusCode)
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
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, capturedReq.Headers, 2)
	assert.Equal(t, "Bearer test", capturedReq.Headers["Authorization"])
	assert.Equal(t, "value", capturedReq.Headers["X-Custom"])
}
