package x509_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/testutils/tlscert"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	x509auth "github.com/yanet-platform/yanet2/controlplane/internal/auth/x509"
)

// decodeConfigNode parses text as the "config: ..." wrapper production uses
// and returns the inner node the authenticator factory receives.
func decodeConfigNode(t *testing.T, text string) *yaml.Node {
	t.Helper()

	var wrapper struct {
		Config yaml.Node `yaml:"config"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(text), &wrapper))

	return &wrapper.Config
}

// Test_NewFromConfig_RequiresCASources verifies that a configuration without
// authorities is refused, since nothing could be verified against it.
func Test_NewFromConfig_RequiresCASources(t *testing.T) {
	_, err := x509auth.NewFromConfig(decodeConfigNode(t, "config:\n  refresh_interval: 1m\n"))
	require.ErrorContains(t, err, "ca_sources is required")
}

// Test_NewFromConfig_WiresSources verifies that the configured bundle and
// revocation list are what the authenticator verifies and revokes against.
func Test_NewFromConfig_WiresSources(t *testing.T) {
	ca := tlscert.NewCA(t)
	revoked := ca.IssueClient(t, "route-operator")
	current := ca.IssueClient(t, "pipeline-operator")
	crlFile := ca.RevocationListFile(t, time.Now().Add(time.Hour), revoked.Leaf)

	rawConfig := decodeConfigNode(t, "config:\n"+
		"  ca_sources:\n    - "+ca.BundleFile()+"\n"+
		"  crl_sources:\n    - "+crlFile+"\n"+
		"  refresh_interval: 1m\n")
	authenticator, err := x509auth.NewFromConfig(rawConfig)
	require.NoError(t, err)
	t.Cleanup(authenticator.Close)

	require.True(t, ca.Pool().Equal(authenticator.ClientCAs()))

	_, err = authenticator.Authenticate(t.Context(), verifiedCredential(ca, revoked), &core.RequestInfo{})
	require.ErrorContains(t, err, x509auth.ErrCertificateRevoked.Error())

	info, err := authenticator.Authenticate(t.Context(), verifiedCredential(ca, current), &core.RequestInfo{})
	require.NoError(t, err)
	require.Equal(t, "pipeline-operator", info.Subject.Login)
}

// Test_NewFromConfig_RejectsUnknownKey verifies that a key the config does
// not declare is an error, so a misspelled revocation list key cannot
// silently leave revocation off.
func Test_NewFromConfig_RejectsUnknownKey(t *testing.T) {
	ca := tlscert.NewCA(t)

	_, err := x509auth.NewFromConfig(decodeConfigNode(t, "config:\n"+
		"  ca_sources:\n    - "+ca.BundleFile()+"\n"+
		"  crl_source: /etc/yanet2/auth/clients.crl\n"))
	require.ErrorContains(t, err, "crl_source")
}
