package auth

import (
	"context"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

const (
	// authMetadataKey is the metadata header key for authentication tokens.
	authMetadataKey = "x-yanet-authentication"
)

// ExtractCredential collects what the request presents for authentication:
// the token from gRPC metadata and the TLS state of the peer connection.
//
// Either part is left empty when absent.
func ExtractCredential(ctx context.Context) core.Credential {
	var credential core.Credential

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		// TODO: should we allow passing multiple tokens at once?
		if values := md.Get(authMetadataKey); len(values) > 0 {
			credential.Token = values[0]
		}
	}

	if p, ok := peer.FromContext(ctx); ok {
		if info, ok := p.AuthInfo.(credentials.TLSInfo); ok {
			state := info.State
			credential.TLS = &state
		}
	}

	return credential
}
