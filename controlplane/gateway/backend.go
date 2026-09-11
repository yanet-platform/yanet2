package gateway

import (
	"context"
	"errors"
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
	"google.golang.org/grpc/test/bufconn"
)

// memoryListenerBufferSize is the per-connection buffer of an in-memory
// listener.
//
// It bounds only how much a writer may run ahead of the reader, not the
// message size: a config larger than the buffer streams through it in
// pieces.
const memoryListenerBufferSize = 1 << 20

// backend is a live proxying connection to a registered upstream: the gRPC
// connection plus the endpoint the registry tracks it by.
type backend struct {
	endpoint  string
	conn      *grpc.ClientConn
	closeOnce sync.Once
	closeErr  error
}

// newBackend creates a proxying connection over a transport the caller
// supplies, labeled with the address the registry tracks it by.
func newBackend(
	endpoint string,
	dial func(context.Context) (net.Conn, error),
	creds credentials.TransportCredentials,
) (*backend, error) {
	conn, err := grpc.NewClient(
		"passthrough:target",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return dial(ctx)
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

// dialBackend creates a backend that proxies to endpoint over TCP, or over a
// Unix socket when endpoint is a path.
func dialBackend(endpoint string, creds credentials.TransportCredentials) (*backend, error) {
	return newBackend(endpoint, func(ctx context.Context) (net.Conn, error) {
		dialer := net.Dialer{}
		if strings.HasPrefix(endpoint, "/") {
			return dialer.DialContext(ctx, "unix", endpoint)
		}
		return dialer.DialContext(ctx, "tcp", endpoint)
	}, creds)
}

// newMemoryListener creates a listener reachable only from inside the
// process, through the backend that dials it.
func newMemoryListener() *bufconn.Listener {
	return bufconn.Listen(memoryListenerBufferSize)
}

// serve runs a gRPC server on a listener, treating a stop that landed
// before serving began as the clean shutdown it is.
//
// A stop during serving ends the call without an error, but a stop that wins
// the race against the goroutine entering it is reported as one, and the two
// are the same outcome for the caller.
func serve(server *grpc.Server, listener net.Listener) error {
	if err := server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return err
	}
	return nil
}

// dialMemoryBackend creates a backend that proxies into an in-memory
// listener without a network hop.
//
// The transport credentials must match what the server behind the listener
// expects: plaintext for a server without credentials, the pinned loopback
// credentials for the gateway's own TLS server. The address is only the
// label the registry tracks the backend by: where the proxied services are
// reachable from outside, which is the gateway's own address.
func dialMemoryBackend(
	endpoint string,
	listener *bufconn.Listener,
	creds credentials.TransportCredentials,
) (*backend, error) {
	return newBackend(endpoint, listener.DialContext, creds)
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

// BuildError satisfies proxy.Backend. The gateway never synthesises error
// frames.
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
