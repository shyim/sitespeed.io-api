package cleanup

import (
	"context"
	"log/slog"
	"time"

	"github.com/shyim/sitespeed-api/internal/runner"
)

func Start(r runner.Runner) {
	slog.Info("Container and result cleanup scheduled every 5 minutes")

	ctx := context.Background()
	run := func() {
		if err := r.CleanupOrphaned(ctx); err != nil {
			slog.Error("Orphaned cleanup failed", "error", err)
		}
		r.CleanupStaleResultDirs(10)
	}

	run()

	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		for range ticker.C {
			run()
		}
	}()
}
