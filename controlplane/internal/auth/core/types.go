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

// AuthInfo contains the result of token validation by an authenticator.
//
// It carries only the information that the authenticator can extract from the
// token itself.
type AuthInfo struct {
	// Username is the authenticated username extracted from the token.
	Username string
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
