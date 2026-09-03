package core

import (
	"crypto/tls"
)

// Credential is what a request presents for authentication: the token from
// its metadata and the state of the TLS connection it arrived on.
//
// Either part may be absent. A request without a token has an empty Token,
// a request over plaintext has a nil TLS.
type Credential struct {
	// Token is the value of the authentication metadata header, empty when
	// absent.
	Token string
	// TLS is the connection state of a TLS transport, nil on plaintext.
	TLS *tls.ConnectionState
}
