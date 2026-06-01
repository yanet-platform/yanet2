package gateway

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/c2h5oh/datasize"
	"github.com/siderolabs/grpc-proxy/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/metadata"
)

// backend is a live proxying connection to a registered upstream: the gRPC
// connection plus the endpoint the registry tracks it by.
type backend struct {
	endpoint  string
	conn      *grpc.ClientConn
	closeOnce sync.Once
	closeErr  error
}

// dialBackend creates a backend that proxies to endpoint.
func dialBackend(endpoint string, creds credentials.TransportCredentials) (*backend, error) {
	conn, err := grpc.NewClient(
		"passthrough:target",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			dialer := net.Dialer{}
			if strings.HasPrefix(endpoint, "/") {
				return dialer.DialContext(ctx, "unix", endpoint)
			}
			return dialer.DialContext(ctx, "tcp", endpoint)
		}),
		grpc.WithDefaultCallOptions(
			grpc.ForceCodecV2(proxy.Codec()),
			grpc.UseCompressor(gzip.Name),
			grpc.MaxCallRecvMsgSize(int(256*datasize.MB)),
			grpc.MaxCallSendMsgSize(int(256*datasize.MB)),
		),
		grpc.WithTransportCredentials(creds),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client to backend: %w", err)
	}

	return &backend{endpoint: endpoint, conn: conn}, nil
}

// String returns the endpoint for logging.
func (m *backend) String() string {
	return m.Endpoint()
}

// Endpoint returns the endpoint address this backend connects to.
func (m *backend) Endpoint() string {
	return m.endpoint
}

// GetConnection returns the underlying gRPC connection, forwarding incoming
// metadata as outgoing metadata.
func (m *backend) GetConnection(ctx context.Context, _ string) (context.Context, *grpc.ClientConn, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	return metadata.NewOutgoingContext(ctx, md.Copy()), m.conn, nil
}

// AppendInfo passes the response bytes through unchanged.
func (m *backend) AppendInfo(_ bool, resp []byte) ([]byte, error) {
	return resp, nil
}

// BuildError satisfies proxy.Backend; the gateway never synthesises error frames.
func (m *backend) BuildError(bool, error) ([]byte, error) {
	return nil, nil
}

// Close closes the underlying connection.
//
// It is idempotent: the loopback backend is shared across several registry
// entries, so the shutdown sweep may close it more than once.
func (m *backend) Close() error {
	m.closeOnce.Do(func() {
		m.closeErr = m.conn.Close()
	})

	return m.closeErr
}
