package operator

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/operators/route/neigh"
	"github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

const defaultStaticTable = "static"

// NeighbourService implements the operator-owned NeighbourService
// surface.
//
// Mutations reach the reconcile loop through the neighbour table, which
// wakes it only when the merged hardware routes changed.
type NeighbourService struct {
	operatorpb.UnimplementedNeighbourServiceServer

	neighTable *neigh.NeighTable
}

// NewNeighbourService constructs a NeighbourService bound to the
// supplied neighbour table.
func NewNeighbourService(neighTable *neigh.NeighTable) *NeighbourService {
	return &NeighbourService{
		neighTable: neighTable,
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
				NextHop:      commonpb.NewIPAddressFromAddr(entry.NextHop),
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
		code := codes.Internal
		if errors.Is(err, neigh.ErrSourceExists) {
			code = codes.AlreadyExists
		}
		return nil, status.Errorf(code, "failed to create neighbour table: %v", err)
	}
	return &operatorpb.CreateNeighbourTableResponse{}, nil
}

func (m *NeighbourService) UpdateTable(
	ctx context.Context,
	req *operatorpb.UpdateNeighbourTableRequest,
) (*operatorpb.UpdateNeighbourTableResponse, error) {
	if err := m.neighTable.UpdateSource(req.GetName(), req.GetDefaultPriority()); err != nil {
		code := codes.Internal
		if errors.Is(err, neigh.ErrSourceNotFound) {
			code = codes.NotFound
		}
		return nil, status.Errorf(code, "failed to update neighbour table: %v", err)
	}
	return &operatorpb.UpdateNeighbourTableResponse{}, nil
}

func (m *NeighbourService) RemoveTable(
	ctx context.Context,
	req *operatorpb.RemoveNeighbourTableRequest,
) (*operatorpb.RemoveNeighbourTableResponse, error) {
	if err := m.neighTable.DeleteSource(req.GetName()); err != nil {
		code := codes.Internal
		switch {
		case errors.Is(err, neigh.ErrSourceNotFound):
			code = codes.NotFound
		case errors.Is(err, neigh.ErrBuiltInSource):
			code = codes.FailedPrecondition
		}
		return nil, status.Errorf(code, "failed to remove neighbour table: %v", err)
	}
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
		addr, err := e.GetNextHop().ToAddr()
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid nexthop (bytes=%x): %v", e.GetNextHop().GetAddr(), err)
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
		code := codes.Internal
		if errors.Is(err, neigh.ErrSourceNotFound) {
			code = codes.NotFound
		}
		return nil, status.Errorf(code, "failed to add neighbours: %v", err)
	}

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
		addr, err := hop.ToAddr()
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid next_hop (bytes=%x): %v", hop.GetAddr(), err)
		}
		addrs = append(addrs, addr)
	}

	if err := m.neighTable.Remove(table, addrs); err != nil {
		code := codes.Internal
		if errors.Is(err, neigh.ErrSourceNotFound) {
			code = codes.NotFound
		}
		return nil, status.Errorf(code, "failed to remove neighbours: %v", err)
	}

	return &operatorpb.RemoveNeighboursResponse{}, nil
}

// SwapNeighbours validates a complete observation before replacing its table.
//
// The response acknowledges the table replacement; FIB application follows
// asynchronously when the merged hardware routes change.
func (m *NeighbourService) SwapNeighbours(
	ctx context.Context,
	req *operatorpb.SwapNeighboursRequest,
) (*operatorpb.SwapNeighboursResponse, error) {
	entries, err := req.ToNeighbourEntries()
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if err := m.neighTable.SwapSource(req.GetTable(), entries); err != nil {
		return nil, status.Errorf(codes.NotFound, "failed to swap neighbours: %v", err)
	}
	return &operatorpb.SwapNeighboursResponse{}, nil
}
