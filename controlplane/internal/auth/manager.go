package auth

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/basic"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/identity"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/none"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/permission"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/rbac"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshcert"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshkey"
)

// AuthenticatorFactory creates an Authenticator from a raw YAML config node.
type AuthenticatorFactory func(
	rawCfg *yaml.Node,
	log *zap.Logger,
) (core.Authenticator, error)

// factories maps authenticator type names to their factory functions.
var factories = map[string]AuthenticatorFactory{
	"basic":   basic.NewFromConfig,
	"sshkey":  sshkey.NewFromConfig,
	"sshcert": sshcert.NewFromConfig,
}

// Authorizer is the interface for authorization decisions.
type Authorizer interface {
	// Authorize checks if the principal has permission to execute the given
	// method.
	Authorize(
		ctx context.Context,
		principal *core.Principal,
		fullMethod string,
	) error
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
	authenticators   []core.Authenticator
	identityProvider identity.Provider
	authorizer       Authorizer
	disabled         bool
	log              *zap.Logger
}

// NewManager creates a new auth Manager.
func NewManager(cfg *Config, options ...ManagerOption) (*Manager, error) {
	opts := newManagerOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log

	m := &Manager{
		authenticators: []core.Authenticator{},
		disabled:       cfg.Disabled,
		log:            log,
	}

	// If disabled, only use NoneAuthenticator.
	if cfg.Disabled {
		log.Warn("authentication is DISABLED - requests will be anonymous with FULL permissions")
		m.authenticators = append(
			m.authenticators, none.NewNoneAuthenticator(),
		)
		return m, nil
	}

	// Build CompositeIdentityProvider from config.
	var identityProviders []identity.Provider
	for _, providerCfg := range cfg.IdentityProviders {
		switch providerCfg.Type {
		case "file":
			fileProvider, err := identity.NewIdentityProviderFromFile(
				providerCfg.Path,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"failed to create file identity provider: %w", err,
				)
			}
			identityProviders = append(identityProviders, fileProvider)
			log.Info("registered identity provider",
				zap.String("type", "file"),
				zap.String("path", providerCfg.Path),
			)
		default:
			return nil, fmt.Errorf(
				"unknown identity provider type: %q", providerCfg.Type,
			)
		}
	}

	if len(identityProviders) == 0 {
		return nil, fmt.Errorf("no identity providers configured")
	}

	m.identityProvider = identity.NewCompositeIdentityProvider(
		identityProviders,
		identity.WithLog(log),
	)

	// Create FilePermissionStore.
	permissionStore, err := permission.NewFilePermissionStore(
		cfg.PermissionsPath,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to create permission store: %w", err,
		)
	}
	log.Info("loaded permissions",
		zap.String("path", cfg.PermissionsPath),
	)

	// Create RBACAuthorizer.
	m.authorizer = rbac.NewRBACAuthorizer(
		permissionStore, rbac.WithLog(log),
	)

	// Create authenticators from config entries.
	for _, entry := range cfg.Authenticators {
		factory, ok := factories[entry.Type]
		if !ok {
			return nil, fmt.Errorf("unknown authenticator type: %q", entry.Type)
		}

		auth, err := factory(&entry.Config, log)
		if err != nil {
			return nil, fmt.Errorf("failed to init %q authenticator: %w", entry.Type, err)
		}

		m.authenticators = append(m.authenticators, auth)
		log.Info("registered authenticator",
			zap.String("type", entry.Type),
		)
	}

	// NoneAuthenticator is always the last fallback.
	m.authenticators = append(
		m.authenticators, none.NewNoneAuthenticator(),
	)

	return m, nil
}

// Authenticate attempts to authenticate the given token using registered
// authenticators.
//
// Returns the authenticated Principal on success.
func (m *Manager) Authenticate(
	ctx context.Context,
	token string,
	reqInfo *core.RequestInfo,
) (*core.Principal, error) {
	// Iterate through authenticators, first match wins.
	for _, auth := range m.authenticators {
		if !auth.IsTokenSupported(token) {
			continue
		}

		m.log.Debug("authenticating with authenticator",
			zap.String("authenticator", auth.Name()),
		)

		authInfo, err := auth.Authenticate(ctx, token, reqInfo)
		if err != nil {
			return nil, err
		}

		return m.buildPrincipal(ctx, authInfo)
	}

	// This shouldn't happen with NoneAuthenticator registered, because it
	// accepts everything.
	return nil, fmt.Errorf("no authenticator supports the given token")
}

// buildPrincipal resolves the identity and assembles a Principal from AuthInfo.
func (m *Manager) buildPrincipal(
	ctx context.Context,
	authInfo *core.AuthInfo,
) (*core.Principal, error) {
	// Anonymous path: skip identity resolution.
	if authInfo.AuthMethod == "none" {
		return &core.Principal{
			User:        authInfo.Username,
			Groups:      []string{},
			AuthMethod:  authInfo.AuthMethod,
			AuthTime:    time.Now(),
			IsAnonymous: true,
		}, nil
	}

	if m.identityProvider == nil {
		return nil, fmt.Errorf("no identity provider configured")
	}

	ident, err := m.identityProvider.GetIdentity(ctx, authInfo.Username)
	if err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"identity lookup failed for user %q: %v",
			authInfo.Username, err,
		)
	}

	if ident.Disabled {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"account %q is disabled", authInfo.Username,
		)
	}

	return &core.Principal{
		User:        ident.Username,
		Groups:      ident.Groups,
		AuthMethod:  authInfo.AuthMethod,
		AuthTime:    time.Now(),
		IsAnonymous: false,
	}, nil
}

// Authorize checks if the principal has permission to execute the given
// method.
func (m *Manager) Authorize(
	ctx context.Context,
	principal *core.Principal,
	fullMethod string,
) error {
	if m.authorizer == nil {
		return nil // No authorizer configured, allow all.
	}
	return m.authorizer.Authorize(ctx, principal, fullMethod)
}
