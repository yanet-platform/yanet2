package l3bpb

import (
	"errors"
	"fmt"

	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
)

// The registry stores names in an 80-byte buffer including its terminator.
const maxNameLength = 79

func validateName(field, name string) error {
	if name == "" {
		return errors.New(field + " is required")
	}
	if len(name) > maxNameLength {
		return fmt.Errorf("%s must be at most %d bytes", field, maxNameLength)
	}
	for idx := range name {
		if name[idx] < 0x20 || name[idx] == 0x7f {
			return fmt.Errorf("%s must contain only printable bytes", field)
		}
	}
	return nil
}

func validateIPNetList(field string, networks []*filterpb.IPNet) error {
	for idx := range networks {
		if networks[idx] == nil {
			return fmt.Errorf("%s[%d] is required", field, idx)
		}
	}
	return nil
}

func validatePortRangeList(field string, ranges []*filterpb.PortRange) error {
	for idx := range ranges {
		if ranges[idx] == nil {
			return fmt.Errorf("%s[%d] is required", field, idx)
		}
	}
	return nil
}

func validateProtoRangeList(field string, ranges []*filterpb.ProtoRange) error {
	for idx := range ranges {
		if ranges[idx] == nil {
			return fmt.Errorf("%s[%d] is required", field, idx)
		}
	}
	return nil
}

// Validate checks the service name and source filter lists.
func (m *VirtualService) Validate() error {
	if err := validateName("name", m.GetName()); err != nil {
		return err
	}
	for idx, rule := range m.GetSourceFilterRules() {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("source_filter_rules[%d]: %w", idx, err)
		}
	}
	return nil
}

// Validate checks source filter lists for missing range messages.
func (m *SourceFilterRule) Validate() error {
	if err := validateIPNetList("net6s", m.GetNet6S()); err != nil {
		return err
	}
	if err := validateIPNetList("net4s", m.GetNet4S()); err != nil {
		return err
	}
	return validatePortRangeList("port_ranges", m.GetPortRanges())
}

// Validate checks destination filter lists for missing range messages.
func (m *DestinationFilterRule) Validate() error {
	if err := validateIPNetList("net6s", m.GetNet6S()); err != nil {
		return err
	}
	if err := validateIPNetList("net4s", m.GetNet4S()); err != nil {
		return err
	}
	return validateProtoRangeList("proto_ranges", m.GetProtoRanges())
}

// Validate checks the module configuration name and destination rules.
func (m *ModuleConfig) Validate() error {
	if err := validateName("name", m.GetName()); err != nil {
		return err
	}
	for idx, rule := range m.GetDestinationFilterRules() {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("destination_filter_rules[%d]: %w", idx, err)
		}
	}
	return nil
}

// Validate checks the required service and its request-only fields.
func (m *CreateServiceRequest) Validate() error {
	service := m.GetService()
	if service == nil {
		return errors.New("service is required")
	}
	if err := service.Validate(); err != nil {
		return fmt.Errorf("service: %w", err)
	}
	return nil
}

// Validate checks the required service and its request-only fields.
func (m *UpdateServiceRequest) Validate() error {
	service := m.GetService()
	if service == nil {
		return errors.New("service is required")
	}
	if err := service.Validate(); err != nil {
		return fmt.Errorf("service: %w", err)
	}
	return nil
}

// Validate checks the service name.
func (m *DeleteServiceRequest) Validate() error {
	return validateName("name", m.GetName())
}

// Validate checks the required configuration and its request-only fields.
func (m *UpdateModuleConfigRequest) Validate() error {
	config := m.GetConfig()
	if config == nil {
		return errors.New("config is required")
	}
	if err := config.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

// Validate checks the service name.
func (m *UpdateRealServerStateRequest) Validate() error {
	return validateName("service", m.GetService())
}

// Validate checks the service name.
func (m *UpdateRealServerWeightRequest) Validate() error {
	return validateName("service", m.GetService())
}

// Validate checks the service name.
func (m *ListSessionsRequest) Validate() error {
	return validateName("service", m.GetService())
}

// Validate checks the service name.
func (m *GetServiceRequest) Validate() error {
	return validateName("name", m.GetName())
}
