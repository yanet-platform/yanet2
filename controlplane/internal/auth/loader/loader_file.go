package loader

import (
	"fmt"
	"os"
)

// FileLoader loads data from a local file.
type FileLoader struct {
	path string
}

// NewFileLoader creates a loader for a literal local path without decoding.
func NewFileLoader(path string) *FileLoader {
	return &FileLoader{path: path}
}

func (m *FileLoader) Source() string {
	return m.path
}

func (m *FileLoader) Load() ([]byte, error) {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %q: %w", m.path, err)
	}

	return data, nil
}
