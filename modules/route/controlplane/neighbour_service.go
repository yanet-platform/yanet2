package route

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
	"github.com/yanet-platform/yanet2/modules/route/internal/discovery/neigh"
)

const defaultStaticTable = "static"

// NeighbourService implements the Neighbour service for retrieving and
// managing neighbor information.
type NeighbourService struct {
	routepb.UnimplementedNeighbourServer

	neighTable *neigh.NeighTable
	log        *zap.SugaredLogger
}

// NewNeighbourService creates a new NeighbourService.
func NewNeighbourService(neighTable *neigh.NeighTable, log *zap.SugaredLogger) *NeighbourService {
	return &NeighbourService{
		neighTable: neighTable,
		log:        log,
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
func (m *NeighbourService) CreateTable(_ context.Context, req *routepb.CreateNeighbourTableRequest) (*routepb.CreateNeighbourTableResponse, error) {
	if _, err := m.neighTable.CreateSource(req.GetName(), req.GetDefaultPriority(), false); err != nil {
		return nil, err
	}
	m.log.Infow("created neighbour table",
		zap.String("name", req.GetName()),
		zap.Uint32("default_priority", req.GetDefaultPriority()),
	)
	return &routepb.CreateNeighbourTableResponse{}, nil
}

// UpdateTable updates the default priority of an existing neighbour
// table.
func (m *NeighbourService) UpdateTable(_ context.Context, req *routepb.UpdateNeighbourTableRequest) (*routepb.UpdateNeighbourTableResponse, error) {
	if err := m.neighTable.UpdateSource(req.GetName(), req.GetDefaultPriority()); err != nil {
		return nil, err
	}
	m.log.Infow("updated neighbour table",
		zap.String("name", req.GetName()),
		zap.Uint32("default_priority", req.GetDefaultPriority()),
	)
	return &routepb.UpdateNeighbourTableResponse{}, nil
}

// RemoveTable removes a user-defined neighbour table.
func (m *NeighbourService) RemoveTable(_ context.Context, req *routepb.RemoveNeighbourTableRequest) (*routepb.RemoveNeighbourTableResponse, error) {
	if err := m.neighTable.DeleteSource(req.GetName()); err != nil {
		return nil, err
	}
	m.log.Infow("removed neighbour table",
		zap.String("name", req.GetName()),
	)
	return &routepb.RemoveNeighbourTableResponse{}, nil
}

// ListTables returns metadata about all registered neighbour tables.
func (m *NeighbourService) ListTables(_ context.Context, _ *routepb.ListNeighbourTablesRequest) (*routepb.ListNeighbourTablesResponse, error) {
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
func (m *NeighbourService) UpdateNeighbours(_ context.Context, req *routepb.UpdateNeighboursRequest) (*routepb.UpdateNeighboursResponse, error) {
	table := req.GetTable()
	if table == "" {
		table = defaultStaticTable
	}

	entries := make([]neigh.NeighbourEntry, 0, len(req.GetEntries()))
	for _, e := range req.GetEntries() {
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

	m.log.Infow("updated neighbour entries",
		zap.String("table", table),
		zap.Int("count", len(entries)),
	)
	return &routepb.UpdateNeighboursResponse{}, nil
}

// RemoveNeighbours deletes one or more neighbour entries from the
// specified table.
func (m *NeighbourService) RemoveNeighbours(_ context.Context, req *routepb.RemoveNeighboursRequest) (*routepb.RemoveNeighboursResponse, error) {
	table := req.GetTable()
	if table == "" {
		table = defaultStaticTable
	}

	addrs := make([]netip.Addr, 0, len(req.GetNextHops()))
	for _, hop := range req.GetNextHops() {
		addr, err := netip.ParseAddr(hop)
		if err != nil {
			return nil, fmt.Errorf("invalid next_hop %q: %w", hop, err)
		}
		addrs = append(addrs, addr)
	}

	if err := m.neighTable.Remove(table, addrs); err != nil {
		return nil, err
	}

	m.log.Infow("removed neighbour entries",
		zap.String("table", table),
		zap.Int("count", len(addrs)),
	)
	return &routepb.RemoveNeighboursResponse{}, nil
}
