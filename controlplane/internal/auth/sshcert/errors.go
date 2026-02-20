package sshcert

import "errors"

var (
	// ErrInvalidTokenPrefix is returned when the token does not start with
	// the expected "sshcert " prefix.
	ErrInvalidTokenPrefix = errors.New(`token does not have "sshcert " prefix`)
	// ErrUnsupportedVersion is returned when the token version is not
	// supported.
	ErrUnsupportedVersion = errors.New("unsupported token version")
	// ErrEmptyCertificate is returned when the token has an empty certificate
	// field.
	ErrEmptyCertificate = errors.New("empty certificate")
	// ErrMissingTimestamp is returned when the token has a zero timestamp.
	ErrMissingTimestamp = errors.New("missing timestamp")
	// ErrEmptyNonce is returned when the token has an empty nonce.
	ErrEmptyNonce = errors.New("empty nonce")
	// ErrEmptyMethod is returned when the token has an empty method.
	ErrEmptyMethod = errors.New("empty method")
	// ErrEmptySignature is returned when the token has an empty signature.
	ErrEmptySignature = errors.New("empty signature")
	// ErrTimestampOutsideWindow is returned when the token timestamp is
	// outside the allowed time window.
	ErrTimestampOutsideWindow = errors.New("token timestamp outside allowed window")
	// ErrSignatureVerificationFailed is returned when the signature could not
	// be verified.
	ErrSignatureVerificationFailed = errors.New("signature verification failed")
	// ErrInvalidCertificate is returned when the certificate cannot be parsed.
	ErrInvalidCertificate = errors.New("invalid certificate")
	// ErrUnsupportedKeyType is returned when the certificate key type is not
	// supported.
	ErrUnsupportedKeyType = errors.New("unsupported key type")
	// ErrNotUserCert is returned when the certificate is not a user
	// certificate.
	ErrNotUserCert = errors.New("not a user certificate")
	// ErrCertExpired is returned when the certificate validity period has
	// passed.
	ErrCertExpired = errors.New("certificate expired")
	// ErrCertNotYetValid is returned when the certificate is not yet valid.
	ErrCertNotYetValid = errors.New("certificate not yet valid")
	// ErrEmptyPrincipals is returned when the certificate has no valid
	// principals.
	ErrEmptyPrincipals = errors.New("certificate has no principals")
	// ErrUntrustedCA is returned when the certificate is not signed by a
	// trusted CA.
	ErrUntrustedCA = errors.New("certificate not signed by trusted CA")
	// ErrCertRevoked is returned when the certificate has been revoked via
	// KRL.
	ErrCertRevoked = errors.New("certificate revoked")
)
