package auth

import (
	"context"
	"crypto/x509"
	"fmt"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/basic"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/identity"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/none"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/permission"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/rbac"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshcert"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/sshkey"
	x509auth "github.com/yanet-platform/yanet2/controlplane/internal/auth/x509"
)

// AuthenticatorFactory creates an Authenticator from a raw YAML config node.
type AuthenticatorFactory func(rawCfg *yaml.Node) (core.Authenticator, error)

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
	clientCAs        func() *x509.CertPool
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
	factories := map[string]AuthenticatorFactory{
		"basic": basic.NewFromConfig,
		"sshkey": func(rawCfg *yaml.Node) (core.Authenticator, error) {
			return sshkey.NewFromConfig(rawCfg, sshkey.WithLog(log))
		},
		"sshcert": func(rawCfg *yaml.Node) (core.Authenticator, error) {
			return sshcert.NewFromConfig(rawCfg, sshcert.WithLog(log))
		},
		"x509": func(rawCfg *yaml.Node) (core.Authenticator, error) {
			// One handshake verifies against one pool, so a second
			// x509 entry could not be honoured.
			if m.clientCAs != nil {
				return nil, fmt.Errorf("x509 authenticator configured more than once")
			}

			authenticator, err := x509auth.NewFromConfig(rawCfg, x509auth.WithLog(log))
			if err != nil {
				return nil, err
			}
			m.clientCAs = authenticator.ClientCAs

			return authenticator, nil
		},
	}

	for _, entry := range cfg.Authenticators {
		factory, ok := factories[entry.Type]
		if !ok {
			return nil, fmt.Errorf("unknown authenticator type: %q", entry.Type)
		}

		auth, err := factory(&entry.Config)
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

// Authenticate attempts to authenticate the given credential using
// registered authenticators.
//
// Returns the authenticated Principal on success.
func (m *Manager) Authenticate(
	ctx context.Context,
	credential core.Credential,
	reqInfo *core.RequestInfo,
) (*core.Principal, error) {
	// Iterate through authenticators, first match wins.
	for _, auth := range m.authenticators {
		if !auth.Supports(credential) {
			continue
		}

		m.log.Debug("authenticating with authenticator",
			zap.String("authenticator", auth.Name()),
		)

		authInfo, err := auth.Authenticate(ctx, credential, reqInfo)
		if err != nil {
			return nil, err
		}

		return m.buildPrincipal(ctx, authInfo)
	}

	// This shouldn't happen with NoneAuthenticator registered, because it
	// accepts everything.
	return nil, fmt.Errorf("no authenticator supports the given credential")
}

// buildPrincipal resolves an authenticated subject into its authorization
// context.
func (m *Manager) buildPrincipal(
	ctx context.Context,
	authInfo *core.AuthInfo,
) (*core.Principal, error) {
	// Anonymous requests do not have an account identity to resolve.
	if authInfo.AuthMethod == "none" {
		return &core.Principal{
			User:        authInfo.Subject.Identifier,
			Groups:      []string{},
			AuthMethod:  authInfo.AuthMethod,
			AuthTime:    time.Now(),
			IsAnonymous: true,
		}, nil
	}

	if m.identityProvider == nil {
		return nil, fmt.Errorf("no identity provider configured")
	}

	ident, err := m.identityProvider.ResolveIdentity(ctx, authInfo.Subject)
	if err != nil {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"identity lookup failed for subject %q from issuer %q: %v",
			authInfo.Subject.Identifier, authInfo.Subject.Issuer, err,
		)
	}

	if ident.Disabled {
		return nil, status.Errorf(
			codes.Unauthenticated,
			"account %q is disabled", ident.Username,
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

// ClientCAs returns the authorities the x509 authenticator verifies client
// certificates against, nil when none is configured.
//
// A TLS server asks for the pool on every handshake, so a reload of the
// authenticator's sources takes effect without a restart.
func (m *Manager) ClientCAs() *x509.CertPool {
	if m.clientCAs == nil {
		return nil
	}

	return m.clientCAs()
}

// metricsCollector provides collected metrics.
type metricsCollector interface {
	Collect() []*commonpb.Metric
}

// Collect gathers the metrics of every authenticator that reports any.
func (m *Manager) Collect() []*commonpb.Metric {
	var out []*commonpb.Metric
	for _, authenticator := range m.authenticators {
		if collector, ok := authenticator.(metricsCollector); ok {
			out = append(out, collector.Collect()...)
		}
	}

	return out
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
