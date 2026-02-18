package sshcert

import (
	"fmt"
	"os"
)

// fileLoader loads data from a local file.
type fileLoader struct {
	path string
}

func (m *fileLoader) Source() string {
	return m.path
}

func (m *fileLoader) Load() ([]byte, error) {
	data, err := os.ReadFile(m.path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %q: %w", m.path, err)
	}

	return data, nil
}
