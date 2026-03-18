package cleanup

import (
	"context"
	"log"
	"time"

	"github.com/shyim/sitespeed-api/internal/runner"
)

func Start(r runner.Runner) {
	log.Println("Container and result cleanup scheduled every 5 minutes")

	ctx := context.Background()
	run := func() {
		if err := r.CleanupOrphaned(ctx); err != nil {
			log.Printf("Orphaned cleanup failed: %v", err)
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
