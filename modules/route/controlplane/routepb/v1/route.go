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
	return commonpb.ValidateModuleName("name", m.GetName())
}

func (m *ShowFIBRequest) Validate() error {
	if err := commonpb.ValidateModuleName("name", m.GetName()); err != nil {
		return err
	}
	if m.GetIpv4Only() && m.GetIpv6Only() {
		return errors.New("ipv4_only and ipv6_only must not both be set")
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
	if m.GetSrcMac().GetAddr()>>48 != 0 {
		return errors.New("src_mac must be an EUI-48 address")
	}
	if m.GetDstMac().GetAddr()>>48 != 0 {
		return errors.New("dst_mac must be an EUI-48 address")
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
	if err := commonpb.ValidateModuleName("module_name", m.GetModuleName()); err != nil {
		return err
	}

	for idx, entry := range m.GetEntries() {
		if entry == nil {
			return fmt.Errorf("entries[%d] is required", idx)
		}
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("entries[%d]: %w", idx, err)
		}
	}

	return nil
}
