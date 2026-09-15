package ynpb

import (
	"errors"
	"fmt"
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

// Validate checks that a backend descriptor has a name and endpoint.
func (m *BackendDesc) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}
	if m.GetEndpoint() == "" {
		return errors.New("endpoint is required")
	}

	return nil
}
