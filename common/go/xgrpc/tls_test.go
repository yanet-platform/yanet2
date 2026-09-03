package xgrpc_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/testutils/tlscert"
	"github.com/yanet-platform/yanet2/common/go/xgrpc"
)

// Test_ClientCredentials_Nil_Plaintext verifies that a missing config dials
// in plaintext.
func Test_ClientCredentials_Nil_Plaintext(t *testing.T) {
	t.Parallel()

	creds, err := xgrpc.ClientCredentials(nil)
	require.NoError(t, err)
	require.Equal(t, "insecure", creds.Info().SecurityProtocol)
}

// Test_ClientCredentials_Config verifies that a present config yields TLS
// credentials when its files are consistent and readable, and an error
// otherwise.
func Test_ClientCredentials_Config(t *testing.T) {
	t.Parallel()

	ca := tlscert.NewCA(t)
	client := ca.IssueClient(t, "operator")
	missing := filepath.Join(t.TempDir(), "missing.pem")

	tests := []struct {
		name    string
		config  xgrpc.ClientTLSConfig
		wantErr bool
	}{
		{name: "empty config uses system roots"},
		{name: "CA bundle", config: xgrpc.ClientTLSConfig{CAFile: ca.BundleFile()}},
		{name: "CA bundle with client keypair", config: xgrpc.ClientTLSConfig{
			CAFile: ca.BundleFile(), CertFile: client.CertFile, KeyFile: client.KeyFile,
		}},
		{name: "certificate without key", config: xgrpc.ClientTLSConfig{CertFile: client.CertFile}, wantErr: true},
		{name: "key without certificate", config: xgrpc.ClientTLSConfig{KeyFile: client.KeyFile}, wantErr: true},
		{name: "missing CA bundle", config: xgrpc.ClientTLSConfig{CAFile: missing}, wantErr: true},
		{name: "CA bundle without certificates", config: xgrpc.ClientTLSConfig{CAFile: client.KeyFile}, wantErr: true},
		{name: "missing certificate file", config: xgrpc.ClientTLSConfig{CertFile: missing, KeyFile: client.KeyFile}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			creds, err := xgrpc.ClientCredentials(&test.config)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "tls", creds.Info().SecurityProtocol)
		})
	}
}
