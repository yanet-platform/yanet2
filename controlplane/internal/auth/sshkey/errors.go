package sshkey

import "errors"

var (
	// ErrInvalidTokenPrefix is returned when the token does not start
	// with the expected "sshkey " prefix.
	ErrInvalidTokenPrefix = errors.New("token does not have \"sshkey \" prefix")
	// ErrUnsupportedVersion is returned when the token version is not
	// supported.
	ErrUnsupportedVersion = errors.New("unsupported token version")
	// ErrEmptyUsername is returned when the token has an empty username.
	ErrEmptyUsername = errors.New("empty username")
	// ErrMissingTimestamp is returned when the token has a zero
	// timestamp.
	ErrMissingTimestamp = errors.New("missing timestamp")
	// ErrEmptyNonce is returned when the token has an empty nonce.
	ErrEmptyNonce = errors.New("empty nonce")
	// ErrEmptyMethod is returned when the token has an empty method.
	ErrEmptyMethod = errors.New("empty method")
	// ErrEmptySignature is returned when the token has an empty
	// signature.
	ErrEmptySignature = errors.New("empty signature")
	// ErrTimestampOutsideWindow is returned when the token timestamp
	// is outside the allowed time window.
	ErrTimestampOutsideWindow = errors.New("token timestamp outside allowed window")
	// ErrSignatureVerificationFailed is returned when no public key
	// successfully verified the signature.
	ErrSignatureVerificationFailed = errors.New("signature verification failed")
)
