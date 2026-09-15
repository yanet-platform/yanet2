package plainpb

import (
	"errors"
	"fmt"
	"strings"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

func (m *UpdateDevicePlainRequest) Validate() error {
	if err := validateDeviceName(m.GetName()); err != nil {
		return err
	}
	if err := m.GetDevice().Validate(); err != nil {
		return fmt.Errorf("device: %w", err)
	}
	return nil
}

func (m *ShowDevicePlainRequest) Validate() error {
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
