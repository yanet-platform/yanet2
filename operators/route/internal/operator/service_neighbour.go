package operator

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/hwroute"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

const defaultStaticTable = "static"

// NeighbourService implements the operator-owned NeighbourService
// surface. Mutations wake the reconcile loop.
type NeighbourService struct {
	operatorpb.UnimplementedNeighbourServiceServer

	neighTable    *neigh.NeighTable
	onChanged     func()
	commitMu      sync.Mutex
	readiness     *NeighbourReadiness
	remoteTable   string
	remoteDevices map[string]bool
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

	devices := map[string]bool{}
	for _, device := range opts.RemoteDevices {
		devices[device] = true
	}
	return &NeighbourService{
		neighTable:    neighTable,
		onChanged:     opts.OnChanged,
		readiness:     opts.Readiness,
		remoteTable:   opts.RemoteTable,
		remoteDevices: devices,
	}
}

// ReplaceNeighbours validates a bounded complete snapshot before atomic commit.
func (m *NeighbourService) ReplaceNeighbours(
	ctx context.Context,
	req *operatorpb.ReplaceNeighboursRequest,
) (*operatorpb.ReplaceNeighboursResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if len(req.GetEntries()) > operatorpb.NeighbourSnapshotEntries || proto.Size(req) > operatorpb.NeighbourSnapshotBytes {
		return nil, status.Error(codes.ResourceExhausted, "neighbour replacement exceeds entry or byte limit")
	}
	table := req.GetTable()
	if err := neigh.ValidateSourceName(table); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	entries := make(map[netip.Addr]neigh.NeighbourEntry, len(req.GetEntries()))
	for _, wireEntry := range req.GetEntries() {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
		if wireEntry.GetSource() != "" || wireEntry.GetUpdatedAt() != 0 {
			return nil, status.Error(codes.InvalidArgument, "source and updated_at are server-owned")
		}
		entry, err := parseNeighbourEntry(wireEntry)
		if err != nil {
			return nil, err
		}
		if _, duplicate := entries[entry.NextHop]; duplicate {
			return nil, status.Errorf(codes.InvalidArgument, "duplicate canonical next hop %s", entry.NextHop)
		}
		if table == m.remoteTable {
			device := entry.HardwareRoute.Device
			if err := operatorpb.ValidateNeighbourDevice(device); err != nil {
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
			if !m.remoteDevices[device] {
				return nil, status.Errorf(codes.InvalidArgument, "unknown remote device %q", device)
			}
		}
		entries[entry.NextHop] = entry
	}
	changed, err := m.replaceSnapshot(ctx, table, req.GetDefaultPriority(), entries)
	if err != nil {
		if errors.Is(err, neigh.ErrBuiltInSource) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, status.FromContextError(err).Err()
		}
		return nil, status.Errorf(codes.Internal, "failed to replace neighbour table: %v", err)
	}
	if changed {
		m.onChanged()
	}
	return &operatorpb.ReplaceNeighboursResponse{}, nil
}

func (m *NeighbourService) replaceSnapshot(ctx context.Context, table string, priority uint32, entries map[netip.Addr]neigh.NeighbourEntry) (bool, error) {
	m.commitMu.Lock()
	defer m.commitMu.Unlock()
	if m.readiness == nil {
		return m.neighTable.ReplaceSource(ctx, table, priority, entries)
	}
	changed, recovered, err := m.readiness.ReplaceSnapshot(ctx, m.neighTable, table, priority, entries)
	return changed || recovered, err
}

func (m *NeighbourService) List(
	ctx context.Context,
	req *operatorpb.ListNeighboursRequest,
) (*operatorpb.ListNeighboursResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	var view neigh.NexthopCacheView
	if table := req.GetTable(); table == "" {
		view = m.neighTable.View()
	} else {
		var found bool
		view, found = m.neighTable.SourceView(table)
		if !found {
			return nil, status.Errorf(codes.NotFound, "table %q not found", table)
		}
	}
	entries, size := view.Entries()
	// Refuse oversized views before allocating the complete unary response.
	totalBytes := 0
	for entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
		wireEntry := listedNeighbour(entry, req.GetTable())
		totalBytes += protowire.SizeTag(1) + protowire.SizeBytes(proto.Size(wireEntry))
		if totalBytes > operatorpb.NeighbourListUnaryBytes {
			return nil, status.Error(codes.ResourceExhausted, "neighbour list exceeds unary limit")
		}
	}
	response := &operatorpb.ListNeighboursResponse{Neighbours: make([]*operatorpb.NeighbourEntry, 0, size)}
	for entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
		response.Neighbours = append(response.Neighbours, listedNeighbour(entry, req.GetTable()))
	}
	return response, nil
}

func listedNeighbour(entry neigh.NeighbourEntry, table string) *operatorpb.NeighbourEntry {
	source := entry.Source
	if source == "" {
		source = table
	}
	return &operatorpb.NeighbourEntry{
		NextHop:      commonpb.NewIPAddressFromAddr(entry.NextHop),
		LinkAddr:     commonpb.NewMACAddressEUI48(entry.HardwareRoute.DestinationMAC),
		HardwareAddr: commonpb.NewMACAddressEUI48(entry.HardwareRoute.SourceMAC),
		State:        operatorpb.NeighbourState(entry.State),
		UpdatedAt:    entry.UpdatedAt.Unix(),
		Source:       source,
		Priority:     entry.Priority,
		Device:       entry.HardwareRoute.Device,
	}
}

func (m *NeighbourService) CreateTable(
	ctx context.Context,
	req *operatorpb.CreateNeighbourTableRequest,
) (*operatorpb.CreateNeighbourTableResponse, error) {
	m.commitMu.Lock()
	defer m.commitMu.Unlock()
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
	if m.remoteTable != "" && req.GetName() == m.remoteTable {
		return nil, status.Error(codes.FailedPrecondition, "configured remote source requires complete replacements")
	}
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
	m.commitMu.Lock()
	defer m.commitMu.Unlock()
	var err error
	if m.readiness == nil {
		err = m.neighTable.DeleteSource(req.GetName())
	} else {
		err = m.readiness.RemoveTable(m.neighTable, req.GetName())
	}
	if err != nil {
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
	if table == m.remoteTable {
		return nil, status.Error(codes.FailedPrecondition, "configured remote source requires complete replacements")
	}

	entries := make([]neigh.NeighbourEntry, 0, len(req.GetEntries()))
	for _, e := range req.GetEntries() {
		entry, err := parseNeighbourEntry(e)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}

	if err := m.neighTable.Add(table, entries); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to add neighbours: %v", err)
	}

	m.onChanged()
	return &operatorpb.UpdateNeighboursResponse{}, nil
}

func parseNeighbourEntry(entry *operatorpb.NeighbourEntry) (neigh.NeighbourEntry, error) {
	address, err := entry.GetNextHop().ToAddr()
	if err != nil {
		return neigh.NeighbourEntry{}, status.Errorf(codes.InvalidArgument, "invalid next hop: %v", err)
	}
	if entry.GetHardwareAddr() == nil || entry.GetLinkAddr() == nil ||
		entry.GetHardwareAddr().GetAddr()>>48 != 0 || entry.GetLinkAddr().GetAddr()>>48 != 0 {
		return neigh.NeighbourEntry{}, status.Error(codes.InvalidArgument, "both MAC addresses must be present EUI-48 values")
	}
	if err := hwroute.ValidateDevice(entry.GetDevice()); err != nil {
		return neigh.NeighbourEntry{}, status.Error(codes.InvalidArgument, err.Error())
	}
	return neigh.NeighbourEntry{
		NextHop: address.Unmap(),
		HardwareRoute: neigh.HardwareRoute{
			SourceMAC:      entry.GetHardwareAddr().EUI48(),
			DestinationMAC: entry.GetLinkAddr().EUI48(),
			Device:         entry.GetDevice(),
		},
		UpdatedAt: time.Now(),
		State:     neigh.NeighbourStatePermanent,
		Priority:  entry.GetPriority(),
	}, nil
}

func (m *NeighbourService) RemoveNeighbours(
	ctx context.Context,
	req *operatorpb.RemoveNeighboursRequest,
) (*operatorpb.RemoveNeighboursResponse, error) {
	table := req.GetTable()
	if table == "" {
		table = defaultStaticTable
	}
	if table == m.remoteTable {
		return nil, status.Error(codes.FailedPrecondition, "configured remote source requires complete replacements")
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
		return nil, status.Errorf(codes.Internal, "failed to remove neighbours: %v", err)
	}

	m.onChanged()
	return &operatorpb.RemoveNeighboursResponse{}, nil
}
