package runner

import (
	"context"

	"github.com/shyim/sitespeed-api/internal/models"
)

// Runner is the interface for executing sitespeed.io analyses in isolated environments.
type Runner interface {
	// RunAnalysis runs a sitespeed.io analysis and returns the local path to results.
	RunAnalysis(ctx context.Context, id string, req models.ApiAnalyzeRequest) (resultDir string, err error)

	// CleanupOrphaned removes stale workloads that exceeded their timeout.
	CleanupOrphaned(ctx context.Context) error

	// CleanupStaleResultDirs removes local result directories older than maxAgeMinutes.
	CleanupStaleResultDirs(maxAgeMinutes int)

	// Close releases resources held by the runner.
	Close() error
}
