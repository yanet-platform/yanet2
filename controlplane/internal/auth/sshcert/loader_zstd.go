package sshcert

import (
	"fmt"

	"github.com/klauspost/compress/zstd"
)

// zstdLoader wraps another Loader and decompresses the data with zstd.
type zstdLoader struct {
	wrapped Loader
}

func (m *zstdLoader) Source() string {
	return m.wrapped.Source()
}

func (m *zstdLoader) Load() ([]byte, error) {
	compressed, err := m.wrapped.Load()
	if err != nil {
		return nil, err
	}

	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("create zstd decoder: %w", err)
	}
	defer decoder.Close()

	data, err := decoder.DecodeAll(compressed, nil)
	if err != nil {
		return nil, fmt.Errorf(
			"zstd decompress %q: %w", m.wrapped.Source(), err,
		)
	}

	return data, nil
}
