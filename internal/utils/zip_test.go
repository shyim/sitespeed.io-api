package utils_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/shyim/sitespeed-api/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestZipDirectory(t *testing.T) {
	srcDir := t.TempDir()

	// Create a directory structure
	require.NoError(t, os.MkdirAll(filepath.Join(srcDir, "subdir"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "root.txt"), []byte("root file"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "subdir", "nested.txt"), []byte("nested file"), 0644))

	zipPath := filepath.Join(t.TempDir(), "output.zip")
	require.NoError(t, utils.ZipDirectory(srcDir, zipPath))

	// Verify the zip contents
	reader, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()

	var names []string
	for _, f := range reader.File {
		names = append(names, f.Name)
	}
	slices.Sort(names)

	expected := []string{"root.txt", "subdir/", "subdir/nested.txt"}
	assert.Equal(t, expected, names)
}

func TestZipDirectoryEmpty(t *testing.T) {
	srcDir := t.TempDir()
	zipPath := filepath.Join(t.TempDir(), "empty.zip")

	require.NoError(t, utils.ZipDirectory(srcDir, zipPath))

	reader, err := zip.OpenReader(zipPath)
	require.NoError(t, err)
	defer func() { _ = reader.Close() }()

	assert.Empty(t, reader.File)
}
