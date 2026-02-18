package core

import (
	"errors"
)

var (
	// ErrInvalidCredentials is returned when credentials are invalid.
	ErrInvalidCredentials = errors.New("invalid credentials")
)
