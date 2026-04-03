package middleware

import (
	"io"
	"net/http"
	"strings"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

// CompressMiddleware adds zstd and gzip compression based on Accept-Encoding.
func CompressMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ae := r.Header.Get("Accept-Encoding")
		if ae == "" {
			next.ServeHTTP(w, r)
			return
		}

		encoding := negotiateEncoding(ae)
		if encoding == "" {
			next.ServeHTTP(w, r)
			return
		}

		cw := &compressResponseWriter{
			ResponseWriter: w,
			encoding:       encoding,
		}
		defer cw.Close()

		next.ServeHTTP(cw, r)
	})
}

func negotiateEncoding(ae string) string {
	// Prefer zstd over gzip
	if strings.Contains(ae, "zstd") {
		return "zstd"
	}
	if strings.Contains(ae, "gzip") {
		return "gzip"
	}
	return ""
}

type compressResponseWriter struct {
	http.ResponseWriter
	encoding    string
	writer      io.WriteCloser
	wroteHeader bool
}

func (cw *compressResponseWriter) initWriter() {
	switch cw.encoding {
	case "zstd":
		w, _ := zstd.NewWriter(cw.ResponseWriter, zstd.WithEncoderLevel(zstd.SpeedDefault))
		cw.writer = w
	case "gzip":
		w, _ := gzip.NewWriterLevel(cw.ResponseWriter, gzip.DefaultCompression)
		cw.writer = w
	}
	cw.ResponseWriter.Header().Set("Content-Encoding", cw.encoding)
	cw.ResponseWriter.Header().Del("Content-Length")
	cw.ResponseWriter.Header().Add("Vary", "Accept-Encoding")
}

func (cw *compressResponseWriter) WriteHeader(statusCode int) {
	if cw.wroteHeader {
		return
	}
	cw.wroteHeader = true

	// Don't compress if no body or already encoded
	if statusCode == http.StatusNoContent || statusCode == http.StatusNotModified ||
		cw.ResponseWriter.Header().Get("Content-Encoding") != "" {
		cw.encoding = ""
		cw.ResponseWriter.WriteHeader(statusCode)
		return
	}

	if cw.encoding != "" {
		cw.initWriter()
	}
	cw.ResponseWriter.WriteHeader(statusCode)
}

func (cw *compressResponseWriter) Write(b []byte) (int, error) {
	if !cw.wroteHeader {
		cw.WriteHeader(http.StatusOK)
	}
	if cw.writer != nil {
		return cw.writer.Write(b)
	}
	return cw.ResponseWriter.Write(b)
}

func (cw *compressResponseWriter) Close() {
	if cw.writer != nil {
		_ = cw.writer.Close()
	}
}

// Unwrap supports http.ResponseController and middleware that needs the original writer.
func (cw *compressResponseWriter) Unwrap() http.ResponseWriter {
	return cw.ResponseWriter
}
