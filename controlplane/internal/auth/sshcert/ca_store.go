package sshcert

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"slices"
	"sync/atomic"

	"golang.org/x/crypto/ssh"

	"github.com/yanet-platform/yanet2/controlplane/internal/auth/core"
	"github.com/yanet-platform/yanet2/controlplane/internal/auth/loader"
)

const (
	// supportedKeyType is the only supported SSH key type.
	supportedKeyType = "ecdsa-sha2-nistp256"
)

// CAVerifier verifies that a certificate was signed by a trusted CA
// and supports periodic reload of CA data.
type CAVerifier interface {
	// VerifyCA checks the certificate against trusted CAs.
	VerifyCA(cert *ssh.Certificate) error
	// Reload refreshes CA data from the source.
	//
	// On error the old data is preserved.
	Reload() error
}

// CAEntry represents a single trusted certificate authority.
type CAEntry struct {
	// PublicKey is the parsed CA public key.
	PublicKey ssh.PublicKey
}

// CAStore stores trusted certificate authority public keys.
type CAStore struct {
	snapshot atomic.Pointer[[]CAEntry]
	source   loader.Loader
}

type policyCAKey struct{}

type signerAssertion struct {
	Key ssh.PublicKey
}

func (m signerAssertion) AssertionType() string { return "sshcert-signer" }

// WithPolicyCA selects the immutable SSH trust of one RPC admission.
func WithPolicyCA(ctx context.Context, store *CAStore) context.Context {
	return context.WithValue(ctx, policyCAKey{}, store)
}

// ParsePolicyCA loads the complete managed CA bundle from verified bytes.
//
// An absent bundle deliberately trusts nobody. A present malformed bundle
// must not activate a partially trusted set.
func ParsePolicyCA(data []byte) (*CAStore, error) {
	entries, err := parseCAData(data, true)
	if err != nil {
		return nil, err
	}
	return NewCAStore(entries), nil
}

// TrustsSigner reports whether the verified signer remains trusted.
func (m *CAStore) TrustsSigner(assertion core.VerifiedAssertion) bool {
	signer, ok := assertion.(signerAssertion)
	return ok && m.trusts(signer.Key)
}

func (m *CAStore) trusts(key ssh.PublicKey) bool {
	if key == nil {
		return false
	}
	encoded := key.Marshal()
	return slices.ContainsFunc(*m.snapshot.Load(), func(entry CAEntry) bool {
		return bytes.Equal(encoded, entry.PublicKey.Marshal())
	})
}

// NewCAStore creates a CAStore from an in-memory slice.
func NewCAStore(entries []CAEntry) *CAStore {
	m := &CAStore{}
	m.snapshot.Store(&entries)

	return m
}

// NewCAStoreFromLoader creates a CAStore by loading CA data from
// the given loader.
func NewCAStoreFromLoader(source loader.Loader) (*CAStore, error) {
	data, err := source.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load CA data: %w", err)
	}

	entries, err := parseCAData(data, false)
	if err != nil {
		return nil, err
	}

	m := &CAStore{source: source}
	m.snapshot.Store(&entries)

	return m, nil
}

// VerifyCA cryptographically verifies that the certificate was signed by a
// trusted CA.
func (m *CAStore) VerifyCA(cert *ssh.Certificate) error {
	if !m.trusts(cert.SignatureKey) {
		return ErrUntrustedCA
	}
	checker := ssh.CertChecker{}
	principal, err := extractPrincipal(cert)
	if err != nil {
		return err
	}

	if err := checker.CheckCert(principal, cert); err != nil {
		return fmt.Errorf("%w: %v", ErrUntrustedCA, err)
	}

	return nil
}

// Reload reloads CA data from the loader.
//
// On error the old data is preserved.
func (m *CAStore) Reload() error {
	if m.source == nil {
		return nil
	}

	data, err := m.source.Load()
	if err != nil {
		return fmt.Errorf("failed to reload CA data: %w", err)
	}

	entries, err := parseCAData(data, false)
	if err != nil {
		return fmt.Errorf("failed to parse CA data: %w", err)
	}

	m.snapshot.Store(&entries)

	return nil
}

// parseCAData rejects malformed entries in managed bundles only.
func parseCAData(data []byte, strict bool) ([]CAEntry, error) {
	if strict && data == nil {
		return nil, nil
	}
	var entries []CAEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		key, _, options, _, err := ssh.ParseAuthorizedKey(line)
		if err != nil || !isKeySupported(key) || (strict && len(options) != 0) {
			if strict {
				return nil, fmt.Errorf("managed SSH CA bundle requires bare ECDSA P-256 keys")
			}
			continue
		}
		entries = append(entries, CAEntry{PublicKey: key})
	}
	if err := scanner.Err(); strict && err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no valid keys found")
	}
	return entries, nil
}

func isKeySupported(pubKey ssh.PublicKey) bool {
	return pubKey.Type() == supportedKeyType
}
