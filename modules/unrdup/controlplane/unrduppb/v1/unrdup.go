package unrduppb

import (
	"errors"
	"fmt"
	"strings"
)

// MaxModuleNameLen is the largest module name that fits in the C module-name
// buffer.
const MaxModuleNameLen = 79

const maxPort = 65535

func (m *ShowConfigRequest) Validate() error {
	return validateConfigName(m.GetName())
}

func (m *UpdateConfigRequest) Validate() error {
	if err := validateConfigName(m.GetName()); err != nil {
		return err
	}
	if m.GetConfig() == nil {
		return errors.New("config is required")
	}
	if err := m.GetConfig().Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	return nil
}

func (m *DeleteConfigRequest) Validate() error {
	return validateConfigName(m.GetName())
}

func (m *Config) Validate() error {
	for idx, service := range m.GetServices() {
		if err := service.Validate(); err != nil {
			return fmt.Errorf("services[%d]: %w", idx, err)
		}
	}

	return nil
}

func (m *Service) Validate() error {
	if len(m.GetPeers()) == 0 {
		return errors.New("peers must contain at least one peer")
	}
	if len(m.GetEndpoints()) == 0 {
		return errors.New("endpoints must contain at least one endpoint")
	}
	for idx, endpoint := range m.GetEndpoints() {
		if err := endpoint.Validate(); err != nil {
			return fmt.Errorf("endpoints[%d]: %w", idx, err)
		}
	}

	return nil
}

func (m *Endpoint) Validate() error {
	port := m.GetPort()
	if port == 0 || port > maxPort {
		return fmt.Errorf("port %d must be in range 1..%d", port, maxPort)
	}

	switch m.GetProtocol() {
	case Protocol_PROTOCOL_TCP, Protocol_PROTOCOL_UDP:
		return nil
	default:
		return fmt.Errorf("protocol unknown value %d", m.GetProtocol())
	}
}

func validateConfigName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if strings.IndexByte(name, 0) != -1 {
		return errors.New("name must not contain NUL")
	}
	if len(name) > MaxModuleNameLen {
		return fmt.Errorf("name must be shorter than %d bytes", MaxModuleNameLen+1)
	}

	return nil
}
