package auth

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/internal/gateway/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/gateway/auth/none"
)

// Authenticator is the interface for authentication methods.
type Authenticator interface {
	// Name returns the authenticator name for logging.
	Name() string
	// IsTokenSupported checks if this authenticator can handle the given
	// token format.
	IsTokenSupported(token string) bool
	// Authenticate validates the token and returns the authenticated
	// Principal.
	Authenticate(ctx context.Context, token string) (*core.Principal, error)
}

// Authorizer is the interface for authorization decisions.
type Authorizer interface {
	// Authorize checks if the principal has permission to execute the given
	// method.
	Authorize(ctx context.Context, principal *core.Principal, fullMethod string) error
}

type managerOptions struct {
	Log *zap.Logger
}

func newManagerOptions() *managerOptions {
	return &managerOptions{
		Log: zap.NewNop(),
	}
}

// ManagerOption is a function that configures the Manager.
type ManagerOption func(*managerOptions)

// WithLog sets the logger for the Manager.
func WithLog(log *zap.Logger) ManagerOption {
	return func(o *managerOptions) {
		o.Log = log
	}
}

// Manager orchestrates authentication and authorization.
type Manager struct {
	authenticators []Authenticator
	authorizer     Authorizer
	disabled       bool
	log            *zap.Logger
}

// NewManager creates a new auth Manager.
func NewManager(cfg *Config, options ...ManagerOption) *Manager {
	opts := newManagerOptions()
	for _, o := range options {
		o(opts)
	}

	m := &Manager{
		authenticators: []Authenticator{},
		disabled:       cfg.Disabled,
		log:            opts.Log,
	}

	// Register NoneAuthenticator for skeleton implementation.
	noneAuth := none.NewNoneAuthenticator()
	m.authenticators = append(m.authenticators, noneAuth)

	if cfg.Disabled {
		m.log.Warn("authentication is DISABLED - requests will be anonymous with FULL permissions")
	}

	return m
}

// Authenticate attempts to authenticate the given token using registered
// authenticators.
//
// Returns the authenticated Principal on success.
func (m *Manager) Authenticate(
	ctx context.Context,
	token string,
) (*core.Principal, error) {
	// Iterate through authenticators, first match wins.
	for _, auth := range m.authenticators {
		if auth.IsTokenSupported(token) {
			m.log.Debug("authenticating with authenticator",
				zap.String("authenticator", auth.Name()),
			)
			return auth.Authenticate(ctx, token)
		}
	}

	// This shouldn't happen with NoneAuthenticator registered, because it
	// accepts everything.
	return nil, fmt.Errorf("no authenticator supports the given token")
}

// Authorize checks if the principal has permission to execute the given
// method.
func (m *Manager) Authorize(
	ctx context.Context,
	principal *core.Principal,
	fullMethod string,
) error {
	// TODO: will be replaced with RBAC authorization in future phases.
	return nil
}
