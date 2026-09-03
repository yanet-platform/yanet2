package operator

import (
	"fmt"

	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/go/xgrpc"
)

// DialGateway opens a client connection to the gateway cfg describes, in
// plaintext or over TLS as its tls block says.
//
// The connection dials lazily, so an unreachable gateway surfaces on the
// first RPC rather than here.
func DialGateway(cfg GatewayConfig) (*grpc.ClientConn, error) {
	creds, err := xgrpc.ClientCredentials(cfg.TLS)
	if err != nil {
		return nil, fmt.Errorf("failed to build transport credentials for gateway %q: %w", cfg.Name, err)
	}

	endpoint := cfg.Endpoint.Unwrap()
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("failed to dial gateway %q at %q: %w", cfg.Name, endpoint, err)
	}

	return conn, nil
}
