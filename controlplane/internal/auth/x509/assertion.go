package x509

import (
	"crypto/x509"
)

// CertificateAssertion carries the client certificate the handshake
// verified, for identity providers that read attributes off it.
type CertificateAssertion struct {
	// Leaf is the verified client certificate.
	Leaf *x509.Certificate
}

// AssertionType identifies the assertion format for provider dispatch.
func (m CertificateAssertion) AssertionType() string {
	return "x509"
}
