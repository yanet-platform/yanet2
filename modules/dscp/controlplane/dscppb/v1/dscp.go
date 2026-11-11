package dscppb

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// MaxFlag mirrors the largest C DSCP marking flag.
const MaxFlag = 2

// MaxMark mirrors the largest six-bit DSCP value of the C marking.
const MaxMark = 63

// UnmarshalJSON accepts the legacy destination keys as aliases for the
// explicit direction names, rejecting a direction specified twice.
func (m *Config) UnmarshalJSON(data []byte) error {
	fields := map[string]jsontext.Value{}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, alias := range []struct{ Legacy, Canonical string }{
		{"prefixes4", "destination_prefixes4"},
		{"prefixes6", "destination_prefixes6"},
	} {
		value, ok := fields[alias.Legacy]
		if !ok {
			continue
		}
		if _, ok := fields[alias.Canonical]; ok {
			return fmt.Errorf("both %q and %q are specified", alias.Legacy, alias.Canonical)
		}
		fields[alias.Canonical] = value
		delete(fields, alias.Legacy)
	}
	encoded, err := json.Marshal(fields, json.Deterministic(true))
	if err != nil {
		return err
	}
	type plainConfig Config
	return json.Unmarshal(encoded, (*plainConfig)(m))
}

func (m *ShowConfigRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

// Validate checks that the request names a config and carries the whole
// config that replaces it.
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

// Validate checks the marking of the config, which may be unset.
func (m *Config) Validate() error {
	if err := m.GetDscpConfig().Validate(); err != nil {
		return fmt.Errorf("dscp_config: %w", err)
	}

	return nil
}

// Validate checks that the request names the config to delete.
func (m *DeleteConfigRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

// Validate checks that the flag and mark values are within their valid
// ranges.
func (m *DscpConfig) Validate() error {
	if m.GetFlag() > MaxFlag {
		return fmt.Errorf("flag %d must be in range 0..%d", m.GetFlag(), MaxFlag)
	}
	if m.GetMark() > MaxMark {
		return fmt.Errorf("mark %d must be in range 0..%d", m.GetMark(), MaxMark)
	}

	return nil
}
