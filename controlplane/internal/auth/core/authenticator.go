package core

import (
	"context"
)

// Authenticator is the interface for authentication methods.
type Authenticator interface {
	// Name returns the authenticator name for logging.
	Name() string
	// IsTokenSupported checks if this authenticator can handle the given token
	// format.
	IsTokenSupported(token string) bool
	// Authenticate validates the token and returns authentication info.
	//
	// The requestInfo provides request context such as the gRPC method being
	// called.
	Authenticate(ctx context.Context, token string, requestInfo *RequestInfo) (*AuthInfo, error)
}
