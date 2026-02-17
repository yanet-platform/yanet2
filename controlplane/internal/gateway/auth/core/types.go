package core

import (
	"fmt"
	"time"

	"github.com/gobwas/glob"
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

// Permission represents an access control rule with compiled glob matcher.
type Permission struct {
	// pattern is the compiled glob matcher for the permission pattern.
	pattern glob.Glob
}

// NewPermission creates a Permission with compiled glob pattern.
func NewPermission(pattern string) (*Permission, error) {
	compiled, err := glob.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid permission pattern %q: %w", pattern, err)
	}

	return &Permission{
		pattern: compiled,
	}, nil
}

// Match checks if the permission pattern matches the given gRPC method.
func (m *Permission) Match(fullMethod string) bool {
	return m.pattern.Match(fullMethod)
}
