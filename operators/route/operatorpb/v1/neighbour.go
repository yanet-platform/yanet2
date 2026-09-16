package operatorpb

import (
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/yanet-platform/yanet2/operators/route/neigh"
)

// ToNeighbourEntries validates the complete snapshot before returning its entries.
//
// An invalid entry rejects the entire observation. Repeated next hops retain
// the last entry in the request.
func (m *SwapNeighboursRequest) ToNeighbourEntries() (map[netip.Addr]neigh.NeighbourEntry, error) {
	if m.GetTable() == "" {
		return nil, errors.New("table is required")
	}
	entries := make(map[netip.Addr]neigh.NeighbourEntry, len(m.GetEntries()))
	for _, entry := range m.GetEntries() {
		address, err := entry.GetNextHop().ToAddr()
		if err != nil {
			return nil, fmt.Errorf("invalid next hop: %w", err)
		}
		if entry.GetHardwareAddr() == nil || entry.GetLinkAddr() == nil {
			return nil, fmt.Errorf("neighbour %q requires both MAC addresses", address)
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
