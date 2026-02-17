package core

import (
	"context"
)

// contextKey is a private type for storing Principal in context.
type contextKey struct{}

// principalKey is the key used to store Principal in context.
var principalKey = contextKey{}

// WithPrincipal adds a Principal to the context.
// This is used by auth interceptors to propagate authenticated identity.
func WithPrincipal(ctx context.Context, principal *Principal) context.Context {
	return context.WithValue(ctx, principalKey, principal)
}

// FromContext extracts the Principal from the context.
//
// Returns nil if no Principal is present.
func FromContext(ctx context.Context) *Principal {
	principal, _ := ctx.Value(principalKey).(*Principal)
	return principal
}
