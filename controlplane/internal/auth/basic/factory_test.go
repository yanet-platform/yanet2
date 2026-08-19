package basic_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/basic"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
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

// writeCredentialsFile writes a basic_auth.yaml file with one entry for
// username, whose password_hash is a bcrypt hash of password.
func writeCredentialsFile(t *testing.T, username, password string) string {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "basic_auth.yaml")
	content := "credentials:\n  - username: " + username +
		"\n    password_hash: " + string(hash) + "\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

// basicToken builds a "Basic <base64(username:password)>" token.
func basicToken(username, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
}

// TestNewFromConfig verifies that a valid configuration builds an
// authenticator backed by the configured credentials file, able to
// authenticate a correct password and reject an incorrect one.
func TestNewFromConfig(t *testing.T) {
	credentialsPath := writeCredentialsFile(t, "alice", "s3cret")

	rawConfig := decodeConfigNode(t, "config:\n  credentials_path: "+credentialsPath+"\n")

	authenticator, err := basic.NewFromConfig(rawConfig)
	require.NoError(t, err)
	require.Equal(t, "basic", authenticator.Name())

	validToken := basicToken("alice", "s3cret")
	require.True(t, authenticator.IsTokenSupported(validToken))
	require.False(t, authenticator.IsTokenSupported("sshkey abc"))

	requestInfo := &core.RequestInfo{FullMethod: "/test.Service/Method"}

	authInfo, err := authenticator.Authenticate(t.Context(), validToken, requestInfo)
	require.NoError(t, err)
	require.Equal(t, &core.AuthInfo{
		Subject:    core.NewLocalSubject("alice"),
		AuthMethod: "basic",
	}, authInfo)

	wrongToken := basicToken("alice", "wrong-password")
	_, err = authenticator.Authenticate(t.Context(), wrongToken, requestInfo)
	require.Error(t, err)
}

// TestNewFromConfigErrors verifies that malformed or incomplete
// configurations are rejected before an authenticator is built.
func TestNewFromConfigErrors(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{
			name: "missing credentials_path",
			text: "config: {}\n",
		},
		{
			name: "nonexistent credentials file",
			text: "config:\n  credentials_path: /nonexistent/basic_auth.yaml\n",
		},
		{
			name: "scalar config node",
			text: "config: \"oops\"\n",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			rawConfig := decodeConfigNode(t, testCase.text)

			_, err := basic.NewFromConfig(rawConfig)
			require.Error(t, err)
		})
	}
}
