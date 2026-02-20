package sshcert

import (
	"encoding/base64"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// verifySignature verifies that the base64-encoded SSH signature over the
// given data is valid for the certificate's embedded public key.
func verifySignature(
	data []byte,
	signatureB64 string,
	cert *ssh.Certificate,
) error {
	sigBytes, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		return fmt.Errorf("invalid base64 signature: %w", err)
	}

	sig := new(ssh.Signature)
	if err := ssh.Unmarshal(sigBytes, sig); err != nil {
		return fmt.Errorf("invalid SSH signature format: %w", err)
	}

	if err := cert.Key.Verify(data, sig); err != nil {
		return ErrSignatureVerificationFailed
	}

	return nil
}
