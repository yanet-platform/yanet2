package sshcert

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// defaultHTTPTimeout is the default timeout for HTTP requests.
	defaultHTTPTimeout = 30 * time.Second
)

// httpLoader loads data from an HTTP(S) URL.
type httpLoader struct {
	url string
}

func (m *httpLoader) Source() string {
	return m.url
}

func (m *httpLoader) Load() ([]byte, error) {
	client := &http.Client{Timeout: defaultHTTPTimeout}

	resp, err := client.Get(m.url)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to fetch %q: %w", m.url, err,
		)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"unexpected status %d from %q",
			resp.StatusCode, m.url,
		)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to read response from %q: %w", m.url, err,
		)
	}

	return data, nil
}
