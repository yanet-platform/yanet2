package operator

import (
	"fmt"
	"slices"

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

// DialGatewayWith is DialGateway with extra gRPC dial options.
//
// The transport credentials the tls block selects are applied after the
// extra options, so that none of them can replace the credentials.
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
	dialOptions = append(slices.Clip(dialOptions), grpc.WithTransportCredentials(creds))
	conn, err := grpc.NewClient(endpoint, dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to dial gateway %q at %q: %w", cfg.Name, endpoint, err)
	}

	return conn, nil
}
