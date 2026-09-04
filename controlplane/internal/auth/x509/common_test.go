package x509_test

import (
	"crypto/tls"
	"crypto/x509"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yanet-platform/yanet2/common/go/testutils/tlscert"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/loader"
	x509auth "github.com/yanet-platform/yanet2/controlplane/internal/auth/x509"
)

// newStore builds a store trusting ca and consulting the given revocation
// list files.
func newStore(t *testing.T, ca *tlscert.CA, crlFiles ...string) *x509auth.Store {
	t.Helper()

	crlSources := make([]loader.Loader, 0, len(crlFiles))
	for _, file := range crlFiles {
		crlSources = append(crlSources, loader.NewLoader(file))
	}

	store, err := x509auth.NewStore([]loader.Loader{loader.NewLoader(ca.BundleFile())}, crlSources)
	require.NoError(t, err)

	return store
}

// newAuthenticator builds an authenticator over store and stops its refresh
// on cleanup so the goroutine does not outlive the test.
func newAuthenticator(t *testing.T, store *x509auth.Store) *x509auth.Authenticator {
	t.Helper()

	authenticator := x509auth.NewAuthenticator(store)
	t.Cleanup(authenticator.Close)

	return authenticator
}

// verifiedCredential is the credential of a connection whose handshake
// verified keypair against ca, carrying no token.
func verifiedCredential(ca *tlscert.CA, keypair tlscert.Keypair) core.Credential {
	return core.Credential{
		TLS: &tls.ConnectionState{
			VerifiedChains: [][]*x509.Certificate{{keypair.Leaf, ca.Certificate()}},
		},
	}
}
