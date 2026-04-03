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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunAnalysisGoogle(t *testing.T) {
	if os.Getenv("INTEGRATION_DOCKER") == "" {
		t.Skip("skipping docker integration test; set INTEGRATION_DOCKER=1 to run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	t.Setenv("RESULT_BASE_DIR", t.TempDir())
	t.Setenv("ANALYSIS_TIMEOUT", "300s")
	t.Setenv("MAX_CONCURRENT_ANALYSES", "1")

	runner, err := docker.NewRunner()
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, runner.Close())
	}()

	require.NoError(t, runner.EnsureImage(ctx))

	resultDir, err := runner.RunAnalysis(ctx, "test-google", models.ApiAnalyzeRequest{
		URLs: []string{"https://www.google.com"},
	})
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, os.RemoveAll(resultDir))
	}()

	// Verify browsertime summary exists and is valid JSON
	btPath := filepath.Join(resultDir, "data", "browsertime.summary-total.json")
	btData, err := os.ReadFile(btPath)
	require.NoError(t, err)

	var bt models.BrowserTime
	require.NoError(t, json.Unmarshal(btData, &bt))

	require.NotNil(t, bt.GoogleWebVitals)
	require.NotNil(t, bt.GoogleWebVitals.Ttfb)
	assert.Greater(t, bt.GoogleWebVitals.Ttfb.Median, float64(0))
	require.NotNil(t, bt.GoogleWebVitals.FirstContentfulPaint)
	assert.Greater(t, bt.GoogleWebVitals.FirstContentfulPaint.Median, float64(0))
	t.Logf("TTFB: %.2fms, FCP: %.2fms", bt.GoogleWebVitals.Ttfb.Median, bt.GoogleWebVitals.FirstContentfulPaint.Median)

	// Verify pagexray summary exists
	pxPath := filepath.Join(resultDir, "data", "pagexray.summary-total.json")
	pxData, err := os.ReadFile(pxPath)
	require.NoError(t, err)

	var px models.PageXray
	require.NoError(t, json.Unmarshal(pxData, &px))
	require.NotNil(t, px.TransferSize)
	assert.Greater(t, px.TransferSize.Median, float64(0))

	// Verify pages directory has content
	pagesDir := filepath.Join(resultDir, "pages")
	pages, err := os.ReadDir(pagesDir)
	require.NoError(t, err)
	require.NotEmpty(t, pages)

	// Verify screenshot exists
	var firstPageDir string
	for _, p := range pages {
		if p.IsDir() {
			firstPageDir = p.Name()
			break
		}
	}
	screenshotPath := filepath.Join(pagesDir, firstPageDir, "data", "screenshots", "1", "afterPageCompleteCheck.png")
	_, err = os.Stat(screenshotPath)
	assert.NoError(t, err)
}
