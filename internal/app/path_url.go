package app

import (
	"net/url"
	"strings"
)

// EscapeNotebookPath percent-encodes each path segment so notebook names with
// spaces or reserved characters work in HTTP and WebSocket URLs.
// Slashes between segments are preserved (multi-level paths if added later).
func EscapeNotebookPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	// Normalize separators so mixed Windows-style paths still split sanely.
	path = strings.ReplaceAll(path, "\\", "/")
	parts := strings.Split(path, "/")
	for i, p := range parts {
		// PathEscape leaves "/" alone if present; we already split so encode fully.
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
