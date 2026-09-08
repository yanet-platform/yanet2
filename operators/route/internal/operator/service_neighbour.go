package operator

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

const defaultStaticTable = "static"

// NeighbourReplacementLimits bounds memory retained by incomplete snapshots.
//
// Defaults are one million entries, 128 MiB of serialized requests per stream,
// and four staged streams. Chunk limits remain 1,000 entries and 256 KiB.
type NeighbourReplacementLimits struct {
	MaxEntries           int
	MaxBytes             int
	MaxConcurrentStreams int
}

// NeighbourService implements the operator-owned NeighbourService
// surface. Mutations wake the reconcile loop.
type NeighbourService struct {
	operatorpb.UnimplementedNeighbourServiceServer

	neighTable *neigh.NeighTable
	onChanged  func()
	limits     NeighbourReplacementLimits
	staged     chan struct{}
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
		limits:     opts.ReplacementLimits,
		staged:     make(chan struct{}, opts.ReplacementLimits.MaxConcurrentStreams),
	}
}

// ReplaceNeighbours stages a bounded snapshot until a clean client half-close.
func (m *NeighbourService) ReplaceNeighbours(
	stream grpc.ClientStreamingServer[operatorpb.ReplaceNeighboursRequest, operatorpb.ReplaceNeighboursResponse],
) error {
	ctx := stream.Context()
	if err := ctx.Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	select {
	case m.staged <- struct{}{}:
		defer func() { <-m.staged }()
	default:
		return status.Error(codes.ResourceExhausted, "too many staged neighbour replacements")
	}
	var table string
	var priority uint32
	totalBytes := 0
	entries := map[netip.Addr]neigh.NeighbourEntry{}
	for {
		request, err := stream.Recv()
		if contextError := ctx.Err(); contextError != nil {
			return status.FromContextError(contextError).Err()
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return status.FromContextError(err).Err()
			}
			return err
		}
		chunkBytes := proto.Size(request)
		if len(request.GetEntries()) > 1000 || chunkBytes > 256*1024 ||
			chunkBytes > m.limits.MaxBytes-totalBytes ||
			len(request.GetEntries()) > m.limits.MaxEntries-len(entries) {
			return status.Error(codes.ResourceExhausted, "neighbour replacement exceeds entry or byte limit")
		}
		totalBytes += chunkBytes
		if table == "" {
			if err := neigh.ValidateSourceName(request.GetTable()); err != nil {
				return status.Error(codes.InvalidArgument, err.Error())
			}
			table, priority = request.GetTable(), request.GetDefaultPriority()
		} else if table != request.GetTable() || priority != request.GetDefaultPriority() {
			return status.Error(codes.InvalidArgument, "replacement metadata must match across chunks")
		} else if len(entries) == 0 || len(request.GetEntries()) == 0 {
			return status.Error(codes.InvalidArgument, "empty snapshot must contain exactly one chunk")
		}
		for _, wireEntry := range request.GetEntries() {
			if wireEntry.GetSource() != "" || wireEntry.GetUpdatedAt() != 0 {
				return status.Error(codes.InvalidArgument, "source and updated_at are server-owned")
			}
			entry, err := parseNeighbourEntry(wireEntry)
			if err != nil {
				return err
			}
			if _, duplicate := entries[entry.NextHop]; duplicate {
				return status.Errorf(codes.InvalidArgument, "duplicate next hop %q", entry.NextHop)
			}
			entries[entry.NextHop] = entry
		}
	}
	if table == "" {
		return status.Error(codes.InvalidArgument, "replacement requires at least one chunk")
	}
	changed, err := m.neighTable.ReplaceSource(ctx, table, priority, entries)
	if err != nil {
		if errors.Is(err, neigh.ErrBuiltInSource) {
			return status.Error(codes.FailedPrecondition, err.Error())
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return status.FromContextError(err).Err()
		}
		return status.Errorf(codes.Internal, "failed to replace neighbour table: %v", err)
	}
	if changed {
		m.onChanged()
	}
	return stream.SendAndClose(&operatorpb.ReplaceNeighboursResponse{})
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
	if len(entry.GetDevice()) > 128 {
		return neigh.NeighbourEntry{}, status.Error(codes.InvalidArgument, "device exceeds 128 bytes")
	}
	return neigh.NeighbourEntry{
		NextHop: address,
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
