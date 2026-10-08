package fwstatepb

import "google.golang.org/protobuf/proto"

// Merge overwrites the settings the update carries and keeps the rest.
//
// A zero port disables its endpoint, so the endpoint address is cleared too.
func (m *SyncConfig) Merge(update *SyncConfig) {
	if update == nil {
		return
	}

	proto.Merge(m, update)
	if update.PortMulticast != nil && update.GetPortMulticast() == 0 {
		m.DstAddrMulticast = nil
	}
	if update.PortUnicast != nil && update.GetPortUnicast() == 0 {
		m.DstAddrUnicast = nil
	}
}
