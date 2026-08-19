package core

import (
	"time"
)

// RequestInfo contains request metadata needed for authentication.
//
// Some authenticators (e.g. SSH key) need to verify method binding,
// so the actual gRPC method is passed here.
type RequestInfo struct {
	// FullMethod is the full gRPC method name, e.g.
	// "/routepb.RouteService/InsertRoute".
	FullMethod string
}

// VerifiedAssertion marks credential attributes whose authenticity has been
// established.
//
// Implementations must only be returned after the carrying credential has been
// fully validated. Verification does not make an attribute an authorization
// decision; an identity provider must still interpret it.
type VerifiedAssertion interface {
	// AssertionType identifies the assertion format for provider dispatch.
	AssertionType() string
}

// Subject identifies the entity proven by a credential.
//
// The authority namespace and its stable identifier form the subject key.
// Account lookup names and verified attributes are auxiliary data and are not
// part of that key.
type Subject struct {
	// Issuer scopes the identifier to the authority that assigned it.
	//
	// For OIDC this is the exact `iss` value. Local authenticators use `local`.
	Issuer string
	// Identifier is the stable value assigned within the issuer namespace.
	//
	// For OIDC this is `sub`; it need not be human-readable or usable as a
	// system account name.
	Identifier string
	// Login is an optional account lookup name for file, NSS, or LDAP providers.
	//
	// It may be mutable and must not be treated as a globally stable subject
	// identifier.
	Login string
	// Assertion contains optional verified attributes from the credential.
	//
	// A claims-backed identity provider may interpret them, while lookup-based
	// providers can ignore them.
	Assertion VerifiedAssertion
}

// NewLocalSubject creates a local subject with one value serving as both its
// stable identifier and account lookup name.
func NewLocalSubject(login string) Subject {
	return Subject{
		Issuer:     "local",
		Identifier: login,
		Login:      login,
	}
}

// AuthInfo contains the result of token validation by an authenticator.
//
// It carries the authenticated subject separately from the method that proved
// it.
type AuthInfo struct {
	// Subject is the entity established by the authenticator.
	Subject Subject
	// AuthMethod is the authentication method used.
	AuthMethod string
}

// Principal represents an authenticated identity with their authorization
// context.
//
// It is carried in the request context and used for authorization and audit
// logging.
type Principal struct {
	// User is the authenticated username.
	User string
	// Groups is the list of group memberships for RBAC.
	Groups []string
	// AuthMethod is the authentication method used.
	AuthMethod string
	// AuthTime is when the authentication occurred.
	AuthTime time.Time
	// IsAnonymous indicates if this is an unauthenticated/anonymous principal.
	IsAnonymous bool
}
