package sshcert

import (
	"errors"

	"golang.org/x/crypto/ssh"
)

// CompositeCAStore wraps multiple independent CAStore instances.
//
// VerifyCA succeeds if any underlying store trusts the certificate.
type CompositeCAStore struct {
	stores []*CAStore
}

// NewCompositeCAStore creates a CompositeCAStore from the given CA stores.
func NewCompositeCAStore(stores []*CAStore) *CompositeCAStore {
	return &CompositeCAStore{stores: stores}
}

// VerifyCA succeeds if at least one underlying store trusts the
// certificate's CA.
func (m *CompositeCAStore) VerifyCA(cert *ssh.Certificate) error {
	for _, store := range m.stores {
		if err := store.VerifyCA(cert); err == nil {
			return nil
		}
	}

	return ErrUntrustedCA
}

// Reload refreshes each underlying store.
func (m *CompositeCAStore) Reload() error {
	errs := make([]error, 0)
	for _, store := range m.stores {
		if err := store.Reload(); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
