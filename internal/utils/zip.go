package utils

import (
	"archive/zip"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

func ZipDirectory(source, target string) error {
	zipfile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer func() { _ = zipfile.Close() }()

	archive := zip.NewWriter(zipfile)
	defer func() { _ = archive.Close() }()

	err = filepath.WalkDir(source, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		// Create a header based on the file info
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}

		// Relativize the path to the source directory
		relPath, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}

		// Use forward slashes for zip spec
		header.Name = filepath.ToSlash(relPath)

		if d.IsDir() {
			header.Name += "/"
		} else {
			header.Method = zip.Deflate
		}

		// Skip the root directory entry itself
		if header.Name == "./" || header.Name == "." {
			return nil
		}

		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		_, err = io.Copy(writer, file)
		return err
	})

	return err
}
