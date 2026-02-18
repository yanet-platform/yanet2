package auth

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/basic"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/identity"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/none"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/permission"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/rbac"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshcert"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshkey"
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
	//
	// The requestInfo provides request context such as the gRPC method being
	// called.
	Authenticate(ctx context.Context, token string, requestInfo *core.RequestInfo) (*core.Principal, error)
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
func NewManager(cfg *Config, options ...ManagerOption) (*Manager, error) {
	opts := newManagerOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log

	m := &Manager{
		authenticators: []Authenticator{},
		disabled:       cfg.Disabled,
		log:            log,
	}

	// If disabled, only use NoneAuthenticator.
	if cfg.Disabled {
		log.Warn("authentication is DISABLED - requests will be anonymous with FULL permissions")
		m.authenticators = append(m.authenticators, none.NewNoneAuthenticator())
		return m, nil
	}

	// Build CompositeIdentityProvider from config.
	var identityProviders []identity.Provider
	for _, providerCfg := range cfg.IdentityProviders {
		switch providerCfg.Type {
		case "file":
			fileProvider, err := identity.NewIdentityProviderFromFile(providerCfg.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to create file identity provider: %w", err)
			}
			identityProviders = append(identityProviders, fileProvider)
			log.Info("registered identity provider",
				zap.String("type", "file"),
				zap.String("path", providerCfg.Path),
			)
		default:
			return nil, fmt.Errorf("unknown identity provider type: %q", providerCfg.Type)
		}
	}

	if len(identityProviders) == 0 {
		return nil, fmt.Errorf("no identity providers configured")
	}

	compositeIdentityProvider := identity.NewCompositeIdentityProvider(
		identityProviders,
		identity.WithLog(log),
	)

	// Create FilePermissionStore.
	permissionStore, err := permission.NewFilePermissionStore(cfg.PermissionsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create permission store: %w", err)
	}
	log.Info("loaded permissions", zap.String("path", cfg.PermissionsPath))

	// Create RBACAuthorizer.
	m.authorizer = rbac.NewRBACAuthorizer(permissionStore, rbac.WithLog(log))

	// Create SSHKeyAuthenticator if keys path configured.
	if cfg.SSHKey.KeysPath != "" {
		keyStore, err := sshkey.NewKeyStoreFromFile(cfg.SSHKey.KeysPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create SSH key store: %w", err)
		}

		sshkeyOpts := []sshkey.Option{
			sshkey.WithLog(log),
		}
		if cfg.SSHKey.TimeWindow > 0 {
			sshkeyOpts = append(sshkeyOpts, sshkey.WithTimeWindow(cfg.SSHKey.TimeWindow))
		}

		sshKeyAuth := sshkey.NewAuthenticator(keyStore, compositeIdentityProvider, sshkeyOpts...)
		m.authenticators = append(m.authenticators, sshKeyAuth)
		log.Info("registered authenticator", zap.String("type", "sshkey"))
	}

	// Create SSHCertAuthenticator if CA source configured.
	if cfg.SSHCert.CASource != "" {
		caLoader := sshcert.NewLoader(cfg.SSHCert.CASource)
		caStore, err := sshcert.NewCAStoreFromLoader(caLoader)
		if err != nil {
			return nil, fmt.Errorf("failed to create SSH cert CA store: %w", err)
		}

		var revChecker sshcert.RevocationChecker = sshcert.NewNopRevocationChecker()
		if cfg.SSHCert.KRLSource != "" {
			krlLoader := sshcert.NewLoader(cfg.SSHCert.KRLSource)
			revChecker, err = sshcert.NewKRLRevocationCheckerFromLoader(krlLoader)
			if err != nil {
				return nil, fmt.Errorf("failed to create SSH cert revocation checker: %w", err)
			}
		}

		sshcertOpts := []sshcert.Option{
			sshcert.WithLog(log),
		}
		if cfg.SSHCert.TimeWindow > 0 {
			sshcertOpts = append(sshcertOpts, sshcert.WithTimeWindow(cfg.SSHCert.TimeWindow))
		}
		if cfg.SSHCert.RefreshInterval > 0 {
			sshcertOpts = append(sshcertOpts, sshcert.WithRefreshInterval(cfg.SSHCert.RefreshInterval))
		}

		sshCertAuth := sshcert.NewAuthenticator(
			caStore, revChecker, compositeIdentityProvider, sshcertOpts...,
		)
		m.authenticators = append(m.authenticators, sshCertAuth)
		log.Info("registered authenticator", zap.String("type", "sshcert"))
	}

	// Create BasicAuthenticator if credentials path configured.
	if cfg.BasicAuth.CredentialsPath != "" {
		credentialStore, err := basic.NewFileCredentialStore(cfg.BasicAuth.CredentialsPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create credential store: %w", err)
		}

		basicAuth := basic.NewBasicAuthenticator(credentialStore, compositeIdentityProvider)
		m.authenticators = append(m.authenticators, basicAuth)
		log.Info("registered authenticator", zap.String("type", "basic"))
	}

	m.authenticators = append(m.authenticators, none.NewNoneAuthenticator())

	if len(m.authenticators) == 0 {
		return nil, fmt.Errorf("no authenticators configured")
	}

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
		if auth.IsTokenSupported(token) {
			m.log.Debug("authenticating with authenticator",
				zap.String("authenticator", auth.Name()),
			)
			return auth.Authenticate(ctx, token, reqInfo)
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
	if m.authorizer == nil {
		return nil // No authorizer configured, allow all.
	}
	return m.authorizer.Authorize(ctx, principal, fullMethod)
}
