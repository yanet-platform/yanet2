package operator

import (
	"context"
	"net/netip"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/agents/yanet-route-operator/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/agents/yanet-route-operator/operatorpb"
	"github.com/yanet-platform/yanet2/common/commonpb"
)

const defaultStaticTable = "static"

// NeighbourService implements the operator-owned NeighbourService
// surface. Mutations wake the reconcile loop.
type NeighbourService struct {
	operatorpb.UnimplementedNeighbourServiceServer

	neighTable *neigh.NeighTable
	onChanged  func()
}

// NewNeighbourService constructs a NeighbourService bound to the
// supplied neighbour table.
func NewNeighbourService(
	neighTable *neigh.NeighTable,
	options ...NeighbourServiceOption,
) *NeighbourService {
	opts := newNeighbourServiceOptions()
	for _, o := range options {
		o(opts)
	}

	return &NeighbourService{
		neighTable: neighTable,
		onChanged:  opts.OnChanged,
	}
}

func (m *NeighbourService) List(
	ctx context.Context,
	req *operatorpb.ListNeighboursRequest,
) (*operatorpb.ListNeighboursResponse, error) {
	table := req.GetTable()

	var view neigh.NexthopCacheView
	if table == "" {
		view = m.neighTable.View()
	} else {
		v, ok := m.neighTable.SourceView(table)
		if !ok {
			return nil, status.Errorf(codes.NotFound, "table %q not found", table)
		}
		view = v
	}

	entries, size := view.Entries()

	neighbours := make([]*operatorpb.NeighbourEntry, 0, size)
	for entry := range entries {
		source := entry.Source
		if source == "" {
			source = table
		}

		neighbours = append(
			neighbours,
			&operatorpb.NeighbourEntry{
				NextHop:      entry.NextHop.String(),
				LinkAddr:     commonpb.NewMACAddressEUI48(entry.HardwareRoute.DestinationMAC),
				HardwareAddr: commonpb.NewMACAddressEUI48(entry.HardwareRoute.SourceMAC),
				State:        operatorpb.NeighbourState(entry.State),
				UpdatedAt:    entry.UpdatedAt.Unix(),
				Source:       source,
				Priority:     entry.Priority,
				Device:       entry.HardwareRoute.Device,
			},
		)
	}

	return &operatorpb.ListNeighboursResponse{
		Neighbours: neighbours,
	}, nil
}

func (m *NeighbourService) CreateTable(
	ctx context.Context,
	req *operatorpb.CreateNeighbourTableRequest,
) (*operatorpb.CreateNeighbourTableResponse, error) {
	if _, err := m.neighTable.CreateSource(req.GetName(), req.GetDefaultPriority(), false); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create neighbour table: %v", err)
	}
	m.onChanged()
	return &operatorpb.CreateNeighbourTableResponse{}, nil
}

func (m *NeighbourService) UpdateTable(
	ctx context.Context,
	req *operatorpb.UpdateNeighbourTableRequest,
) (*operatorpb.UpdateNeighbourTableResponse, error) {
	if err := m.neighTable.UpdateSource(req.GetName(), req.GetDefaultPriority()); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update neighbour table: %v", err)
	}
	m.onChanged()
	return &operatorpb.UpdateNeighbourTableResponse{}, nil
}

func (m *NeighbourService) RemoveTable(
	ctx context.Context,
	req *operatorpb.RemoveNeighbourTableRequest,
) (*operatorpb.RemoveNeighbourTableResponse, error) {
	if err := m.neighTable.DeleteSource(req.GetName()); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to remove neighbour table: %v", err)
	}
	m.onChanged()
	return &operatorpb.RemoveNeighbourTableResponse{}, nil
}

func (m *NeighbourService) ListTables(
	ctx context.Context,
	req *operatorpb.ListNeighbourTablesRequest,
) (*operatorpb.ListNeighbourTablesResponse, error) {
	sources := m.neighTable.ListSources()

	tables := make([]*operatorpb.NeighbourTableInfo, 0, len(sources))
	for _, src := range sources {
		tables = append(tables, &operatorpb.NeighbourTableInfo{
			Name:            src.Name,
			DefaultPriority: src.DefaultPriority,
			EntryCount:      int64(src.EntryCount),
			BuiltIn:         src.BuiltIn,
		})
	}

	return &operatorpb.ListNeighbourTablesResponse{
		Tables: tables,
	}, nil
}

func (m *NeighbourService) UpdateNeighbours(
	ctx context.Context,
	req *operatorpb.UpdateNeighboursRequest,
) (*operatorpb.UpdateNeighboursResponse, error) {
	table := req.GetTable()
	if table == "" {
		table = defaultStaticTable
	}

	entries := make([]neigh.NeighbourEntry, 0, len(req.GetEntries()))
	for _, e := range req.GetEntries() {
		addr, err := netip.ParseAddr(e.GetNextHop())
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid nexthop %q: %v", e.GetNextHop(), err)
		}
		if e.GetHardwareAddr() == nil {
			return nil, status.Errorf(codes.InvalidArgument, "neighbour entry %q is missing hardware_addr", e.GetNextHop())
		}
		if e.GetLinkAddr() == nil {
			return nil, status.Errorf(codes.InvalidArgument, "neighbour entry %q is missing link_addr", e.GetNextHop())
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
		return nil, status.Errorf(codes.Internal, "failed to add neighbours: %v", err)
	}

	m.onChanged()
	return &operatorpb.UpdateNeighboursResponse{}, nil
}

func (m *NeighbourService) RemoveNeighbours(
	ctx context.Context,
	req *operatorpb.RemoveNeighboursRequest,
) (*operatorpb.RemoveNeighboursResponse, error) {
	table := req.GetTable()
	if table == "" {
		table = defaultStaticTable
	}

	addrs := make([]netip.Addr, 0, len(req.GetNextHops()))
	for _, hop := range req.GetNextHops() {
		addr, err := netip.ParseAddr(hop)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid next_hop %q: %v", hop, err)
		}
		addrs = append(addrs, addr)
	}

	if err := m.neighTable.Remove(table, addrs); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to remove neighbours: %v", err)
	}

	m.onChanged()
	return &operatorpb.RemoveNeighboursResponse{}, nil
}
