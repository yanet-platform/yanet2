package routepb

import (
	"errors"
	"fmt"
	"strings"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
)

// MaxCounterNameLen is the longest counter name that fits in the C counter-name buffer.
const MaxCounterNameLen = 127

const nexthopCounterPrefix = "nexthop_"

func (m *DeleteConfigRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}

func (m *ShowFIBRequest) Validate() error {
	if m.GetName() == "" {
		return errors.New("name is required")
	}

	return nil
}

func (m *FIBNexthop) Validate() error {
	if m.GetSrcMac() == nil {
		return errors.New("src_mac is required")
	}
	if m.GetDstMac() == nil {
		return errors.New("dst_mac is required")
	}

	device := m.GetDevice()
	if device == "" {
		return errors.New("device is required")
	}
	if strings.IndexByte(device, 0) != -1 {
		return errors.New("device must not contain NUL")
	}
	if len(device) >= commonpb.MaxDeviceNameLen {
		return fmt.Errorf("device must be shorter than %d bytes", commonpb.MaxDeviceNameLen)
	}

	counter := m.GetCounter()
	if counter == "" {
		return nil
	}
	if !strings.HasPrefix(counter, nexthopCounterPrefix) {
		return fmt.Errorf("counter must start with %q", nexthopCounterPrefix)
	}
	if strings.IndexByte(counter, 0) != -1 {
		return errors.New("counter must not contain NUL")
	}
	if len(counter) > MaxCounterNameLen {
		return fmt.Errorf("counter must be shorter than %d bytes", MaxCounterNameLen+1)
	}

	return nil
}

func (m *FIBEntry) Validate() error {
	for idx, nexthop := range m.GetNexthops() {
		if err := nexthop.Validate(); err != nil {
			return fmt.Errorf("nexthops[%d]: %w", idx, err)
		}
	}

	return nil
}

func (m *UpdateFIBRequest) Validate() error {
	if m.GetModuleName() == "" {
		return errors.New("module_name is required")
	}

	for idx, entry := range m.GetEntries() {
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("entries[%d]: %w", idx, err)
		}
	}

	return nil
}
