package basic

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/internal/gateway/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/gateway/auth/identity"
)

// BasicAuthenticator implements Basic Auth (username:password in base64).
type BasicAuthenticator struct {
	credentialStore  CredentialStore
	identityProvider identity.Provider
}

// NewBasicAuthenticator creates a new BasicAuthenticator.
func NewBasicAuthenticator(
	credentialStore CredentialStore,
	identityProvider identity.Provider,
) *BasicAuthenticator {
	return &BasicAuthenticator{
		credentialStore:  credentialStore,
		identityProvider: identityProvider,
	}
}

// Name returns the authenticator name.
func (m *BasicAuthenticator) Name() string {
	return "basic"
}

// IsTokenSupported checks if the token is a Basic Auth token.
func (m *BasicAuthenticator) IsTokenSupported(token string) bool {
	return strings.HasPrefix(strings.ToLower(token), "basic ")
}

// Authenticate validates the Basic Auth token.
func (m *BasicAuthenticator) Authenticate(ctx context.Context, token string) (*core.Principal, error) {
	// Extract base64 part.
	parts := strings.SplitN(token, " ", 2)
	if len(parts) != 2 {
		return nil, status.Error(codes.Unauthenticated, "invalid token format")
	}

	// Decode base64.
	decoded, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid base64 encoding")
	}

	// Split username:password.
	credentials := strings.SplitN(string(decoded), ":", 2)
	if len(credentials) != 2 {
		return nil, status.Error(codes.Unauthenticated, "invalid credentials format")
	}

	username := credentials[0]
	password := credentials[1]

	// Verify password.
	if err := m.credentialStore.VerifyCredentials(username, password); err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
	}

	// Get identity (groups, disabled status).
	identity, err := m.identityProvider.GetIdentity(ctx, username)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "identity lookup failed: %v", err)
	}

	// Check if account is disabled.
	if identity.Disabled {
		return nil, status.Error(codes.Unauthenticated, "account is disabled")
	}

	// Create principal.
	return &core.Principal{
		User:        identity.Username,
		Groups:      identity.Groups,
		AuthMethod:  "basic",
		AuthTime:    time.Now(),
		IsAnonymous: false,
	}, nil
}
