package x509

import "errors"

var (
	// ErrNoVerifiedCertificate is returned when the connection carries no
	// client certificate chain verified in the handshake.
	ErrNoVerifiedCertificate = errors.New("no verified client certificate")
	// ErrCertificateNotYetValid is returned when the certificate validity
	// period has not started.
	ErrCertificateNotYetValid = errors.New("certificate not yet valid")
	// ErrCertificateExpired is returned when the certificate validity period
	// has passed.
	ErrCertificateExpired = errors.New("certificate expired")
	// ErrCertificateRevoked is returned when a revocation list names the
	// certificate.
	ErrCertificateRevoked = errors.New("certificate revoked")
	// ErrNoSubjectName is returned when the certificate has neither a common
	// name nor exactly one SPIFFE identity to serve as the login.
	ErrNoSubjectName = errors.New("certificate has neither a common name nor a single spiffe id")
	// ErrNoAuthorities is returned when a bundle holds no certificate.
	ErrNoAuthorities = errors.New("no certificates found")
	// ErrUntrustedRevocationList is returned when no trusted authority signed
	// the revocation list.
	ErrUntrustedRevocationList = errors.New("revocation list not signed by a trusted authority")
)
