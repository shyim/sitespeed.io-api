package cleanup

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func Start() {
	log.Println("Chromium temp file cleanup scheduled every 5 minutes")
	cleanupChromiumTempFiles(5)

	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		for range ticker.C {
			cleanupChromiumTempFiles(5)
		}
	}()
}

func cleanupChromiumTempFiles(maxAgeMinutes int) {
	tmpDir := os.TempDir()
	maxAge := time.Duration(maxAgeMinutes) * time.Minute
	now := time.Now()

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		log.Printf("Failed to read temp dir for cleanup: %v", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".org.chromium.Chromium.") {
			info, err := entry.Info()
			if err != nil {
				continue
			}

			if now.Sub(info.ModTime()) > maxAge {
				fullPath := filepath.Join(tmpDir, entry.Name())
				if err := os.RemoveAll(fullPath); err != nil {
					log.Printf("Failed to clean up %s: %v", fullPath, err)
				} else {
					log.Printf("Cleaned up Chromium temp directory (%dmin old): %s", int(now.Sub(info.ModTime()).Minutes()), fullPath)
				}
			}
		}
	}
}
