package utils_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/shyim/sitespeed-api/internal/utils"
)

func TestZipDirectory(t *testing.T) {
	srcDir := t.TempDir()

	// Create a directory structure
	if err := os.MkdirAll(filepath.Join(srcDir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "root.txt"), []byte("root file"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "subdir", "nested.txt"), []byte("nested file"), 0644); err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(t.TempDir(), "output.zip")
	if err := utils.ZipDirectory(srcDir, zipPath); err != nil {
		t.Fatalf("ZipDirectory failed: %v", err)
	}

	// Verify the zip contents
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	var names []string
	for _, f := range reader.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)

	expected := []string{"root.txt", "subdir/", "subdir/nested.txt"}
	if len(names) != len(expected) {
		t.Fatalf("expected %d entries, got %d: %v", len(expected), len(names), names)
	}
	for i, name := range names {
		if name != expected[i] {
			t.Errorf("entry %d: expected %q, got %q", i, expected[i], name)
		}
	}
}

func TestZipDirectoryEmpty(t *testing.T) {
	srcDir := t.TempDir()
	zipPath := filepath.Join(t.TempDir(), "empty.zip")

	if err := utils.ZipDirectory(srcDir, zipPath); err != nil {
		t.Fatalf("ZipDirectory failed on empty dir: %v", err)
	}

	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	if len(reader.File) != 0 {
		t.Errorf("expected 0 entries in empty zip, got %d", len(reader.File))
	}
}
