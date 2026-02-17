package core

import (
	"strings"
	"time"
)

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

// Identity represents a user account with authentication and authorization
// details.
type Identity struct {
	// Username is the unique user identifier.
	Username string
	// Groups is the list of groups this user belongs to.
	Groups []string
	// Disabled indicates if the account is disabled.
	Disabled bool
}

// Permission represents an access control rule pattern.
type Permission struct {
	// Pattern is the gRPC method pattern (e.g., "/routepb.RouteService/*").
	Pattern string
}

// Match checks if the permission pattern matches the given gRPC method.
func (m *Permission) Match(fullMethod string) bool {
	// TODO: exact match only for now, wildcard matching in future phases.
	return strings.EqualFold(m.Pattern, fullMethod)
}
