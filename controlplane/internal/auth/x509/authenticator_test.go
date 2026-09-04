package x509_test

import (
	"crypto/tls"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/testutils/tlscert"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	x509auth "github.com/yanet-platform/yanet2/controlplane/internal/auth/x509"
)

// Test_Authenticator_Supports verifies that only a verified client
// certificate presented without a token is handled, so a token of any kind
// wins over the certificate of the connection it arrived on.
func Test_Authenticator_Supports(t *testing.T) {
	ca := tlscert.NewCA(t)
	client := ca.IssueClient(t, "route-operator")
	authenticator := newAuthenticator(t, newStore(t, ca))

	withToken := verifiedCredential(ca, client)
	withToken.Token = "Basic YWxpY2U6czNjcmV0"

	tests := []struct {
		name       string
		credential core.Credential
		want       bool
	}{
		{name: "plaintext connection", credential: core.Credential{}, want: false},
		{name: "tls without client certificate", credential: core.Credential{TLS: &tls.ConnectionState{}}, want: false},
		{name: "verified certificate with token", credential: withToken, want: false},
		{name: "verified certificate alone", credential: verifiedCredential(ca, client), want: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equal(t, testCase.want, authenticator.Supports(testCase.credential))
		})
	}
}

// Test_Authenticator_Authenticate_CommonNameBecomesLogin verifies that a
// valid certificate yields a local subject whose login is its common name
// and whose assertion carries the certificate itself.
func Test_Authenticator_Authenticate_CommonNameBecomesLogin(t *testing.T) {
	ca := tlscert.NewCA(t)
	client := ca.IssueClient(t, "route-operator")
	authenticator := newAuthenticator(t, newStore(t, ca))

	info, err := authenticator.Authenticate(t.Context(), verifiedCredential(ca, client), &core.RequestInfo{})
	require.NoError(t, err)

	require.Equal(t, "x509", info.AuthMethod)
	require.Equal(t, core.Subject{
		Issuer:     "local",
		Identifier: "route-operator",
		Login:      "route-operator",
		Assertion:  x509auth.CertificateAssertion{Leaf: client.Leaf},
	}, info.Subject)
}

// Test_Authenticator_Authenticate_Login verifies how the login is derived
// from a certificate without a common name: exactly one SPIFFE identity is
// used, anything else is rejected.
func Test_Authenticator_Authenticate_Login(t *testing.T) {
	ca := tlscert.NewCA(t)
	authenticator := newAuthenticator(t, newStore(t, ca))

	spiffe := &url.URL{Scheme: "spiffe", Host: "example.org", Path: "/ns/yanet/sa/route"}
	other := &url.URL{Scheme: "spiffe", Host: "example.org", Path: "/ns/yanet/sa/pipeline"}
	https := &url.URL{Scheme: "https", Host: "example.org", Path: "/route"}

	tests := []struct {
		name      string
		keypair   tlscert.Keypair
		wantLogin string
	}{
		{name: "common name wins over spiffe id", keypair: ca.IssueClient(t, "route-operator", tlscert.WithURIs(spiffe)), wantLogin: "route-operator"},
		{name: "single spiffe id without common name", keypair: ca.IssueClient(t, "", tlscert.WithURIs(spiffe)), wantLogin: spiffe.String()},
		{name: "spiffe id beside another uri", keypair: ca.IssueClient(t, "", tlscert.WithURIs(spiffe, https)), wantLogin: spiffe.String()},
		{name: "no name at all", keypair: ca.IssueClient(t, "")},
		{name: "two spiffe ids", keypair: ca.IssueClient(t, "", tlscert.WithURIs(spiffe, other))},
		{name: "only a non-spiffe uri", keypair: ca.IssueClient(t, "", tlscert.WithURIs(https))},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			info, err := authenticator.Authenticate(t.Context(), verifiedCredential(ca, testCase.keypair), &core.RequestInfo{})
			if testCase.wantLogin == "" {
				require.Equal(t, codes.Unauthenticated, status.Code(err))
				require.ErrorContains(t, err, x509auth.ErrNoSubjectName.Error())
				return
			}

			require.NoError(t, err)
			require.Equal(t, testCase.wantLogin, info.Subject.Login)
		})
	}
}

// Test_Authenticator_Authenticate_Validity verifies that the validity period
// is checked at the time of the call, since the handshake that verified the
// certificate may be arbitrarily old.
func Test_Authenticator_Authenticate_Validity(t *testing.T) {
	ca := tlscert.NewCA(t)
	authenticator := newAuthenticator(t, newStore(t, ca))
	now := time.Now()

	tests := []struct {
		name    string
		keypair tlscert.Keypair
		wantErr error
	}{
		{name: "expired", keypair: ca.IssueClient(t, "route-operator", tlscert.WithValidity(now.Add(-2*time.Hour), now.Add(-time.Hour))), wantErr: x509auth.ErrCertificateExpired},
		{name: "not yet valid", keypair: ca.IssueClient(t, "route-operator", tlscert.WithValidity(now.Add(time.Hour), now.Add(2*time.Hour))), wantErr: x509auth.ErrCertificateNotYetValid},
		{name: "current", keypair: ca.IssueClient(t, "route-operator")},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := authenticator.Authenticate(t.Context(), verifiedCredential(ca, testCase.keypair), &core.RequestInfo{})
			if testCase.wantErr == nil {
				require.NoError(t, err)
				return
			}

			require.Equal(t, codes.Unauthenticated, status.Code(err))
			require.ErrorContains(t, err, testCase.wantErr.Error())
		})
	}
}

// Test_Authenticator_Authenticate_RejectsRevokedCertificate verifies that a
// certificate named by a loaded revocation list is rejected while another
// certificate from the same authority is not.
func Test_Authenticator_Authenticate_RejectsRevokedCertificate(t *testing.T) {
	ca := tlscert.NewCA(t)
	revoked := ca.IssueClient(t, "route-operator")
	current := ca.IssueClient(t, "pipeline-operator")
	crlFile := ca.RevocationListFile(t, time.Now().Add(time.Hour), revoked.Leaf)
	authenticator := newAuthenticator(t, newStore(t, ca, crlFile))

	_, err := authenticator.Authenticate(t.Context(), verifiedCredential(ca, revoked), &core.RequestInfo{})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.ErrorContains(t, err, x509auth.ErrCertificateRevoked.Error())

	_, err = authenticator.Authenticate(t.Context(), verifiedCredential(ca, current), &core.RequestInfo{})
	require.NoError(t, err)
}

// Test_Authenticator_Authenticate_StaleRevocationListStillApplies verifies
// that a list past its next update keeps revoking, so a stalled PKI never
// widens what is accepted.
func Test_Authenticator_Authenticate_StaleRevocationListStillApplies(t *testing.T) {
	ca := tlscert.NewCA(t)
	revoked := ca.IssueClient(t, "route-operator")
	crlFile := ca.RevocationListFile(t, time.Now().Add(-time.Hour), revoked.Leaf)
	authenticator := newAuthenticator(t, newStore(t, ca, crlFile))

	_, err := authenticator.Authenticate(t.Context(), verifiedCredential(ca, revoked), &core.RequestInfo{})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.ErrorContains(t, err, x509auth.ErrCertificateRevoked.Error())
}

// Test_Authenticator_Authenticate_RejectsMissingChain verifies that a
// credential without a verified chain is refused rather than treated as
// anonymous, whichever way it reached the authenticator.
func Test_Authenticator_Authenticate_RejectsMissingChain(t *testing.T) {
	ca := tlscert.NewCA(t)
	authenticator := newAuthenticator(t, newStore(t, ca))

	for _, credential := range []core.Credential{{}, {TLS: &tls.ConnectionState{}}} {
		_, err := authenticator.Authenticate(t.Context(), credential, &core.RequestInfo{})
		require.Equal(t, codes.Unauthenticated, status.Code(err))
		require.ErrorContains(t, err, x509auth.ErrNoVerifiedCertificate.Error())
	}
}
