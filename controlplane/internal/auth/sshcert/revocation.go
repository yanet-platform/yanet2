package sshcert

import (
	"fmt"
	"sync/atomic"

	"github.com/stripe/krl"
	"golang.org/x/crypto/ssh"
)

// RevocationChecker checks whether a certificate has been revoked.
type RevocationChecker interface {
	// IsRevoked returns ErrCertRevoked if the certificate is
	// revoked, nil otherwise.
	IsRevoked(cert *ssh.Certificate) error
	// Reload refreshes the revocation data from the source.
	// On error the old data is preserved.
	Reload() error
}

// nopRevocationChecker is a no-op implementation that never revokes
// anything. Used when KRL is not configured.
type nopRevocationChecker struct{}

// NewNopRevocationChecker returns a RevocationChecker that never
// considers any certificate revoked.
func NewNopRevocationChecker() RevocationChecker {
	return &nopRevocationChecker{}
}

func (m *nopRevocationChecker) IsRevoked(*ssh.Certificate) error {
	return nil
}

func (m *nopRevocationChecker) Reload() error {
	return nil
}

// KRLRevocationChecker checks certificates against an OpenSSH KRL.
type KRLRevocationChecker struct {
	krlData atomic.Pointer[krl.KRL]
	loader  Loader
}

// NewKRLRevocationChecker creates a RevocationChecker from a parsed
// KRL.
func NewKRLRevocationChecker(k *krl.KRL) *KRLRevocationChecker {
	m := &KRLRevocationChecker{}
	m.krlData.Store(k)

	return m
}

// NewKRLRevocationCheckerFromLoader creates a RevocationChecker by
// loading KRL data from the given loader.
func NewKRLRevocationCheckerFromLoader(
	loader Loader,
) (*KRLRevocationChecker, error) {
	data, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load KRL data: %w", err)
	}

	k, err := krl.ParseKRL(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse KRL: %w", err)
	}

	m := &KRLRevocationChecker{loader: loader}
	m.krlData.Store(k)

	return m, nil
}

// IsRevoked checks if the given certificate has been revoked.
func (m *KRLRevocationChecker) IsRevoked(cert *ssh.Certificate) error {
	k := m.krlData.Load()
	if k.IsRevoked(cert) {
		return ErrCertRevoked
	}

	return nil
}

// Reload reloads KRL data from the loader.
//
// On error the old data is preserved.
func (m *KRLRevocationChecker) Reload() error {
	if m.loader == nil {
		return nil
	}

	data, err := m.loader.Load()
	if err != nil {
		return fmt.Errorf("failed to reload KRL data: %w", err)
	}

	k, err := krl.ParseKRL(data)
	if err != nil {
		return fmt.Errorf("failed to parse KRL: %w", err)
	}

	m.krlData.Store(k)

	return nil
}
