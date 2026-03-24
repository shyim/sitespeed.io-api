package runner

import (
	"context"

	"github.com/shyim/sitespeed-api/internal/models"
)

type Runner interface {
	RunAnalysis(ctx context.Context, id string, req models.ApiAnalyzeRequest) (resultDir string, err error)
	CleanupOrphaned(ctx context.Context) error
	CleanupStaleResultDirs(maxAgeMinutes int)
	Close() error
}
