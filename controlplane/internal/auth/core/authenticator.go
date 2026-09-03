package core

import (
	"context"
)

// Authenticator is the interface for authentication methods.
type Authenticator interface {
	// Name returns the authenticator name for logging.
	Name() string
	// Supports checks if this authenticator can handle the given credential.
	Supports(credential Credential) bool
	// Authenticate validates the credential and returns authentication info.
	//
	// The requestInfo provides request context such as the gRPC method being
	// called.
	Authenticate(ctx context.Context, credential Credential, requestInfo *RequestInfo) (*AuthInfo, error)
}
