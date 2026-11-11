package auth

import (
	"context"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

// TokenMetadataKey is the metadata key a caller sends its authentication
// token under.
const TokenMetadataKey = "x-yanet-authentication"

// credentialKey keys in a call's context the credential the call was let in
// with before it reached the server.
type credentialKey struct{}

// WithCredential records in ctx the credential a call was let in with before
// it reached the server.
func WithCredential(ctx context.Context, credential core.Credential) context.Context {
	return context.WithValue(ctx, credentialKey{}, credential)
}

// ExtractCredential collects what the request presents for authentication:
// the token from gRPC metadata and the TLS state of the peer connection.
//
// A call let in before it reached the server presents the credential it was
// let in with instead. Either part is left empty when absent.
func ExtractCredential(ctx context.Context) core.Credential {
	if credential, ok := ctx.Value(credentialKey{}).(core.Credential); ok {
		return credential
	}

	var credential core.Credential

	if md, ok := metadata.FromIncomingContext(ctx); ok {
		// TODO: should we allow passing multiple tokens at once?
		if values := md.Get(TokenMetadataKey); len(values) > 0 {
			credential.Token = values[0]
		}
	}

	if peerInfo, ok := peer.FromContext(ctx); ok {
		if info, ok := peerInfo.AuthInfo.(credentials.TLSInfo); ok {
			state := info.State
			credential.TLS = &state
		}
	}

	return credential
}
