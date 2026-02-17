package identity

import "errors"

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

var (
	// ErrIdentityNotFound is returned when a requested identity does not exist.
	ErrIdentityNotFound = errors.New("identity not found")
)
