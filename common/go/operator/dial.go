package operator

import (
	"fmt"

	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/go/xgrpc"
)

// DialGateway opens a client connection to the gateway cfg describes, in
// plaintext or over TLS as its tls block says.
//
// The options configure the store presenting the client certificate. The
// connection dials lazily, so an unreachable gateway surfaces on the first
// RPC rather than here.
func DialGateway(cfg GatewayConfig, options ...xgrpc.CertificateStoreOption) (*grpc.ClientConn, error) {
	return DialGatewayWith(cfg, nil, options...)
}

// DialGatewayWith is DialGateway with extra gRPC dial options, applied after
// the transport credentials so that they may override them.
//
// A context dialer among them lets the connection run over an in-memory
// transport; the endpoint then needs a scheme that skips name resolution,
// such as passthrough.
func DialGatewayWith(
	cfg GatewayConfig,
	dialOptions []grpc.DialOption,
	options ...xgrpc.CertificateStoreOption,
) (*grpc.ClientConn, error) {
	creds, err := xgrpc.ClientCredentials(cfg.TLS, options...)
	if err != nil {
		return nil, fmt.Errorf("failed to build transport credentials for gateway %q: %w", cfg.Name, err)
	}

	endpoint := cfg.Endpoint.Unwrap()
	dialOptions = append([]grpc.DialOption{grpc.WithTransportCredentials(creds)}, dialOptions...)
	conn, err := grpc.NewClient(endpoint, dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to dial gateway %q at %q: %w", cfg.Name, endpoint, err)
	}

	return conn, nil
}
