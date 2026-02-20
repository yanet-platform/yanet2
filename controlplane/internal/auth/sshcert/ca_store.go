package sshcert

import (
	"bufio"
	"bytes"
	"fmt"
	"sync/atomic"

	"golang.org/x/crypto/ssh"
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

// caSnapshot holds an immutable set of CA entries for atomic swap.
type caSnapshot struct {
	entries []CAEntry
}

// CAStore stores trusted certificate authority public keys.
type CAStore struct {
	snapshot atomic.Pointer[caSnapshot]
	loader   Loader
}

// NewCAStore creates a CAStore from an in-memory slice.
func NewCAStore(entries []CAEntry) *CAStore {
	s := &CAStore{}
	s.snapshot.Store(&caSnapshot{entries: entries})

	return s
}

// NewCAStoreFromLoader creates a CAStore by loading CA data from
// the given loader.
func NewCAStoreFromLoader(loader Loader) (*CAStore, error) {
	data, err := loader.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load CA data: %w", err)
	}

	entries, err := parseCAData(data)
	if err != nil {
		return nil, err
	}

	s := &CAStore{loader: loader}
	s.snapshot.Store(&caSnapshot{entries: entries})

	return s, nil
}

// VerifyCA cryptographically verifies that the certificate was signed by a
// trusted CA.
func (m *CAStore) VerifyCA(cert *ssh.Certificate) error {
	snapshot := m.snapshot.Load()

	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			authData := auth.Marshal()

			for _, ca := range snapshot.entries {
				if bytes.Equal(authData, ca.PublicKey.Marshal()) {
					return true
				}
			}

			return false
		},
	}

	if !checker.IsUserAuthority(cert.SignatureKey) {
		return ErrUntrustedCA
	}

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
	if m.loader == nil {
		return nil
	}

	data, err := m.loader.Load()
	if err != nil {
		return fmt.Errorf("failed to reload CA data: %w", err)
	}

	entries, err := parseCAData(data)
	if err != nil {
		return fmt.Errorf("failed to parse CA data: %w", err)
	}

	m.snapshot.Store(&caSnapshot{entries: entries})

	return nil
}

// parseCAData parses CA data in OpenSSH authorized_keys format.
//
// Expected format, one key per line:
//
//	ecdsa-sha2-nistp256 AAAAE2...
//	ecdsa-sha2-nistp256 AAAAE2... secure_20260107
//
// Empty lines and comments (starting with #) are skipped. Keys with
// unsupported types are skipped.
func parseCAData(data []byte) ([]CAEntry, error) {
	var entries []CAEntry

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()

		pubKey, _, _, _, err := ssh.ParseAuthorizedKey(line)
		if err != nil {
			// Skip blank lines, comments, and unparseable lines.
			// TODO: log?
			continue
		}
		if !isKeySupported(pubKey) {
			continue
		}

		entries = append(entries, CAEntry{
			PublicKey: pubKey,
		})
	}

	if len(entries) == 0 {
		return nil, fmt.Errorf("no valid keys found")
	}

	return entries, nil
}

func isKeySupported(pubKey ssh.PublicKey) bool {
	return pubKey.Type() == supportedKeyType
}
