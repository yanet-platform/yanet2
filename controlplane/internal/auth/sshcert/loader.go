package sshcert

import (
	"strings"
)

// Loader loads raw bytes from a source (file or HTTP).
type Loader interface {
	// Source returns the source identifier for logging.
	Source() string
	// Load returns the raw content from the source.
	Load() ([]byte, error)
}

// NewLoader creates a Loader based on the source string.
//
// Sources starting with "http://" or "https://" use HTTP, otherwise
// the source is treated as a file path.
func NewLoader(source string) Loader {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return &httpLoader{url: source}
	}

	return &fileLoader{path: source}
}
