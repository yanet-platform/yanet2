package identity

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
)

// Provider provides access to identities from a specific source.
type Provider interface {
	Name() string
	// ResolveIdentity derives an account identity from an authenticated subject.
	ResolveIdentity(
		ctx context.Context,
		subject core.Subject,
	) (Identity, error)
}

// CompositeIdentityProvider implements chain of responsibility pattern.
//
// It tries providers in order and returns the first match.
type CompositeIdentityProvider struct {
	providers []Provider
	log       *zap.Logger
}

type compositeOptions struct {
	Log *zap.Logger
}

// CompositeOption configures CompositeIdentityProvider.
type CompositeOption func(*compositeOptions)

// WithLog sets the logger for the composite provider.
func WithLog(log *zap.Logger) CompositeOption {
	return func(o *compositeOptions) {
		o.Log = log
	}
}

func newCompositeOptions() *compositeOptions {
	return &compositeOptions{
		Log: zap.NewNop(),
	}
}

// NewCompositeIdentityProvider creates a new composite provider.
func NewCompositeIdentityProvider(providers []Provider, options ...CompositeOption) *CompositeIdentityProvider {
	opts := newCompositeOptions()
	for _, o := range options {
		o(opts)
	}

	return &CompositeIdentityProvider{
		providers: providers,
		log:       opts.Log,
	}
}

// Name returns the provider name.
func (m *CompositeIdentityProvider) Name() string {
	return "composite"
}

// ResolveIdentity tries each provider until one resolves the subject.
func (m *CompositeIdentityProvider) ResolveIdentity(
	ctx context.Context,
	subject core.Subject,
) (Identity, error) {
	matchedSubject := false
	for _, provider := range m.providers {
		identity, err := provider.ResolveIdentity(ctx, subject)
		if err == nil {
			m.log.Debug("identity found",
				zap.String("username", identity.Username),
				zap.String("provider", provider.Name()),
			)
			return identity, nil
		}

		if errors.Is(err, ErrSubjectUnsupported) {
			continue
		}

		matchedSubject = true
		if errors.Is(err, ErrIdentityNotFound) {
			continue
		}

		m.log.Warn("provider error",
			zap.String("provider", provider.Name()),
			zap.Error(err),
		)
		return Identity{}, fmt.Errorf("provider %s: %w", provider.Name(), err)
	}

	if !matchedSubject {
		return Identity{}, ErrSubjectUnsupported
	}
	return Identity{}, ErrIdentityNotFound
}
