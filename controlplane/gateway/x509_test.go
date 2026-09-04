package gateway_test

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/common/go/testutils/tlscert"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
)

// writeTestFile writes content under the test's temporary directory and
// returns the path.
func writeTestFile(t *testing.T, name, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

// newX509AuthConfig builds an enabled auth config from YAML the way
// production parses it: identities know route-operator and alice, both in
// a group allowed every method, anonymous may only introspect and list
// services, the x509 authenticator trusts ca and a basic authenticator knows
// alice with the password "s3cret".
func newX509AuthConfig(t *testing.T, ca *tlscert.CA) auth.Config {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.MinCost)
	require.NoError(t, err)

	identities := writeTestFile(t, "identities.yaml", "identities:\n"+
		"  - username: route-operator\n    groups: [operators]\n"+
		"  - username: alice\n    groups: [operators]\n")
	permissions := writeTestFile(t, "permissions.yaml", "permissions:\n"+
		"  groups:\n    - name: operators\n      permissions: [\"/*/*\"]\n"+
		"  users:\n    - username: anonymous\n      permissions:\n"+
		"        - \"/controlplane.ynpb.v1.AuthService/IntrospectToken\"\n"+
		"        - \"/controlplane.ynpb.v1.Gateway/ListServices\"\n")
	credentials := writeTestFile(t, "basic_auth.yaml", "credentials:\n"+
		"  - username: alice\n    password_hash: "+string(hash)+"\n")

	text := "disabled: false\n" +
		"identity_providers:\n  - type: file\n    path: " + identities + "\n" +
		"permissions_path: " + permissions + "\n" +
		"authenticators:\n" +
		"  - type: basic\n    config:\n      credentials_path: " + credentials + "\n" +
		"  - type: x509\n    config:\n      ca_sources:\n        - " + ca.BundleFile() + "\n"

	var config auth.Config
	require.NoError(t, yaml.Unmarshal([]byte(text), &config))

	return config
}

// startX509Gateway runs a gateway serving TLS with a certificate issued by
// ca on a pre-bound listener and returns the address to dial.
func startX509Gateway(t *testing.T, ca *tlscert.CA, cfg gateway.Config) string {
	t.Helper()

	server := ca.IssueServer(t, "127.0.0.1")
	cfg.Server.TLS = &gateway.TLSConfig{
		CertFile: xcfg.MustNonEmptyString(server.CertFile),
		KeyFile:  xcfg.MustNonEmptyString(server.KeyFile),
	}

	listener := NewTestListener(t)
	gw, err := gateway.NewGateway(cfg, gateway.WithLog(zap.NewNop()), gateway.WithListener(listener))
	require.NoError(t, err)
	t.Cleanup(func() { _ = gw.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error { return gw.Run(ctx) })
	t.Cleanup(func() {
		cancel()
		require.NoError(t, group.Wait())
	})

	return listener.Addr().String()
}

// dialAuth opens a TLS connection trusting ca, presenting the given client
// certificates if any, and returns an auth client over it.
func dialAuth(t *testing.T, address string, ca *tlscert.CA, certificates ...tls.Certificate) ynpb.AuthServiceClient {
	t.Helper()

	creds := credentials.NewTLS(&tls.Config{
		RootCAs:      ca.Pool(),
		Certificates: certificates,
		MinVersion:   tls.VersionTLS12,
	})
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(creds))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return ynpb.NewAuthServiceClient(conn)
}

// introspectSelf asks the gateway who the caller is, with a bounded wait.
func introspectSelf(t *testing.T, ctx context.Context, client ynpb.AuthServiceClient) (*ynpb.Principal, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	response, err := client.IntrospectToken(ctx, &ynpb.IntrospectTokenRequest{})
	if err != nil {
		return nil, err
	}

	return response.GetPrincipal(), nil
}

// Test_Gateway_X509_ClientCertificateBecomesPrincipal verifies that a call
// over a connection with a trusted client certificate and no token is
// authenticated as the certificate's common name, and that introspection
// with an empty token reports that identity.
func Test_Gateway_X509_ClientCertificateBecomesPrincipal(t *testing.T) {
	t.Parallel()

	ca := tlscert.NewCA(t)
	cfg := gateway.DefaultConfig()
	cfg.Auth = newX509AuthConfig(t, ca)
	address := startX509Gateway(t, ca, cfg)

	client := dialAuth(t, address, ca, ca.IssueClient(t, "route-operator").Certificate)
	principal, err := introspectSelf(t, t.Context(), client)
	require.NoError(t, err)

	require.Equal(t, "route-operator", principal.GetUser())
	require.Equal(t, []string{"operators"}, principal.GetGroups())
	require.Equal(t, "x509", principal.GetAuthMethod())
	require.False(t, principal.GetIsAnonymous())
}

// Test_Gateway_X509_NoCertificateIsAnonymous verifies that a TLS client
// without a certificate is still accepted by the listener and ends up
// anonymous, so the certificate stays optional at the transport.
func Test_Gateway_X509_NoCertificateIsAnonymous(t *testing.T) {
	t.Parallel()

	ca := tlscert.NewCA(t)
	cfg := gateway.DefaultConfig()
	cfg.Auth = newX509AuthConfig(t, ca)
	address := startX509Gateway(t, ca, cfg)

	principal, err := introspectSelf(t, t.Context(), dialAuth(t, address, ca))
	require.NoError(t, err)

	require.True(t, principal.GetIsAnonymous())
	require.Equal(t, "none", principal.GetAuthMethod())
}

// Test_Gateway_X509_TokenWinsOverCertificate verifies that a token in the
// metadata identifies the call even when the connection carries a trusted
// certificate of someone else.
func Test_Gateway_X509_TokenWinsOverCertificate(t *testing.T) {
	t.Parallel()

	ca := tlscert.NewCA(t)
	cfg := gateway.DefaultConfig()
	cfg.Auth = newX509AuthConfig(t, ca)
	address := startX509Gateway(t, ca, cfg)

	client := dialAuth(t, address, ca, ca.IssueClient(t, "route-operator").Certificate)
	token := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret"))
	ctx := metadata.AppendToOutgoingContext(t.Context(), "x-yanet-authentication", token)

	principal, err := introspectSelf(t, ctx, client)
	require.NoError(t, err)

	require.Equal(t, "alice", principal.GetUser())
	require.Equal(t, "basic", principal.GetAuthMethod())
}

// Test_Gateway_X509_UnknownCommonNameIsRejected verifies that a trusted
// certificate whose common name no identity provider knows is refused
// rather than downgraded to anonymous.
func Test_Gateway_X509_UnknownCommonNameIsRejected(t *testing.T) {
	t.Parallel()

	ca := tlscert.NewCA(t)
	cfg := gateway.DefaultConfig()
	cfg.Auth = newX509AuthConfig(t, ca)
	address := startX509Gateway(t, ca, cfg)

	client := dialAuth(t, address, ca, ca.IssueClient(t, "stranger").Certificate)
	_, err := introspectSelf(t, t.Context(), client)
	require.Equal(t, codes.Unauthenticated, status.Code(err))
}

// Test_Gateway_X509_UntrustedCertificateFailsHandshake verifies that a
// client certificate from an authority outside the pool is rejected in the
// handshake, before any RPC is served.
func Test_Gateway_X509_UntrustedCertificateFailsHandshake(t *testing.T) {
	t.Parallel()

	ca := tlscert.NewCA(t)
	other := tlscert.NewCA(t)
	cfg := gateway.DefaultConfig()
	cfg.Auth = newX509AuthConfig(t, ca)
	address := startX509Gateway(t, ca, cfg)

	client := dialAuth(t, address, ca, other.IssueClient(t, "route-operator").Certificate)
	_, err := introspectSelf(t, t.Context(), client)
	require.Equal(t, codes.Unavailable, status.Code(err))
}

// Test_Gateway_HTTPS_NeverRequestsClientCertificate verifies that the HTTP
// listener does not ask for a client certificate even when the gRPC one
// does, so a browser never sees a certificate prompt.
func Test_Gateway_HTTPS_NeverRequestsClientCertificate(t *testing.T) {
	t.Parallel()

	ca := tlscert.NewCA(t)
	cfg := gateway.DefaultConfig()
	cfg.Auth = newX509AuthConfig(t, ca)
	cfg.Server.HTTPEndpoint = newFreeAddress(t)
	startX509Gateway(t, ca, cfg)

	var requested atomic.Bool
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:    ca.Pool(),
				MinVersion: tls.VersionTLS12,
				GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
					requested.Store(true)
					return &tls.Certificate{}, nil
				},
			},
		},
		Timeout: 5 * time.Second,
	}
	url := "https://" + cfg.Server.HTTPEndpoint + "/api/controlplane.ynpb.v1.Gateway/ListServices"

	var statusCode int
	require.Eventually(t, func() bool {
		response, postErr := client.Post(url, "application/json", strings.NewReader("{}"))
		if postErr != nil {
			return false
		}
		defer func() { _ = response.Body.Close() }()

		_, postErr = io.ReadAll(response.Body)
		statusCode = response.StatusCode
		return postErr == nil
	}, 5*time.Second, 50*time.Millisecond, "HTTP proxy did not answer")

	require.Equal(t, http.StatusOK, statusCode)
	require.False(t, requested.Load(), "HTTP listener requested a client certificate")
}

// Test_NewGateway_X509RequiresServerTLS verifies that an x509 authenticator
// without server TLS is a startup error, since no handshake would ever
// verify a certificate for it.
func Test_NewGateway_X509RequiresServerTLS(t *testing.T) {
	t.Parallel()

	ca := tlscert.NewCA(t)
	cfg := gateway.DefaultConfig()
	cfg.Auth = newX509AuthConfig(t, ca)

	_, err := gateway.NewGateway(cfg, gateway.WithLog(zap.NewNop()))
	require.ErrorContains(t, err, "x509 authenticator requires server.tls")
}
