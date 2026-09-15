package trafgenpb

import (
	"errors"
	"fmt"
	"strings"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

func (m *UpdateDeviceRequest) Validate() error {
	if err := validateDeviceName(m.GetName()); err != nil {
		return err
	}
	if err := m.GetDevice().Validate(); err != nil {
		return fmt.Errorf("device: %w", err)
	}
	return nil
}

func (m *ShowConfigRequest) Validate() error {
	return validateDeviceName(m.GetName())
}

func (m *ShowPacketsRequest) Validate() error {
	return validateDeviceName(m.GetName())
}

func (m *UploadPcapRequest) Validate() error {
	return validateDeviceName(m.GetName())
}

func (m *SetRateRequest) Validate() error {
	return validateDeviceName(m.GetName())
}

func validateDeviceName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if strings.IndexByte(name, 0) != -1 {
		return errors.New("name must not contain NUL")
	}
	if len(name) >= commonpb.MaxDeviceNameLen {
		return fmt.Errorf("name must be shorter than %d bytes", commonpb.MaxDeviceNameLen)
	}
	return nil
}
