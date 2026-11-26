package auth

import (
	"context"
	"crypto/x509"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
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
	authenticators     []core.Authenticator
	closeOnce          sync.Once
	active             atomic.Pointer[policySnapshot]
	managedPolicy      *ManagedPolicyConfig
	policyReloadErrors atomic.Uint64
	clientCAs          func() *x509.CertPool
	mode               Mode
	streamsMutex       sync.Mutex
	streams            map[*streamControl]admittedStream
	// admissions holds, by id, the calls Admit let in that no server has
	// taken yet.
	admissions sync.Map
	log        *zap.Logger
}

// NewManager creates a new auth Manager.
func NewManager(cfg *Config, options ...ManagerOption) (*Manager, error) {
	opts := newManagerOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid authentication policy: %w", err)
	}
	mode := cfg.EffectiveMode()
	if cfg.Disabled != nil {
		log.Warn("gateway.auth.disabled is deprecated; use gateway.auth.mode",
			zap.String("mode", string(mode)),
		)
	}

	m := &Manager{
		authenticators: []core.Authenticator{},
		mode:           mode,
		log:            log,
	}

	// Disabled mode admits every caller anonymously without checking credentials.
	if m.mode == ModeDisabled {
		log.Warn("authentication is disabled; requests are anonymous with full permissions")
		m.authenticators = append(
			m.authenticators, none.NewNoneAuthenticator(),
		)
		return m, nil
	}

	if m.mode == ModeEnabled {
		if err := m.configureAuthorization(cfg); err != nil {
			return nil, err
		}
	}

	if err := m.configureAuthenticators(cfg); err != nil {
		m.Close()
		return nil, err
	}
	return m, nil
}

// Close stops background refresh owned by the manager's authenticators.
//
// The gateway separately stops RunPolicyReload using its run context.
func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		for _, authenticator := range m.authenticators {
			if refresher, ok := authenticator.(interface{ Close() }); ok {
				refresher.Close()
			}
		}
	})
}

func (m *Manager) configureAuthorization(cfg *Config) error {
	if cfg.ManagedPolicy != nil {
		configuration := *cfg.ManagedPolicy
		if configuration.SSHTrust && !slices.ContainsFunc(cfg.Authenticators, func(entry AuthenticatorConfig) bool {
			return entry.Type == "sshcert"
		}) {
			return fmt.Errorf("managed SSH trust requires the sshcert authenticator")
		}
		m.managedPolicy = &configuration
		m.streams = map[*streamControl]admittedStream{}
		initial, err := readInitialPolicy(configuration)
		if err != nil {
			return fmt.Errorf("load initial gateway policy: %w", err)
		}
		initial.Activated = time.Now()
		m.active.Store(initial)
		return nil
	}

	var identityProviders []identity.Provider
	for _, providerCfg := range cfg.IdentityProviders {
		switch providerCfg.Type {
		case "file":
			fileProvider, err := identity.NewIdentityProviderFromFile(
				providerCfg.Path,
			)
			if err != nil {
				return fmt.Errorf(
					"failed to create file identity provider: %w", err,
				)
			}
			identityProviders = append(identityProviders, fileProvider)
			m.log.Info("registered identity provider",
				zap.String("type", "file"),
				zap.String("path", providerCfg.Path),
			)
		default:
			return fmt.Errorf(
				"unknown identity provider type: %q", providerCfg.Type,
			)
		}
	}

	if len(identityProviders) == 0 {
		return fmt.Errorf("no identity providers configured")
	}

	identityProvider := identity.NewCompositeIdentityProvider(
		identityProviders,
		identity.WithLog(m.log),
	)

	// Create FilePermissionStore.
	permissionStore, err := permission.NewFilePermissionStore(
		cfg.PermissionsPath,
	)
	if err != nil {
		return fmt.Errorf(
			"failed to create permission store: %w", err,
		)
	}
	m.log.Info("loaded permissions",
		zap.String("path", cfg.PermissionsPath),
	)

	// Create RBACAuthorizer.
	m.active.Store(&policySnapshot{
		IdentityProvider: identityProvider,
		Authorizer:       rbac.NewRBACAuthorizer(permissionStore, rbac.WithLog(m.log)),
	})
	return nil
}

func (m *Manager) configureAuthenticators(cfg *Config) error {
	log := m.log
	factories := map[string]AuthenticatorFactory{
		"basic": basic.NewFromConfig,
		"sshkey": func(rawCfg *yaml.Node) (core.Authenticator, error) {
			return sshkey.NewFromConfig(rawCfg, sshkey.WithLog(log))
		},
		"sshcert": func(rawCfg *yaml.Node) (core.Authenticator, error) {
			if m.managedPolicy != nil && m.managedPolicy.SSHTrust {
				return sshcert.NewManagedFromConfig(rawCfg, sshcert.WithLog(log))
			}
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
			return fmt.Errorf("unknown authenticator type: %q", entry.Type)
		}

		auth, err := factory(&entry.Config)
		if err != nil {
			return fmt.Errorf("failed to init %q authenticator: %w", entry.Type, err)
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

	return nil
}

// Authenticate attempts to authenticate the given credential using
// registered authenticators.
//
// Verification remains strict in audit mode; only RPC admission may fall back
// to an anonymous identity after a verification failure.
func (m *Manager) Authenticate(
	ctx context.Context,
	credential core.Credential,
	reqInfo *core.RequestInfo,
) (*core.Principal, error) {
	principal, _, err := m.authenticate(ctx, m.policyFor(ctx), credential, reqInfo)
	return principal, err
}

func (m *Manager) authenticate(
	ctx context.Context,
	policy *policySnapshot,
	credential core.Credential,
	reqInfo *core.RequestInfo,
) (*core.Principal, *core.AuthInfo, error) {
	if policy != nil && policy.SSHTrust != nil {
		ctx = sshcert.WithPolicyCA(ctx, policy.SSHTrust)
	}
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
			return nil, nil, err
		}
		if m.mode == ModeAudit && authInfo.AuthMethod == "none" && credential.Token != "" {
			return nil, nil, status.Error(codes.Unauthenticated, "unsupported credential")
		}

		principal, err := m.buildPrincipal(ctx, policy, authInfo)
		return principal, authInfo, err
	}

	// This shouldn't happen with NoneAuthenticator registered, because it
	// accepts everything.
	return nil, nil, fmt.Errorf("no authenticator supports the given credential")
}

// buildPrincipal resolves an authenticated subject into its authorization
// context.
func (m *Manager) buildPrincipal(
	ctx context.Context,
	policy *policySnapshot,
	authInfo *core.AuthInfo,
) (*core.Principal, error) {
	// Anonymous callers and audit subjects need no account lookup.
	if authInfo.AuthMethod == "none" || m.mode == ModeAudit {
		return &core.Principal{
			User:        authInfo.Subject.Identifier,
			Groups:      []string{},
			AuthMethod:  authInfo.AuthMethod,
			AuthTime:    time.Now(),
			IsAnonymous: authInfo.AuthMethod == "none",
		}, nil
	}

	if policy == nil {
		return nil, fmt.Errorf("no identity provider configured")
	}

	ident, err := policy.IdentityProvider.ResolveIdentity(ctx, authInfo.Subject)
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

// Mode returns the policy the manager applies, with an omitted mode already
// resolved from the deprecated input or to disabled.
func (m *Manager) Mode() Mode {
	return m.mode
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

// Collect reports policy activation and authenticator metrics.
func (m *Manager) Collect() []*commonpb.Metric {
	out := m.collectPolicy()
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
	return m.policyFor(ctx).Authorize(ctx, principal, fullMethod)
}

type policyContextKey struct{}

func (m *Manager) policyFor(ctx context.Context) *policySnapshot {
	if snapshot, ok := ctx.Value(policyContextKey{}).(*policySnapshot); ok {
		return snapshot
	}
	return m.active.Load()
}
