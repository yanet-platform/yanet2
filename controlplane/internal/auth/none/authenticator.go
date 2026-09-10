package none

import (
	"context"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

// NoneAuthenticator implements authentication that always succeeds with
// anonymous identity.
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

// Supports always returns true.
func (m *NoneAuthenticator) Supports(credential core.Credential) bool {
	return true
}

// Authenticate always succeeds and returns anonymous authentication info.
func (m *NoneAuthenticator) Authenticate(
	ctx context.Context,
	credential core.Credential,
	reqInfo *core.RequestInfo,
) (*core.AuthInfo, error) {
	return &core.AuthInfo{
		Subject:    core.NewLocalSubject("anonymous"),
		AuthMethod: "none",
	}, nil
}
