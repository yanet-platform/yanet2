package auth

import (
	"context"

	"google.golang.org/grpc/metadata"
)

const (
	// authMetadataKey is the metadata header key for authentication tokens.
	authMetadataKey = "x-yanet-authentication"
)

// ExtractToken extracts the authentication token from gRPC metadata.
// Returns empty string if no token is present.
func ExtractToken(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}

	values := md.Get(authMetadataKey)
	if len(values) == 0 {
		return ""
	}

	// Return the first value (there should only be one).
	return values[0]
}
