package sshkey_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshkey"
)

// decodeConfigNode parses text as the "config: ..." wrapper production uses,
// and returns the inner node exactly as the authenticator entry's nested
// configuration node in the control-plane auth configuration would.
func decodeConfigNode(t *testing.T, text string) *yaml.Node {
	t.Helper()

	var wrapper struct {
		Config yaml.Node `yaml:"config"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(text), &wrapper))

	return &wrapper.Config
}

// generateEd25519Signer creates a new Ed25519 SSH signer.
func generateEd25519Signer(t *testing.T) ssh.Signer {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	signer, err := ssh.NewSignerFromKey(privateKey)
	require.NoError(t, err)

	return signer
}

// writeKeysFile writes a keys.yaml file with one entry for username, whose
// public_key is the authorized_keys line for signer.
func writeKeysFile(t *testing.T, username string, signer ssh.Signer) string {
	t.Helper()

	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))

	path := filepath.Join(t.TempDir(), "keys.yaml")
	content := "keys:\n  - username: " + username +
		"\n    public_key: \"" + line + "\"\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

// signToken builds a "sshkey <base64-json>" token signed by signer.
func signToken(
	t *testing.T,
	signer ssh.Signer,
	username, method string,
	timestamp int64,
	nonce string,
) string {
	t.Helper()

	token := &sshkey.Token{
		Version:   1,
		Username:  username,
		Timestamp: timestamp,
		Nonce:     nonce,
		Method:    method,
	}

	data := fmt.Appendf(nil,
		"version=%d\nusername=%s\ntimestamp=%d\nnonce=%s\nmethod=%s",
		token.Version, token.Username, token.Timestamp, token.Nonce, token.Method,
	)

	signature, err := signer.Sign(rand.Reader, data)
	require.NoError(t, err)
	token.Signature = base64.StdEncoding.EncodeToString(ssh.Marshal(signature))

	jsonBytes, err := json.Marshal(token)
	require.NoError(t, err)

	return "sshkey " + base64.StdEncoding.EncodeToString(jsonBytes)
}

// TestNewFromConfig verifies that a valid configuration builds an
// authenticator backed by the configured keys file, able to authenticate a
// token signed by a registered key.
func TestNewFromConfig(t *testing.T) {
	signer := generateEd25519Signer(t)
	keysPath := writeKeysFile(t, "alice", signer)

	rawConfig := decodeConfigNode(t, "config:\n  keys_path: "+keysPath+"\n")

	authenticator, err := sshkey.NewFromConfig(rawConfig)
	require.NoError(t, err)

	method := "/test.Service/Method"
	requestInfo := &core.RequestInfo{FullMethod: method}
	token := signToken(t, signer, "alice", method, time.Now().UnixNano(), "nonce-1")

	authInfo, err := authenticator.Authenticate(t.Context(), token, requestInfo)
	require.NoError(t, err)
	require.Equal(t, &core.AuthInfo{
		Subject:    core.NewLocalSubject("alice"),
		AuthMethod: "sshkey",
	}, authInfo)
}

// TestNewFromConfigTimeWindow verifies that time_window from the config is
// actually wired into the authenticator: an explicit wide window accepts a
// stale token, while the default (narrow) window rejects the same token.
func TestNewFromConfigTimeWindow(t *testing.T) {
	signer := generateEd25519Signer(t)
	keysPath := writeKeysFile(t, "alice", signer)

	method := "/test.Service/Method"
	requestInfo := &core.RequestInfo{FullMethod: method}
	staleTimestamp := time.Now().Add(-10 * time.Minute).UnixNano()
	token := signToken(t, signer, "alice", method, staleTimestamp, "nonce-1")

	t.Run("wide time_window accepts a stale token", func(t *testing.T) {
		rawConfig := decodeConfigNode(t,
			"config:\n  keys_path: "+keysPath+"\n  time_window: 1h\n")

		authenticator, err := sshkey.NewFromConfig(rawConfig)
		require.NoError(t, err)

		_, err = authenticator.Authenticate(t.Context(), token, requestInfo)
		require.NoError(t, err)
	})

	t.Run("default time_window rejects the same stale token", func(t *testing.T) {
		rawConfig := decodeConfigNode(t, "config:\n  keys_path: "+keysPath+"\n")

		authenticator, err := sshkey.NewFromConfig(rawConfig)
		require.NoError(t, err)

		_, err = authenticator.Authenticate(t.Context(), token, requestInfo)
		require.Error(t, err)
	})
}

// TestNewFromConfigErrors verifies that malformed or incomplete
// configurations are rejected before an authenticator is built.
func TestNewFromConfigErrors(t *testing.T) {
	unparseableKeysPath := filepath.Join(t.TempDir(), "keys.yaml")
	require.NoError(t, os.WriteFile(unparseableKeysPath,
		[]byte("keys:\n  - username: alice\n    public_key: \"not a key\"\n"),
		0o600,
	))

	tests := []struct {
		name string
		text string
	}{
		{
			name: "missing keys_path",
			text: "config: {}\n",
		},
		{
			name: "nonexistent keys file",
			text: "config:\n  keys_path: /nonexistent/keys.yaml\n",
		},
		{
			name: "unparseable public_key",
			text: "config:\n  keys_path: " + unparseableKeysPath + "\n",
		},
		{
			name: "scalar config node",
			text: "config: \"oops\"\n",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			rawConfig := decodeConfigNode(t, testCase.text)

			_, err := sshkey.NewFromConfig(rawConfig)
			require.Error(t, err)
		})
	}
}
