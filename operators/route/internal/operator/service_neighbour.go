package operator

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"sync"
	"time"

	"google.golang.org/grpc"
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

// NeighbourReplacementLimits bounds memory retained by incomplete snapshots.
//
// Defaults are one million entries, 128 MiB of serialized requests per stream,
// and four staged streams. Chunk limits remain 1,000 entries and 256 KiB.
type NeighbourReplacementLimits struct {
	MaxEntries           int
	MaxBytes             int
	MaxConcurrentStreams int
}

// NeighbourListLimits bounds readers retaining immutable cache generations.
type NeighbourListLimits struct {
	MaxConcurrentStreams int
	MaxDuration          time.Duration
}

// NeighbourService implements the operator-owned NeighbourService
// surface. Mutations wake the reconcile loop.
type NeighbourService struct {
	operatorpb.UnimplementedNeighbourServiceServer

	neighTable         *neigh.NeighTable
	onChanged          func()
	limits             NeighbourReplacementLimits
	staged             chan struct{}
	listReads          chan struct{}
	listMaxDuration    time.Duration
	onSnapshotReceived func(string, bool) bool
	onTableRemoved     func(string)
	commitMu           sync.Mutex
	readiness          *NeighbourReadiness
	remoteTable        string
	remoteDevices      map[string]bool
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
		neighTable:         neighTable,
		onChanged:          opts.OnChanged,
		limits:             opts.ReplacementLimits,
		staged:             make(chan struct{}, opts.ReplacementLimits.MaxConcurrentStreams),
		listReads:          make(chan struct{}, opts.ListLimits.MaxConcurrentStreams),
		listMaxDuration:    opts.ListLimits.MaxDuration,
		onSnapshotReceived: opts.OnSnapshotReceived,
		onTableRemoved:     opts.OnTableRemoved,
		readiness:          opts.Readiness,
		remoteTable:        opts.RemoteTable,
		remoteDevices:      devices,
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
	entries := map[neigh.Key]neigh.NeighbourEntry{}
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
		if len(request.GetEntries()) > operatorpb.NeighbourChunkEntries || chunkBytes > operatorpb.NeighbourChunkBytes ||
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
			if _, duplicate := entries[entry.Key()]; duplicate {
				return status.Errorf(codes.InvalidArgument, "duplicate next hop/device %s/%s", entry.NextHop, entry.HardwareRoute.Device)
			}
			entries[entry.Key()] = entry
		}
	}
	if table == "" {
		return status.Error(codes.InvalidArgument, "replacement requires at least one chunk")
	}
	if table == m.remoteTable {
		if err := m.validateRemoteSnapshot(entries); err != nil {
			return err
		}
	}
	changed, err := m.replaceSnapshot(ctx, table, priority, entries)
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

func (m *NeighbourService) replaceSnapshot(ctx context.Context, table string, priority uint32, entries map[neigh.Key]neigh.NeighbourEntry) (bool, error) {
	m.commitMu.Lock()
	defer m.commitMu.Unlock()
	var changed, recovered bool
	var err error
	if m.readiness == nil {
		changed, err = m.neighTable.ReplaceSource(ctx, table, priority, entries)
	} else {
		changed, recovered, err = m.readiness.ReplaceSnapshot(ctx, m.neighTable, table, priority, entries)
	}
	if err == nil {
		changed = m.onSnapshotReceived(table, changed) || changed || recovered
	}
	return changed, err
}

func (m *NeighbourService) validateRemoteSnapshot(entries map[neigh.Key]neigh.NeighbourEntry) error {
	byIndex := map[uint32]string{}
	byDevice := map[string]uint32{}
	for _, entry := range entries {
		device := entry.HardwareRoute.Device
		if err := operatorpb.ValidateNeighbourDevice(device); err != nil {
			return status.Error(codes.InvalidArgument, err.Error())
		}
		if !m.remoteDevices[device] {
			return status.Errorf(codes.InvalidArgument, "unknown remote device %q", device)
		}
		if entry.Ifindex == 0 {
			continue
		}
		if previous, present := byIndex[entry.Ifindex]; present && previous != device {
			return status.Error(codes.InvalidArgument, "remote ifindex maps to multiple devices")
		}
		if previous, present := byDevice[device]; present && previous != entry.Ifindex {
			return status.Error(codes.InvalidArgument, "remote device maps to multiple ifindices")
		}
		byIndex[entry.Ifindex], byDevice[device] = device, entry.Ifindex
	}
	return nil
}

func (m *NeighbourService) List(
	ctx context.Context,
	req *operatorpb.ListNeighboursRequest,
) (*operatorpb.ListNeighboursResponse, error) {
	view, err := m.listView(ctx, req.GetTable())
	if err != nil {
		return nil, err
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
			return nil, status.Error(codes.ResourceExhausted, "neighbour list exceeds unary limit; use ListStream")
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

// ListStream holds one immutable cache view for the lifetime of the read.
//
// Admission precedes view acquisition. A server deadline ends even a read stuck
// in transport flow control; returning the handler cancels its blocked send.
// The worker retains its admission slot until the view and send are released.
func (m *NeighbourService) ListStream(
	req *operatorpb.ListNeighboursRequest,
	stream grpc.ServerStreamingServer[operatorpb.ListNeighboursResponse],
) error {
	ctx, cancel := context.WithTimeout(stream.Context(), m.listMaxDuration)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	select {
	case m.listReads <- struct{}{}:
	default:
		return status.Error(codes.ResourceExhausted, "too many active neighbour list streams")
	}
	result := make(chan error, 1)
	go func() {
		defer func() { <-m.listReads }()
		result <- m.listStream(ctx, req, stream)
	}()
	select {
	case err := <-result:
		if contextError := ctx.Err(); contextError != nil {
			return status.FromContextError(contextError).Err()
		}
		return err
	case <-ctx.Done():
		return status.FromContextError(ctx.Err()).Err()
	}
}

func (m *NeighbourService) listStream(
	ctx context.Context,
	req *operatorpb.ListNeighboursRequest,
	stream grpc.ServerStreamingServer[operatorpb.ListNeighboursResponse],
) error {
	view, err := m.listView(ctx, req.GetTable())
	if err != nil {
		return err
	}
	entries, _ := view.Entries()
	response := &operatorpb.ListNeighboursResponse{}
	chunkBytes := 0
	for entry := range entries {
		if err := ctx.Err(); err != nil {
			return status.FromContextError(err).Err()
		}
		wireEntry := listedNeighbour(entry, req.GetTable())
		entryBytes := protowire.SizeTag(1) + protowire.SizeBytes(proto.Size(wireEntry))
		if entryBytes > operatorpb.NeighbourListChunkBytes {
			return status.Error(codes.ResourceExhausted, "neighbour entry exceeds list chunk limit")
		}
		if len(response.Neighbours) == operatorpb.NeighbourListChunkEntries ||
			chunkBytes+entryBytes > operatorpb.NeighbourListChunkBytes {
			if err := stream.Send(response); err != nil {
				return err
			}
			response = &operatorpb.ListNeighboursResponse{}
			chunkBytes = 0
		}
		response.Neighbours = append(response.Neighbours, wireEntry)
		chunkBytes += entryBytes
	}
	if err := ctx.Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	return stream.Send(response)
}

func (m *NeighbourService) listView(ctx context.Context, table string) (neigh.NexthopCacheView, error) {
	if err := ctx.Err(); err != nil {
		return neigh.NexthopCacheView{}, status.FromContextError(err).Err()
	}
	if table == "" {
		return m.neighTable.View(), nil
	}
	view, ok := m.neighTable.SourceView(table)
	if !ok {
		return neigh.NexthopCacheView{}, status.Errorf(codes.NotFound, "table %q not found", table)
	}
	return view, nil
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
		Ifindex:      entry.Ifindex,
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
	m.onTableRemoved(req.GetName())
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
		Ifindex: entry.GetIfindex(),
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
