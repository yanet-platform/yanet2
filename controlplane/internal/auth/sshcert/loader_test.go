package sshcert_test

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshcert"
)

func TestNewLoader_FileAutoDetect(t *testing.T) {
	expectedData := []byte("file CA data content")
	path := t.TempDir() + "/ca.pub"
	require.NoError(t, os.WriteFile(path, expectedData, 0o600))

	loader := sshcert.NewLoader(path)
	data, err := loader.Load()
	require.NoError(t, err)
	assert.Equal(t, expectedData, data)
}

func TestNewLoader_HTTPAutoDetect(t *testing.T) {
	expectedData := []byte("HTTPS CA data content")
	server := httptest.NewTLSServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(expectedData)
		}),
	)
	defer server.Close()

	loader := sshcert.NewLoader(server.URL + "/ca.yaml")
	_, err := loader.Load()
	require.Error(t, err)
	var verificationError *tls.CertificateVerificationError
	require.True(t, errors.As(err, &verificationError))
}

func TestNewLoader_HTTPAutoDetect_NoTLS(t *testing.T) {
	loader := sshcert.NewLoader("http://example.com/ca.yaml")
	assert.Equal(t, "http://example.com/ca.yaml", loader.Source())
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

	loader := sshcert.NewLoader(server.URL + "/ca.yaml")
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

	loader := sshcert.NewLoader(server.URL + "/missing")
	_, err := loader.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 404")
}
