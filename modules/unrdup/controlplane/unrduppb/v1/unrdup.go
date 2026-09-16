package unrduppb

import (
	"errors"
	"fmt"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

const maxPort = 65535

func (m *ShowConfigRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

func (m *UpdateConfigRequest) Validate() error {
	if err := commonpb.ValidateModuleName("name", m.GetName()); err != nil {
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
	return commonpb.ValidateModuleName("name", m.GetName())
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
