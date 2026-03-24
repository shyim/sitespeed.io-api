package docker_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shyim/sitespeed-api/internal/docker"
	"github.com/shyim/sitespeed-api/internal/models"
)

func TestRunAnalysisGoogle(t *testing.T) {
	if os.Getenv("INTEGRATION_DOCKER") == "" {
		t.Skip("skipping docker integration test; set INTEGRATION_DOCKER=1 to run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	t.Setenv("RESULT_BASE_DIR", t.TempDir())
	t.Setenv("DOCKER_TIMEOUT", "300s")
	t.Setenv("MAX_CONCURRENT_ANALYSES", "1")

	runner, err := docker.NewRunner()
	if err != nil {
		t.Fatalf("failed to create runner: %v", err)
	}
	defer runner.Close()

	if err := runner.EnsureImage(ctx); err != nil {
		t.Fatalf("failed to ensure image: %v", err)
	}

	resultDir, err := runner.RunAnalysis(ctx, "test-google", models.ApiAnalyzeRequest{
		URLs: []string{"https://www.google.com"},
	})
	if err != nil {
		t.Fatalf("RunAnalysis failed: %v", err)
	}
	defer os.RemoveAll(resultDir)

	// Verify browsertime summary exists and is valid JSON
	btPath := filepath.Join(resultDir, "data", "browsertime.summary-total.json")
	btData, err := os.ReadFile(btPath)
	if err != nil {
		t.Fatalf("browsertime summary not found: %v", err)
	}

	var bt models.BrowserTime
	if err := json.Unmarshal(btData, &bt); err != nil {
		t.Fatalf("failed to parse browsertime summary: %v", err)
	}

	if bt.GoogleWebVitals == nil {
		t.Fatal("expected GoogleWebVitals to be present")
	}
	if bt.GoogleWebVitals.Ttfb == nil || bt.GoogleWebVitals.Ttfb.Median <= 0 {
		t.Error("expected TTFB > 0")
	}
	if bt.GoogleWebVitals.FirstContentfulPaint == nil || bt.GoogleWebVitals.FirstContentfulPaint.Median <= 0 {
		t.Error("expected FCP > 0")
	}
	t.Logf("TTFB: %.2fms, FCP: %.2fms", bt.GoogleWebVitals.Ttfb.Median, bt.GoogleWebVitals.FirstContentfulPaint.Median)

	// Verify pagexray summary exists
	pxPath := filepath.Join(resultDir, "data", "pagexray.summary-total.json")
	pxData, err := os.ReadFile(pxPath)
	if err != nil {
		t.Fatalf("pagexray summary not found: %v", err)
	}

	var px models.PageXray
	if err := json.Unmarshal(pxData, &px); err != nil {
		t.Fatalf("failed to parse pagexray summary: %v", err)
	}
	if px.TransferSize == nil || px.TransferSize.Median <= 0 {
		t.Error("expected TransferSize > 0")
	}

	// Verify pages directory has content
	pagesDir := filepath.Join(resultDir, "pages")
	pages, err := os.ReadDir(pagesDir)
	if err != nil {
		t.Fatalf("pages directory not found: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("expected at least one page directory")
	}

	// Verify screenshot exists
	var firstPageDir string
	for _, p := range pages {
		if p.IsDir() {
			firstPageDir = p.Name()
			break
		}
	}
	screenshotPath := filepath.Join(pagesDir, firstPageDir, "data", "screenshots", "1", "afterPageCompleteCheck.png")
	if _, err := os.Stat(screenshotPath); err != nil {
		t.Errorf("screenshot not found: %v", err)
	}
}
