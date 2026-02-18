package none

import (
	"context"
	"time"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

// NoneAuthenticator implements authentication that always succeeds with
// anonymous principal.
//
// This is used for transitional/testing scenarios and when auth is disabled.
type NoneAuthenticator struct{}

// NewNoneAuthenticator creates a new NoneAuthenticator.
func NewNoneAuthenticator() *NoneAuthenticator {
	return &NoneAuthenticator{}
}

// Name returns the authenticator name for logging.
func (m *NoneAuthenticator) Name() string {
	return "none"
}

// IsTokenSupported always returns true.
func (m *NoneAuthenticator) IsTokenSupported(token string) bool {
	return true
}

// Authenticate always succeeds and returns an anonymous principal with full
// permissions.
func (m *NoneAuthenticator) Authenticate(
	ctx context.Context,
	token string,
	reqInfo *core.RequestInfo,
) (*core.Principal, error) {
	return &core.Principal{
		User:        "anonymous",
		Groups:      []string{},
		AuthMethod:  "none",
		AuthTime:    time.Now(),
		IsAnonymous: true,
	}, nil
}
