package ynpb

import (
	"errors"
	"fmt"
	"strings"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

func (m *DeleteDeviceRequest) Validate() error {
	name := m.GetName()
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
