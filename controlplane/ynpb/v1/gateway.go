package ynpb

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// Validate checks that a registration includes a valid backend descriptor.
func (m *RegisterRequest) Validate() error {
	backend := m.GetBackend()
	if backend == nil {
		return errors.New("backend is required")
	}
	if err := backend.Validate(); err != nil {
		return fmt.Errorf("backend: %w", err)
	}

	return nil
}

// Validate checks that a backend descriptor has a name and a dialable
// endpoint.
//
// The endpoint is either an absolute path (a unix socket the callee may
// gate separately) or a host:port pair the dialer accepts.
func (m *BackendDesc) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}
	endpoint := m.GetEndpoint()
	if endpoint == "" {
		return errors.New("endpoint is required")
	}
	if strings.HasPrefix(endpoint, "/") {
		return nil
	}
	if _, _, err := net.SplitHostPort(endpoint); err != nil {
		return fmt.Errorf("endpoint: %w", err)
	}

	return nil
}
