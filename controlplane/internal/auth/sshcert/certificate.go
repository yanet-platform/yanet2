package sshcert

import (
	"encoding/base64"
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"
)

// parseCertificate decodes a base64-encoded SSH certificate and
// returns the parsed certificate.
func parseCertificate(certB64 string) (*ssh.Certificate, error) {
	certBytes, err := base64.StdEncoding.DecodeString(certB64)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid base64: %v", ErrInvalidCertificate, err)
	}

	pubKey, err := ssh.ParsePublicKey(certBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCertificate, err)
	}

	cert, ok := pubKey.(*ssh.Certificate)
	if !ok {
		return nil, fmt.Errorf("%w: not a certificate", ErrInvalidCertificate)
	}

	return cert, nil
}

// checkKeyType verifies that the certificate's embedded key is
// ecdsa-sha2-nistp256.
func checkKeyType(cert *ssh.Certificate) error {
	if cert.Key.Type() != supportedKeyType {
		return fmt.Errorf(
			"%w: got %q, expected %q",
			ErrUnsupportedKeyType,
			cert.Key.Type(),
			supportedKeyType,
		)
	}

	return nil
}

// checkCertType verifies that the certificate is a user
// certificate.
func checkCertType(cert *ssh.Certificate) error {
	if cert.CertType != ssh.UserCert {
		return ErrNotUserCert
	}

	return nil
}

// checkValidity verifies that the certificate is valid at the
// given time.
//
// ValidAfter and ValidBefore are Unix timestamps (seconds).
// A zero value means "no restriction".
func checkValidity(cert *ssh.Certificate, now time.Time) error {
	unixNow := uint64(now.Unix())

	if cert.ValidAfter != 0 && unixNow < cert.ValidAfter {
		return fmt.Errorf(
			"%w: not valid until %s",
			ErrCertNotYetValid,
			time.Unix(int64(cert.ValidAfter), 0).UTC(),
		)
	}

	if cert.ValidBefore != 0 && unixNow >= cert.ValidBefore {
		return fmt.Errorf(
			"%w: expired at %s",
			ErrCertExpired,
			time.Unix(int64(cert.ValidBefore), 0).UTC(),
		)
	}

	return nil
}

// extractPrincipal returns the first principal from the
// certificate's ValidPrincipals list.
func extractPrincipal(cert *ssh.Certificate) (string, error) {
	if len(cert.ValidPrincipals) == 0 {
		return "", ErrEmptyPrincipals
	}

	return cert.ValidPrincipals[0], nil
}
