package identity

import (
	"errors"
)

var (
	// ErrIdentityNotFound is returned when a requested identity does not exist.
	ErrIdentityNotFound = errors.New("identity not found")
)

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

func (m Identity) Clone() Identity {
	return Identity{
		Username: m.Username,
		Groups:   append([]string{}, m.Groups...),
		Disabled: m.Disabled,
	}
}
