package storage_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shyim/sitespeed-api/internal/storage"
	"github.com/shyim/sitespeed-api/internal/testhelper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupService(t *testing.T) (*storage.Service, context.Context) {
	t.Helper()
	ctx := context.Background()
	cfg := testhelper.StartMinio(t, ctx)
	svc, err := storage.NewServiceWithConfig(ctx, cfg)
	require.NoError(t, err)
	return svc, ctx
}

func TestUploadAndDownloadFile(t *testing.T) {
	svc, ctx := setupService(t)

	// Create a temp file to upload
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "test.txt")
	require.NoError(t, os.WriteFile(srcPath, []byte("hello world"), 0644))

	// Upload
	require.NoError(t, svc.UploadFile(ctx, "test/test.txt", srcPath))

	// Download
	destPath := filepath.Join(tmpDir, "downloaded.txt")
	require.NoError(t, svc.DownloadFile(ctx, "test/test.txt", destPath))

	data, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(data))
}

func TestUploadStreamAndGetFile(t *testing.T) {
	svc, ctx := setupService(t)

	content := "stream content here"
	require.NoError(t, svc.UploadStream(ctx, "stream/data.txt", strings.NewReader(content)))

	reader, _, lastModified, etag, err := svc.GetFile(ctx, "stream/data.txt")
	require.NoError(t, err)
	defer func() {
		require.NoError(t, reader.Close())
	}()

	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, content, string(data))
	assert.NotNil(t, lastModified)
	assert.NotNil(t, etag)
}

func TestDeleteFile(t *testing.T) {
	svc, ctx := setupService(t)

	// Upload a file
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "to-delete.txt")
	require.NoError(t, os.WriteFile(srcPath, []byte("delete me"), 0644))
	require.NoError(t, svc.UploadFile(ctx, "delete/file.txt", srcPath))

	// Delete
	require.NoError(t, svc.DeleteFile(ctx, "delete/file.txt"))

	// Verify it's gone
	_, _, _, _, err := svc.GetFile(ctx, "delete/file.txt")
	assert.Error(t, err)
}

func TestGetFileNotFound(t *testing.T) {
	svc, ctx := setupService(t)

	_, _, _, _, err := svc.GetFile(ctx, "nonexistent/file.txt")
	assert.Error(t, err)
}

func TestDownloadFileNotFound(t *testing.T) {
	svc, ctx := setupService(t)

	destPath := filepath.Join(t.TempDir(), "nope.txt")
	err := svc.DownloadFile(ctx, "nonexistent/file.txt", destPath)
	assert.Error(t, err)
}
