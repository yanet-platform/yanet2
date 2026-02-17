package identity

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
)

// Provider provides access to identities from a specific source.
type Provider interface {
	Name() string
	GetIdentity(ctx context.Context, username string) (Identity, error)
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

// GetIdentity tries each provider until one succeeds.
func (m *CompositeIdentityProvider) GetIdentity(ctx context.Context, username string) (Identity, error) {
	for _, provider := range m.providers {
		identity, err := provider.GetIdentity(ctx, username)
		if err == nil {
			m.log.Debug("identity found",
				zap.String("username", username),
				zap.String("provider", provider.Name()),
			)
			return identity, nil
		}

		if errors.Is(err, ErrIdentityNotFound) {
			continue
		}

		m.log.Warn("provider error",
			zap.String("provider", provider.Name()),
			zap.Error(err),
		)
		return Identity{}, fmt.Errorf("provider %s: %w", provider.Name(), err)
	}

	return Identity{}, ErrIdentityNotFound
}
