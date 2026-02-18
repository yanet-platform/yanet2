package sshcert

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLoader_FileAutoDetect(t *testing.T) {
	loader := NewLoader("/etc/yanet/auth/ca.yaml")
	_, ok := loader.(*fileLoader)
	assert.True(t, ok, "expected fileLoader for file path")
	assert.Equal(t, "/etc/yanet/auth/ca.yaml", loader.Source())
}

func TestNewLoader_HTTPAutoDetect(t *testing.T) {
	loader := NewLoader("https://example.com/ca.yaml")
	_, ok := loader.(*httpLoader)
	assert.True(t, ok, "expected httpLoader for HTTP URL")
	assert.Equal(t, "https://example.com/ca.yaml", loader.Source())
}

func TestNewLoader_HTTPAutoDetect_NoTLS(t *testing.T) {
	loader := NewLoader("http://example.com/ca.yaml")
	_, ok := loader.(*httpLoader)
	assert.True(t, ok, "expected httpLoader for http:// URL")
}

func TestHTTPLoader_Success(t *testing.T) {
	expectedData := []byte("test CA data content")

	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(expectedData)
		}),
	)
	defer server.Close()

	loader := NewLoader(server.URL + "/ca.yaml")
	data, err := loader.Load()
	require.NoError(t, err)
	assert.Equal(t, expectedData, data)
}

func TestHTTPLoader_Non200Status(t *testing.T) {
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}),
	)
	defer server.Close()

	loader := NewLoader(server.URL + "/missing")
	_, err := loader.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 404")
}
