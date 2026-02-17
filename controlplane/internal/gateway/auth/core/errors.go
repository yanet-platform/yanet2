package core

import (
	"errors"
)

var (
	// ErrAccountDisabled is returned when attempting to authenticate with a disabled account.
	ErrAccountDisabled = errors.New("account is disabled")

	// ErrInvalidCredentials is returned when credentials are invalid.
	ErrInvalidCredentials = errors.New("invalid credentials")
)
