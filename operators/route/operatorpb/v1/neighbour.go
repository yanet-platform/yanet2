package operatorpb

import (
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/yanet-platform/yanet2/operators/route/neigh"
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

// Validate requires an explicit table and complete neighbour entries.
func (m *SwapNeighboursRequest) Validate() error {
	if m.GetTable() == "" {
		return errors.New("table is required")
	}
	for idx, entry := range m.GetEntries() {
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("entries[%d]: %w", idx, err)
		}
	}
	return nil
}

// ToNeighbourEntries converts a validated snapshot, rejecting malformed next hops.
//
// An invalid entry rejects the entire observation. Repeated next hops retain
// the last entry in the request.
func (m *SwapNeighboursRequest) ToNeighbourEntries() (map[netip.Addr]neigh.NeighbourEntry, error) {
	entries := make(map[netip.Addr]neigh.NeighbourEntry, len(m.GetEntries()))
	for _, entry := range m.GetEntries() {
		address, err := entry.GetNextHop().ToAddr()
		if err != nil {
			return nil, fmt.Errorf("invalid next hop: %w", err)
		}
		entries[address] = neigh.NeighbourEntry{
			NextHop: address,
			HardwareRoute: neigh.HardwareRoute{
				SourceMAC:      entry.GetHardwareAddr().EUI48(),
				DestinationMAC: entry.GetLinkAddr().EUI48(),
				Device:         entry.GetDevice(),
			},
			UpdatedAt: time.Unix(entry.GetUpdatedAt(), 0),
			State:     neigh.NeighbourState(entry.GetState()),
			Priority:  entry.GetPriority(),
		}
	}
	return entries, nil
}
