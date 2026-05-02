package gateway

import (
	"net"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// TransportCredentials returns credentials for gateway registration clients.
func TransportCredentials(
	cfg *TLSConfig,
	endpoint string,
) (credentials.TransportCredentials, error) {
	if cfg == nil {
		return insecure.NewCredentials(), nil
	}

	return cfg.LoopbackClientCredentials(hostFromEndpoint(endpoint))
}

func hostFromEndpoint(endpoint string) string {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return endpoint
	}

	return host
}
