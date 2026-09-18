package dscppb

import (
	"errors"
	"fmt"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// MaxFlag mirrors the largest C DSCP marking flag.
const MaxFlag = 2

// MaxMark mirrors the largest six-bit DSCP value of the C marking.
const MaxMark = 63

func (m *ShowConfigRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

func (m *AddPrefixesRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

func (m *RemovePrefixesRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

// Validate checks that the request names the config to delete.
func (m *DeleteConfigRequest) Validate() error {
	return commonpb.ValidateModuleName("name", m.GetName())
}

func (m *SetDscpMarkingRequest) Validate() error {
	if err := commonpb.ValidateModuleName("name", m.GetName()); err != nil {
		return err
	}

	if m.GetDscpConfig() == nil {
		return errors.New("dscp_config is required")
	}
	if err := m.GetDscpConfig().Validate(); err != nil {
		return fmt.Errorf("dscp_config: %w", err)
	}

	return nil
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
