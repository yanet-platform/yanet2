package operatorpb

import (
	"errors"
	"fmt"
)

func (m *NeighbourEntry) Validate() error {
	if m.GetHardwareAddr() == nil {
		return errors.New("hardware_addr is required")
	}
	if m.GetLinkAddr() == nil {
		return errors.New("link_addr is required")
	}
	if m.GetHardwareAddr().GetAddr()>>48 != 0 {
		return errors.New("hardware_addr must be an EUI-48 address")
	}
	if m.GetLinkAddr().GetAddr()>>48 != 0 {
		return errors.New("link_addr must be an EUI-48 address")
	}

	return nil
}

func (m *UpdateNeighboursRequest) Validate() error {
	for idx, entry := range m.GetEntries() {
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("entries[%d]: %w", idx, err)
		}
	}

	return nil
}
