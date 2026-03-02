package route

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
	"github.com/yanet-platform/yanet2/modules/route/internal/discovery/neigh"
)

const defaultStaticTable = "static"

// NeighbourService implements the Neighbour service for retrieving and
// managing neighbor information.
type NeighbourService struct {
	routepb.UnimplementedNeighbourServer

	neighTable *neigh.NeighTable
}

// NewNeighbourService creates a new NeighbourService.
func NewNeighbourService(neighTable *neigh.NeighTable) *NeighbourService {
	return &NeighbourService{
		neighTable: neighTable,
	}
}

// List returns neighbors.
//
// If table is empty, returns the merged view.
// If table is set, returns entries from the specified table only.
func (m *NeighbourService) List(
	ctx context.Context,
	req *routepb.ListNeighboursRequest,
) (*routepb.ListNeighboursResponse, error) {
	table := req.GetTable()

	var view neigh.NexthopCacheView
	if table == "" {
		view = m.neighTable.View()
	} else {
		v, ok := m.neighTable.SourceView(table)
		if !ok {
			return nil, fmt.Errorf("table %q not found", table)
		}
		view = v
	}

	entries, size := view.Entries()

	neighbours := make([]*routepb.NeighbourEntry, 0, size)
	for entry := range entries {
		source := entry.Source
		if source == "" {
			source = table
		}

		neighbours = append(
			neighbours,
			&routepb.NeighbourEntry{
				NextHop:      entry.NextHop.String(),
				LinkAddr:     routepb.NewMACAddressEUI48(entry.HardwareRoute.DestinationMAC),
				HardwareAddr: routepb.NewMACAddressEUI48(entry.HardwareRoute.SourceMAC),
				State:        routepb.NeighbourState(entry.State),
				UpdatedAt:    entry.UpdatedAt.Unix(),
				Source:       source,
				Priority:     entry.Priority,
				Device:       entry.HardwareRoute.Device,
			},
		)
	}

	return &routepb.ListNeighboursResponse{
		Neighbours: neighbours,
	}, nil
}

// CreateTable creates a new neighbour table.
func (m *NeighbourService) CreateTable(
	ctx context.Context,
	request *routepb.CreateNeighbourTableRequest,
) (*routepb.CreateNeighbourTableResponse, error) {
	if _, err := m.neighTable.CreateSource(request.GetName(), request.GetDefaultPriority(), false); err != nil {
		return nil, err
	}

	return &routepb.CreateNeighbourTableResponse{}, nil
}

// UpdateTable updates the default priority of an existing neighbour
// table.
func (m *NeighbourService) UpdateTable(
	ctx context.Context,
	request *routepb.UpdateNeighbourTableRequest,
) (*routepb.UpdateNeighbourTableResponse, error) {
	if err := m.neighTable.UpdateSource(request.GetName(), request.GetDefaultPriority()); err != nil {
		return nil, err
	}

	return &routepb.UpdateNeighbourTableResponse{}, nil
}

// RemoveTable removes a user-defined neighbour table.
func (m *NeighbourService) RemoveTable(
	ctx context.Context,
	request *routepb.RemoveNeighbourTableRequest,
) (*routepb.RemoveNeighbourTableResponse, error) {
	if err := m.neighTable.DeleteSource(request.GetName()); err != nil {
		return nil, err
	}

	return &routepb.RemoveNeighbourTableResponse{}, nil
}

// ListTables returns metadata about all registered neighbour tables.
func (m *NeighbourService) ListTables(
	ctx context.Context,
	request *routepb.ListNeighbourTablesRequest,
) (*routepb.ListNeighbourTablesResponse, error) {
	sources := m.neighTable.ListSources()

	tables := make([]*routepb.NeighbourTableInfo, 0, len(sources))
	for _, src := range sources {
		tables = append(tables, &routepb.NeighbourTableInfo{
			Name:            src.Name,
			DefaultPriority: src.DefaultPriority,
			EntryCount:      int64(src.EntryCount),
			BuiltIn:         src.BuiltIn,
		})
	}

	return &routepb.ListNeighbourTablesResponse{
		Tables: tables,
	}, nil
}

// UpdateNeighbours inserts or updates one or more neighbour entries in
// the specified table.
func (m *NeighbourService) UpdateNeighbours(
	ctx context.Context,
	request *routepb.UpdateNeighboursRequest,
) (*routepb.UpdateNeighboursResponse, error) {
	table := request.GetTable()
	if table == "" {
		table = defaultStaticTable
	}

	entries := make([]neigh.NeighbourEntry, 0, len(request.GetEntries()))
	for _, e := range request.GetEntries() {
		addr, err := netip.ParseAddr(e.GetNextHop())
		if err != nil {
			return nil, fmt.Errorf("invalid nexthop %q: %w", e.GetNextHop(), err)
		}

		entries = append(entries, neigh.NeighbourEntry{
			NextHop: addr,
			HardwareRoute: neigh.HardwareRoute{
				SourceMAC:      e.GetHardwareAddr().EUI48(),
				DestinationMAC: e.GetLinkAddr().EUI48(),
				Device:         e.GetDevice(),
			},
			UpdatedAt: time.Now(),
			State:     neigh.NeighbourStatePermanent,
			Priority:  e.GetPriority(),
		})
	}

	if err := m.neighTable.Add(table, entries); err != nil {
		return nil, err
	}

	return &routepb.UpdateNeighboursResponse{}, nil
}

// RemoveNeighbours deletes one or more neighbour entries from the
// specified table.
func (m *NeighbourService) RemoveNeighbours(
	ctx context.Context,
	request *routepb.RemoveNeighboursRequest,
) (*routepb.RemoveNeighboursResponse, error) {
	table := request.GetTable()
	if table == "" {
		table = defaultStaticTable
	}

	addrs := make([]netip.Addr, 0, len(request.GetNextHops()))
	for _, hop := range request.GetNextHops() {
		addr, err := netip.ParseAddr(hop)
		if err != nil {
			return nil, fmt.Errorf("invalid next_hop %q: %w", hop, err)
		}
		addrs = append(addrs, addr)
	}

	if err := m.neighTable.Remove(table, addrs); err != nil {
		return nil, err
	}

	return &routepb.RemoveNeighboursResponse{}, nil
}
