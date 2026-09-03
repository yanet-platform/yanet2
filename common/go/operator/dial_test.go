package operator_test

import (
	"context"
	"crypto/tls"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/testutils/tlscert"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/common/go/xgrpc"
)

// serveHealth starts a gRPC health server on a loopback port with the given
// server options, stopping it when the test ends, and returns its address.
func serveHealth(t *testing.T, options ...grpc.ServerOption) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := grpc.NewServer(options...)
	grpc_health_v1.RegisterHealthServer(server, health.NewServer())

	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(server.Stop)

	return listener.Addr().String()
}

// checkHealth dials the gateway described by cfg and reports the error of
// one health check through it.
func checkHealth(t *testing.T, cfg operator.GatewayConfig) error {
	t.Helper()

	conn, err := operator.DialGateway(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	_, err = grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	return err
}

// Test_DialGateway_TLS verifies that the tls block selects the transport the
// gateway speaks: a trusted CA, a client certificate the server demands and a
// server name override all reach a TLS gateway, while plaintext, an untrusted
// CA and a missing client certificate do not.
func Test_DialGateway_TLS(t *testing.T) {
	t.Parallel()

	ca := tlscert.NewCA(t)
	other := tlscert.NewCA(t)
	server := ca.IssueServer(t, "127.0.0.1", "gateway.test")
	client := ca.IssueClient(t, "operator")

	serverTLS := serveHealth(t, grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{server.Certificate},
		MinVersion:   tls.VersionTLS12,
	})))
	mutualTLS := serveHealth(t, grpc.Creds(credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{server.Certificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.Pool(),
		MinVersion:   tls.VersionTLS12,
	})))
	plaintext := serveHealth(t)

	tests := []struct {
		name     string
		endpoint string
		tls      *xgrpc.ClientTLSConfig
		wantErr  bool
	}{
		{name: "plaintext gateway without tls block", endpoint: plaintext},
		{name: "tls gateway with trusted CA", endpoint: serverTLS, tls: &xgrpc.ClientTLSConfig{CAFile: ca.BundleFile()}},
		{name: "tls gateway with server name override", endpoint: serverTLS, tls: &xgrpc.ClientTLSConfig{
			CAFile: ca.BundleFile(), ServerName: "gateway.test",
		}},
		{name: "mutual tls gateway with client certificate", endpoint: mutualTLS, tls: &xgrpc.ClientTLSConfig{
			CAFile: ca.BundleFile(), CertFile: client.CertFile, KeyFile: client.KeyFile,
		}},
		{name: "tls gateway without tls block", endpoint: serverTLS, wantErr: true},
		{name: "tls gateway with untrusted CA", endpoint: serverTLS, tls: &xgrpc.ClientTLSConfig{CAFile: other.BundleFile()}, wantErr: true},
		{name: "mutual tls gateway without client certificate", endpoint: mutualTLS, tls: &xgrpc.ClientTLSConfig{
			CAFile: ca.BundleFile(),
		}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := checkHealth(t, operator.GatewayConfig{
				Name:     "numa0",
				Endpoint: xcfg.MustNonEmptyString(test.endpoint),
				TLS:      test.tls,
			})
			if test.wantErr {
				require.Equal(t, codes.Unavailable, status.Code(err), "%v", err)
				return
			}
			require.NoError(t, err)
		})
	}
}
