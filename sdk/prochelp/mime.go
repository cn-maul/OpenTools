package prochelp

import (
	"os"
	"path/filepath"
	"strings"
)

// MIMEByExtension maps a file extension to a MIME type for common artifact
// formats. Unknown extensions default to application/octet-stream.
func MIMEByExtension(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return "application/json"
	case ".csv":
		return "text/csv"
	case ".txt", ".log", ".md":
		return "text/plain"
	case ".html", ".htm":
		return "text/html"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	default:
		return "application/octet-stream"
	}
}

// ensureDir is exported for tools that need the same directory discipline
// (0700) the framework applies elsewhere.
func ensureDir(path string) error {
	return os.MkdirAll(path, 0o700)
}
