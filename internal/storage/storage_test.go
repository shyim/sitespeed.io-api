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
)

func setupService(t *testing.T) (*storage.Service, context.Context) {
	t.Helper()
	ctx := context.Background()
	cfg := testhelper.StartMinio(t, ctx)
	svc, err := storage.NewServiceWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create storage service: %v", err)
	}
	return svc, ctx
}

func TestUploadAndDownloadFile(t *testing.T) {
	svc, ctx := setupService(t)

	// Create a temp file to upload
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(srcPath, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	// Upload
	if err := svc.UploadFile(ctx, "test/test.txt", srcPath); err != nil {
		t.Fatalf("UploadFile failed: %v", err)
	}

	// Download
	destPath := filepath.Join(tmpDir, "downloaded.txt")
	if err := svc.DownloadFile(ctx, "test/test.txt", destPath); err != nil {
		t.Fatalf("DownloadFile failed: %v", err)
	}

	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Fatalf("expected 'hello world', got %q", string(data))
	}
}

func TestUploadStreamAndGetFile(t *testing.T) {
	svc, ctx := setupService(t)

	content := "stream content here"
	if err := svc.UploadStream(ctx, "stream/data.txt", strings.NewReader(content)); err != nil {
		t.Fatalf("UploadStream failed: %v", err)
	}

	reader, _, lastModified, etag, err := svc.GetFile(ctx, "stream/data.txt")
	if err != nil {
		t.Fatalf("GetFile failed: %v", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("expected %q, got %q", content, string(data))
	}
	if lastModified == nil {
		t.Fatal("expected lastModified to be set")
	}
	if etag == nil {
		t.Fatal("expected etag to be set")
	}
}

func TestDeleteFile(t *testing.T) {
	svc, ctx := setupService(t)

	// Upload a file
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "to-delete.txt")
	if err := os.WriteFile(srcPath, []byte("delete me"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := svc.UploadFile(ctx, "delete/file.txt", srcPath); err != nil {
		t.Fatalf("UploadFile failed: %v", err)
	}

	// Delete
	if err := svc.DeleteFile(ctx, "delete/file.txt"); err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}

	// Verify it's gone
	_, _, _, _, err := svc.GetFile(ctx, "delete/file.txt")
	if err == nil {
		t.Fatal("expected error when getting deleted file, got nil")
	}
}

func TestGetFileNotFound(t *testing.T) {
	svc, ctx := setupService(t)

	_, _, _, _, err := svc.GetFile(ctx, "nonexistent/file.txt")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestDownloadFileNotFound(t *testing.T) {
	svc, ctx := setupService(t)

	destPath := filepath.Join(t.TempDir(), "nope.txt")
	err := svc.DownloadFile(ctx, "nonexistent/file.txt", destPath)
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}
