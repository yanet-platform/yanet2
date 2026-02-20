package sshcert

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	// tokenVersion is the current token format version.
	tokenVersion = 1
	// tokenPrefix is the prefix for SSH cert auth tokens in the
	// x-yanet-authentication header.
	tokenPrefix = "sshcert "
)

// Token represents a parsed SSH certificate authentication token.
type Token struct {
	// Version is the token format version (must be 1).
	Version int `json:"version"`
	// Certificate is the base64-encoded SSH certificate.
	Certificate string `json:"certificate"`
	// Timestamp is the token creation time in Unix nanoseconds.
	Timestamp int64 `json:"timestamp"`
	// Nonce is a random value for uniqueness within the timestamp
	// window.
	Nonce string `json:"nonce"`
	// Method is the gRPC method this token is bound to.
	Method string `json:"method"`
	// Signature is the base64-encoded SSH signature over the
	// canonical signed data.
	Signature string `json:"signature"`
}

// parseToken extracts and parses a Token from the raw
// authentication header value.
//
// Expected format: "sshcert <base64-json-payload>".
func parseToken(raw string) (*Token, error) {
	if !strings.HasPrefix(strings.ToLower(raw), tokenPrefix) {
		return nil, ErrInvalidTokenPrefix
	}

	payload := raw[len(tokenPrefix):]

	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 encoding: %w", err)
	}

	var token Token
	if err := json.Unmarshal(decoded, &token); err != nil {
		return nil, fmt.Errorf("invalid JSON payload: %w", err)
	}

	if err := token.validate(); err != nil {
		return nil, err
	}

	return &token, nil
}

// validate checks token fields for correctness.
func (m *Token) validate() error {
	if m.Version != tokenVersion {
		return fmt.Errorf("%w: %d", ErrUnsupportedVersion, m.Version)
	}

	if m.Certificate == "" {
		return ErrEmptyCertificate
	}

	if m.Timestamp == 0 {
		return ErrMissingTimestamp
	}

	if m.Nonce == "" {
		return ErrEmptyNonce
	}

	if m.Method == "" {
		return ErrEmptyMethod
	}

	if m.Signature == "" {
		return ErrEmptySignature
	}

	return nil
}

// canonicalSignedData builds the canonical string that must be
// signed.
//
// Format:
//
//	version={version}\ncertificate={certificate}\n
//	timestamp={timestamp}\nnonce={nonce}\nmethod={method}
func (m *Token) canonicalSignedData() []byte {
	return fmt.Appendf(nil,
		"version=%d\ncertificate=%s\ntimestamp=%d\nnonce=%s\nmethod=%s",
		m.Version,
		m.Certificate,
		m.Timestamp,
		m.Nonce,
		m.Method,
	)
}

// checkTimestamp verifies that the token timestamp is within the
// allowed window relative to the current time.
func (m *Token) checkTimestamp(
	now time.Time,
	window time.Duration,
) error {
	tokenTime := time.Unix(0, m.Timestamp)
	diff := now.Sub(tokenTime)

	if diff < 0 {
		diff = -diff
	}

	if diff > window {
		return fmt.Errorf(
			"%w: diff=%s, window=%s",
			ErrTimestampOutsideWindow, diff, window,
		)
	}

	return nil
}
