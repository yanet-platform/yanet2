package sshkey

import (
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// verifySignature verifies that the base64-encoded SSH signature over
// the given data is valid for at least one of the provided public keys.
//
// It tries each key in order and returns nil on the first successful
// verification.
func verifySignature(
	data []byte,
	signatureB64 string,
	publicKeys []ssh.PublicKey,
) error {
	sigBytes, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		return fmt.Errorf("invalid base64 signature: %w", err)
	}

	sig := new(ssh.Signature)
	if err := ssh.Unmarshal(sigBytes, sig); err != nil {
		return fmt.Errorf("invalid SSH signature format: %w", err)
	}

	for _, key := range publicKeys {
		if err := key.Verify(data, sig); err == nil {
			return nil
		}
	}

	return ErrSignatureVerificationFailed
}
