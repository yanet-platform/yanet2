package operator_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/operators/route/internal/operator"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

type neighbourServiceFixture struct {
	Table    *neigh.NeighTable
	Service  *operator.NeighbourService
	Client   operatorpb.NeighbourServiceClient
	Endpoint string
	Changes  atomic.Int64
}

// newNeighbourServiceFixture serves the real unary handlers with default caps.
func newNeighbourServiceFixture(t *testing.T, options ...operator.NeighbourServiceOption) *neighbourServiceFixture {
	t.Helper()
	return newInterceptedNeighbourFixture(t, nil, options...)
}

// newInterceptedNeighbourFixture permits controlled failures around real commits.
func newInterceptedNeighbourFixture(t *testing.T, interceptor grpc.UnaryServerInterceptor, options ...operator.NeighbourServiceOption) *neighbourServiceFixture {
	t.Helper()
	fixture := &neighbourServiceFixture{Table: neigh.NewNeighTable()}
	options = append([]operator.NeighbourServiceOption{
		operator.WithNeighbourServiceOnChanged(func() { fixture.Changes.Add(1) }),
	}, options...)
	fixture.Service = operator.NewNeighbourService(fixture.Table, options...)
	server := grpc.NewServer(grpc.UnaryInterceptor(interceptor))
	operatorpb.RegisterNeighbourServiceServer(server, fixture.Service)
	fixture.Endpoint = serveTestGRPCServer(t, server)
	connection, err := grpc.NewClient(fixture.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	fixture.Client = operatorpb.NewNeighbourServiceClient(connection)
	return fixture
}

// serveTestGRPCServer serves registered handlers until test cleanup and joins.
func serveTestGRPCServer(t *testing.T, server *grpc.Server) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var group errgroup.Group
	group.Go(func() error {
		err := server.Serve(listener)
		if errors.Is(err, grpc.ErrServerStopped) {
			return nil
		}
		return err
	})
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
		require.NoError(t, group.Wait())
	})
	return listener.Addr().String()
}

// replacementRequest returns a complete snapshot with server metadata unset.
func replacementRequest(table string, priority uint32, addresses ...string) *operatorpb.ReplaceNeighboursRequest {
	request := &operatorpb.ReplaceNeighboursRequest{Table: table, DefaultPriority: priority}
	for _, address := range addresses {
		request.Entries = append(request.Entries, &operatorpb.NeighbourEntry{
			NextHop:      commonpb.NewIPAddressFromAddr(netip.MustParseAddr(address)),
			HardwareAddr: commonpb.NewMACAddressEUI48([6]byte{2, 0, 0, 0, 0, 1}),
			LinkAddr:     commonpb.NewMACAddressEUI48([6]byte{2, 0, 0, 0, 0, 2}),
			Device:       "logical0",
			State:        operatorpb.NeighbourState_NUD_REACHABLE,
		})
	}
	return request
}

// sendNeighbourSnapshot sends exactly one full request through the unary client.
func sendNeighbourSnapshot(ctx context.Context, client operatorpb.NeighbourServiceClient, request *operatorpb.ReplaceNeighboursRequest) error {
	response, err := client.ReplaceNeighbours(ctx, request)
	if err == nil && response == nil {
		return errors.New("missing replacement acknowledgement")
	}
	return err
}

// sourceEntries copies an immutable source view, including entry ages.
func sourceEntries(t *testing.T, table *neigh.NeighTable, name string) map[netip.Addr]neigh.NeighbourEntry {
	t.Helper()
	view, found := table.SourceView(name)
	require.True(t, found)
	entries, _ := view.All()
	return maps.Collect(entries)
}

// Test_NeighbourService_RemoteRemoval verifies that only complete replacements
// can mutate configured remote input, while ordinary static edits still work.
func Test_NeighbourService_RemoteRemoval(t *testing.T) {
	fixture, input, _, _ := newReadinessFixture(t, 200*time.Millisecond)
	request := replacementRequest("remote", 100, "192.0.2.1")
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, request))
	before := sourceEntries(t, fixture.Table, "remote")
	generation, available := input.Generation()
	require.True(t, available)
	_, err := fixture.Client.RemoveNeighbours(t.Context(), &operatorpb.RemoveNeighboursRequest{
		Table: "remote", NextHops: []*commonpb.IPAddress{request.Entries[0].NextHop},
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	_, err = fixture.Client.UpdateNeighbours(t.Context(), &operatorpb.UpdateNeighboursRequest{Table: "remote", Entries: request.Entries})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Equal(t, before, sourceEntries(t, fixture.Table, "remote"))
	current, available := input.Generation()
	require.True(t, available)
	require.Equal(t, generation, current)
	require.Equal(t, int64(1), fixture.Changes.Load())
	for _, stale := range []bool{false, true} {
		if stale {
			require.Eventually(t, func() bool { return !input.Available() }, time.Second, time.Millisecond)
		}
		_, err = fixture.Client.UpdateTable(t.Context(), &operatorpb.UpdateNeighbourTableRequest{Name: "remote", DefaultPriority: 200})
		require.Equal(t, codes.FailedPrecondition, status.Code(err))
		require.Equal(t, []neigh.SourceInfo{{Name: "remote", DefaultPriority: 100, EntryCount: 1}}, fixture.Table.ListSources())
		require.Equal(t, before, sourceEntries(t, fixture.Table, "remote"))
		current, available = input.Generation()
		require.Equal(t, !stale, available)
		require.Equal(t, generation, current)
		require.Equal(t, int64(1), fixture.Changes.Load())
	}
	request.DefaultPriority = 200
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, request))
	require.Equal(t, []neigh.SourceInfo{{Name: "remote", DefaultPriority: 200, EntryCount: 1}}, fixture.Table.ListSources())
	require.Equal(t, uint32(200), sourceEntries(t, fixture.Table, "remote")[netip.MustParseAddr("192.0.2.1")].Priority)
	current, available = input.Generation()
	require.True(t, available)
	require.Equal(t, generation+1, current)
	require.Equal(t, int64(2), fixture.Changes.Load())
	_, err = fixture.Client.CreateTable(t.Context(), &operatorpb.CreateNeighbourTableRequest{Name: "custom", DefaultPriority: 10})
	require.NoError(t, err)
	_, err = fixture.Client.UpdateTable(t.Context(), &operatorpb.UpdateNeighbourTableRequest{Name: "custom", DefaultPriority: 20})
	require.NoError(t, err)
	require.Contains(t, fixture.Table.ListSources(), neigh.SourceInfo{Name: "custom", DefaultPriority: 20})
	require.Equal(t, int64(4), fixture.Changes.Load())
	_, err = fixture.Table.CreateSource("static", 10, true)
	require.NoError(t, err)
	for _, table := range []string{"static", ""} {
		_, err = fixture.Client.UpdateNeighbours(t.Context(), &operatorpb.UpdateNeighboursRequest{Table: table, Entries: request.Entries})
		require.NoError(t, err)
		_, err = fixture.Client.RemoveNeighbours(t.Context(), &operatorpb.RemoveNeighboursRequest{
			Table: table, NextHops: []*commonpb.IPAddress{request.Entries[0].NextHop},
		})
		require.NoError(t, err)
		require.Empty(t, sourceEntries(t, fixture.Table, "static"))
	}
}

// Test_NeighbourService_AtomicReplacement verifies that entries and priority
// commit together and retained views never change after a replacement.
func Test_NeighbourService_AtomicReplacement(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%t", existing), func(t *testing.T) {
			fixture := newNeighbourServiceFixture(t)
			if existing {
				require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("snapshot", 10, "192.0.2.1")))
			}
			previous := fixture.Table.View()
			oldEntries, _ := previous.All()
			before := maps.Collect(oldEntries)
			changes := fixture.Changes.Load()
			request := replacementRequest("snapshot", 200, "192.0.2.2", "2001:db8::1")
			request.Entries[1].Priority = 7
			require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, request))
			require.Equal(t, changes+1, fixture.Changes.Load())
			require.Equal(t, []neigh.SourceInfo{{Name: "snapshot", DefaultPriority: 200, EntryCount: 2}}, fixture.Table.ListSources())
			entries := sourceEntries(t, fixture.Table, "snapshot")
			require.Equal(t, uint32(200), entries[netip.MustParseAddr("192.0.2.2")].Priority)
			require.Equal(t, uint32(7), entries[netip.MustParseAddr("2001:db8::1")].Priority)
			for _, entry := range entries {
				require.Equal(t, neigh.NeighbourStatePermanent, entry.State)
				require.False(t, entry.UpdatedAt.IsZero())
			}
			oldEntries, _ = previous.All()
			require.Equal(t, before, maps.Collect(oldEntries))
			response, err := fixture.Client.List(t.Context(), &operatorpb.ListNeighboursRequest{Table: "snapshot"})
			require.NoError(t, err)
			require.Len(t, response.GetNeighbours(), 2)
			for _, entry := range response.GetNeighbours() {
				require.Equal(t, "snapshot", entry.GetSource())
			}
		})
	}
}

// Test_NeighbourService_EmptyReplacement verifies that an explicit empty
// replacement clears only its source and an empty heartbeat retains generation.
func Test_NeighbourService_EmptyReplacement(t *testing.T) {
	fixture, input, _, _ := newReadinessFixture(t, time.Minute)
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("other", 100, "192.0.2.2")))
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("remote", 100, "192.0.2.1")))
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("remote", 100)))
	generation, _ := input.Generation()
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("remote", 100)))
	current, available := input.Generation()
	require.True(t, available)
	require.Equal(t, generation, current)
	require.Empty(t, sourceEntries(t, fixture.Table, "remote"))
	require.Len(t, sourceEntries(t, fixture.Table, "other"), 1)
	require.Equal(t, int64(3), fixture.Changes.Load())
}

// Test_NeighbourService_DeviceNameBoundary verifies that incremental and full
// updates enforce the byte limit rather than counting Unicode characters.
func Test_NeighbourService_DeviceNameBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		device string
		valid  bool
	}{
		{name: "79 ASCII bytes", device: strings.Repeat("d", 79), valid: true},
		{name: "80 ASCII bytes", device: strings.Repeat("d", 80)},
		{name: "79 UTF-8 bytes", device: strings.Repeat("é", 39) + "a", valid: true},
		{name: "80 UTF-8 bytes", device: strings.Repeat("é", 40)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newNeighbourServiceFixture(t)
			_, err := fixture.Table.CreateSource("static", 100, true)
			require.NoError(t, err)
			request := replacementRequest("snapshot", 100, "192.0.2.1")
			request.Entries[0].Device = tc.device
			for _, replace := range []bool{false, true} {
				table := "static"
				if replace {
					table = "snapshot"
					err = sendNeighbourSnapshot(t.Context(), fixture.Client, request)
				} else {
					_, err = fixture.Client.UpdateNeighbours(t.Context(), &operatorpb.UpdateNeighboursRequest{Table: table, Entries: request.Entries})
				}
				if tc.valid {
					require.NoError(t, err)
					require.Equal(t, tc.device, sourceEntries(t, fixture.Table, table)[netip.MustParseAddr("192.0.2.1")].HardwareRoute.Device)
				} else {
					require.Equal(t, codes.InvalidArgument, status.Code(err))
				}
			}
		})
	}
}

// Test_NeighbourService_InvalidReplacement verifies that a malformed final
// entry cannot alter entries, priority, generation or expired freshness.
func Test_NeighbourService_InvalidReplacement(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*operatorpb.ReplaceNeighboursRequest)
	}{
		{name: "missing table", mutate: func(request *operatorpb.ReplaceNeighboursRequest) { request.Table = "" }},
		{name: "overlong table", mutate: func(request *operatorpb.ReplaceNeighboursRequest) { request.Table = strings.Repeat("t", 129) }},
		{name: "invalid table prefix", mutate: func(request *operatorpb.ReplaceNeighboursRequest) { request.Table = "_remote" }},
		{name: "non ASCII table", mutate: func(request *operatorpb.ReplaceNeighboursRequest) { request.Table = "réseau" }},
		{name: "nil entry", mutate: func(request *operatorpb.ReplaceNeighboursRequest) { request.Entries[1] = nil }},
		{name: "nil next hop", mutate: func(request *operatorpb.ReplaceNeighboursRequest) { request.Entries[1].NextHop = nil }},
		{name: "short next hop", mutate: func(request *operatorpb.ReplaceNeighboursRequest) { request.Entries[1].NextHop.Addr = []byte{1, 2, 3} }},
		{name: "nil source MAC", mutate: func(request *operatorpb.ReplaceNeighboursRequest) { request.Entries[1].HardwareAddr = nil }},
		{name: "nil destination MAC", mutate: func(request *operatorpb.ReplaceNeighboursRequest) { request.Entries[1].LinkAddr = nil }},
		{name: "source MAC above EUI48", mutate: func(request *operatorpb.ReplaceNeighboursRequest) { request.Entries[1].HardwareAddr.Addr = 1 << 48 }},
		{name: "destination MAC above EUI48", mutate: func(request *operatorpb.ReplaceNeighboursRequest) { request.Entries[1].LinkAddr.Addr = 1 << 48 }},
		{name: "overlong device", mutate: func(request *operatorpb.ReplaceNeighboursRequest) {
			request.Entries[1].Device = strings.Repeat("d", 80)
		}},
		{name: "unknown remote device", mutate: func(request *operatorpb.ReplaceNeighboursRequest) { request.Entries[1].Device = "unknown" }},
		{name: "missing remote device", mutate: func(request *operatorpb.ReplaceNeighboursRequest) { request.Entries[1].Device = "" }},
		{name: "server owned source", mutate: func(request *operatorpb.ReplaceNeighboursRequest) { request.Entries[1].Source = "remote" }},
		{name: "server owned timestamp", mutate: func(request *operatorpb.ReplaceNeighboursRequest) { request.Entries[1].UpdatedAt = 1 }},
		{name: "duplicate IP on same device", mutate: func(request *operatorpb.ReplaceNeighboursRequest) {
			request.Entries[1].NextHop = request.Entries[0].NextHop
		}},
		{name: "duplicate IP on different device", mutate: func(request *operatorpb.ReplaceNeighboursRequest) {
			request.Entries[1].NextHop = request.Entries[0].NextHop
			request.Entries[1].Device = "logical1"
		}},
		{name: "duplicate mapped IPv4", mutate: func(request *operatorpb.ReplaceNeighboursRequest) {
			request.Entries[1].NextHop = commonpb.NewIPAddressFromAddr(netip.MustParseAddr("::ffff:192.0.2.2"))
			request.Entries[1].Device = "logical1"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture, input, _, _ := newReadinessFixture(t, 20*time.Millisecond)
			require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("remote", 10, "192.0.2.1")))
			before := sourceEntries(t, fixture.Table, "remote")
			generation, _ := input.Generation()
			require.Eventually(t, func() bool { return !input.Available() }, time.Second, time.Millisecond)
			request := replacementRequest("remote", 200, "192.0.2.2", "192.0.2.3")
			tc.mutate(request)
			require.Equal(t, codes.InvalidArgument, status.Code(sendNeighbourSnapshot(t.Context(), fixture.Client, request)))
			require.Equal(t, before, sourceEntries(t, fixture.Table, "remote"))
			require.Equal(t, []neigh.SourceInfo{{Name: "remote", DefaultPriority: 10, EntryCount: 1}}, fixture.Table.ListSources())
			current, available := input.Generation()
			require.False(t, available)
			require.Equal(t, generation, current)
			require.Equal(t, int64(1), fixture.Changes.Load())
		})
	}
}

// sizedReplacement adds unknown wire data without changing semantic entries.
func sizedReplacement(t *testing.T, request *operatorpb.ReplaceNeighboursRequest, size int) *operatorpb.ReplaceNeighboursRequest {
	t.Helper()
	tag := protowire.AppendTag(nil, 100, protowire.BytesType)
	remaining := size - proto.Size(request) - len(tag)
	padding := remaining - protowire.SizeVarint(uint64(remaining))
	request.ProtoReflect().SetUnknown(protowire.AppendBytes(tag, make([]byte, padding)))
	require.Equal(t, size, proto.Size(request))
	return request
}

// numberedReplacement returns distinct IPv6 entries with maximal device payload.
func numberedReplacement(table string, count int, address netip.Addr) *operatorpb.ReplaceNeighboursRequest {
	request := replacementRequest(table, math.MaxUint32)
	for range count {
		entry := replacementRequest(table, 0, address.String()).Entries[0]
		entry.Device = strings.Repeat("d", 79)
		entry.Priority = math.MaxUint32
		request.Entries = append(request.Entries, entry)
		address = address.Next()
	}
	return request
}

// Test_NeighbourService_ReplacementLimits verifies inclusive entry and full
// protobuf byte limits in both the handler and the default gRPC transport.
func Test_NeighbourService_ReplacementLimits(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(fmt.Sprintf("direct=%t", direct), func(t *testing.T) {
			fixture := newNeighbourServiceFixture(t)
			replace := func(request *operatorpb.ReplaceNeighboursRequest) error {
				if direct {
					_, err := fixture.Service.ReplaceNeighbours(t.Context(), request)
					return err
				}
				return sendNeighbourSnapshot(t.Context(), fixture.Client, request)
			}
			request := numberedReplacement(strings.Repeat("t", operatorpb.NeighbourTableNameBytes), operatorpb.NeighbourSnapshotEntries, netip.MustParseAddr("2001:db8::1"))
			for _, entry := range request.Entries {
				entry.HardwareAddr.Addr, entry.LinkAddr.Addr = 1<<48-1, 1<<48-1
				entry.State = operatorpb.NeighbourState(-1)
			}
			require.LessOrEqual(t, proto.Size(request), operatorpb.NeighbourSnapshotBytes)
			require.NoError(t, replace(request))
			before := sourceEntries(t, fixture.Table, request.Table)
			request.Entries = append(request.Entries, replacementRequest(request.Table, 0, "2001:db8:1::1").Entries[0])
			require.Less(t, proto.Size(request), operatorpb.NeighbourSnapshotBytes)
			require.Equal(t, codes.ResourceExhausted, status.Code(replace(request)))
			require.Equal(t, before, sourceEntries(t, fixture.Table, request.Table))
			boundary := sizedReplacement(t, replacementRequest("snapshot", 100, "192.0.2.1"), operatorpb.NeighbourSnapshotBytes)
			require.NoError(t, replace(boundary))
			before = sourceEntries(t, fixture.Table, "snapshot")
			oversized := sizedReplacement(t, replacementRequest("snapshot", 200, "192.0.2.2"), operatorpb.NeighbourSnapshotBytes+1)
			require.Equal(t, codes.ResourceExhausted, status.Code(replace(oversized)))
			require.Equal(t, before, sourceEntries(t, fixture.Table, "snapshot"))
			require.NoError(t, replace(boundary))
			require.Equal(t, int64(2), fixture.Changes.Load())
		})
	}
}

// Test_NeighbourService_InterruptedReplacement verifies that cancellation before
// the handler commits cannot create, clear or refresh input.
func Test_NeighbourService_InterruptedReplacement(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, deadline := range []bool{false, true} {
			t.Run(fmt.Sprintf("existing=%t/deadline=%t", existing, deadline), func(t *testing.T) {
				fixture, input, _, _ := newReadinessFixture(t, time.Minute)
				if existing {
					require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("remote", 10, "192.0.2.1")))
				}
				before, _ := fixture.Table.View().All()
				metadata := fixture.Table.ListSources()
				generation, available := input.Generation()
				ctx, cancel := context.WithCancel(t.Context())
				if deadline {
					cancel()
					ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
				}
				cancel()
				response, err := fixture.Service.ReplaceNeighbours(ctx, replacementRequest("remote", 200, "192.0.2.2"))
				code := codes.Canceled
				if deadline {
					code = codes.DeadlineExceeded
				}
				require.Nil(t, response)
				require.Equal(t, code, status.Code(err))
				after, _ := fixture.Table.View().All()
				require.Equal(t, maps.Collect(before), maps.Collect(after))
				require.Equal(t, metadata, fixture.Table.ListSources())
				current, currentAvailable := input.Generation()
				require.Equal(t, generation, current)
				require.Equal(t, available, currentAvailable)
			})
		}
	}
}

// Test_NeighbourService_LostResponse verifies that a real commit survives a
// transport failure and repeating the same request is semantically idempotent.
func Test_NeighbourService_LostResponse(t *testing.T) {
	var calls atomic.Int32
	fixture := newInterceptedNeighbourFixture(t, func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		response, err := handler(ctx, request)
		if err == nil && info.FullMethod == operatorpb.NeighbourService_ReplaceNeighbours_FullMethodName && calls.Add(1) == 1 {
			return nil, status.Error(codes.Unavailable, "response lost after commit")
		}
		return response, err
	})
	request := replacementRequest("snapshot", 100, "192.0.2.1")
	require.Equal(t, codes.Unavailable, status.Code(sendNeighbourSnapshot(t.Context(), fixture.Client, request)))
	before := sourceEntries(t, fixture.Table, "snapshot")
	require.Len(t, before, 1)
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, request))
	require.Equal(t, before, sourceEntries(t, fixture.Table, "snapshot"))
	require.Equal(t, int64(1), fixture.Changes.Load())
}

// Test_NeighbourService_BuiltInReplacement verifies that even empty snapshots
// cannot alter a protected source's entries or default priority.
func Test_NeighbourService_BuiltInReplacement(t *testing.T) {
	fixture := newNeighbourServiceFixture(t)
	_, err := fixture.Table.CreateSource("kernel", 10, true)
	require.NoError(t, err)
	entry := neigh.NeighbourEntry{NextHop: netip.MustParseAddr("192.0.2.1"), UpdatedAt: time.Now()}
	require.NoError(t, fixture.Table.Add("kernel", []neigh.NeighbourEntry{entry}))
	before := sourceEntries(t, fixture.Table, "kernel")
	for _, request := range []*operatorpb.ReplaceNeighboursRequest{replacementRequest("kernel", 200, "192.0.2.2"), replacementRequest("kernel", 200)} {
		require.Equal(t, codes.FailedPrecondition, status.Code(sendNeighbourSnapshot(t.Context(), fixture.Client, request)))
		require.Equal(t, before, sourceEntries(t, fixture.Table, "kernel"))
		require.Equal(t, []neigh.SourceInfo{{Name: "kernel", DefaultPriority: 10, EntryCount: 1, BuiltIn: true}}, fixture.Table.ListSources())
		require.Zero(t, fixture.Changes.Load())
	}
}

// Test_NeighbourService_ConcurrentReaders verifies that named and merged unary
// reads see one whole generation while full snapshots are replaced concurrently.
func Test_NeighbourService_ConcurrentReaders(t *testing.T) {
	fixture := newNeighbourServiceFixture(t)
	first := replacementRequest("snapshot", 100, "192.0.2.1", "192.0.2.2")
	second := replacementRequest("snapshot", 200, "2001:db8::1", "2001:db8::2", "2001:db8::3")
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, first))
	var group errgroup.Group
	group.Go(func() error {
		for range 30 {
			for _, request := range []*operatorpb.ReplaceNeighboursRequest{second, first} {
				if err := sendNeighbourSnapshot(t.Context(), fixture.Client, request); err != nil {
					return err
				}
			}
		}
		return nil
	})
	for _, table := range []string{"", "snapshot"} {
		group.Go(func() error {
			for range 60 {
				response, err := fixture.Client.List(t.Context(), &operatorpb.ListNeighboursRequest{Table: table})
				if err != nil {
					return err
				}
				count := len(response.GetNeighbours())
				if count != 2 && count != 3 {
					return fmt.Errorf("partial snapshot of %d entries", count)
				}
				seen := map[netip.Addr]bool{}
				for _, entry := range response.GetNeighbours() {
					address, err := entry.GetNextHop().ToAddr()
					if err != nil || seen[address] || entry.GetSource() != "snapshot" ||
						(count == 2 && (!address.Is4() || entry.GetPriority() != 100)) ||
						(count == 3 && (!address.Is6() || entry.GetPriority() != 200)) {
						return errors.New("mixed snapshot")
					}
					seen[address] = true
				}
			}
			return nil
		})
	}
	require.NoError(t, group.Wait())
}

type neighbourGatewayFixture struct {
	*neighbourServiceFixture
	HTTP string
}

// newNeighbourGatewayClient runs the production TCP and JSON proxy paths with
// default receive sizes and authentication disabled only in this fixture.
func newNeighbourGatewayClient(t *testing.T) neighbourGatewayFixture {
	t.Helper()
	fixture := newNeighbourServiceFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	httpAddress := httpListener.Addr().String()
	require.NoError(t, httpListener.Close())
	config := gateway.DefaultConfig()
	config.Auth.Disabled = true
	config.Server.HTTPEndpoint = httpAddress
	proxy, err := gateway.NewGateway(config, gateway.WithListener(listener))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, proxy.Close()) })
	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error { return proxy.Run(ctx) })
	t.Cleanup(func() {
		cancel()
		require.NoError(t, group.Wait())
	})
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	registration, stopRegistration := context.WithTimeout(t.Context(), 10*time.Second)
	defer stopRegistration()
	_, err = ynpb.NewGatewayClient(connection).Register(registration,
		&ynpb.RegisterRequest{Backend: &ynpb.BackendDesc{
			Name: operatorpb.NeighbourService_ServiceDesc.ServiceName, Endpoint: fixture.Endpoint,
		}}, grpc.WaitForReady(true),
	)
	require.NoError(t, err)
	fixture.Client = operatorpb.NewNeighbourServiceClient(connection)
	fixture.Endpoint = "grpc://" + listener.Addr().String()
	return neighbourGatewayFixture{
		neighbourServiceFixture: fixture,
		HTTP:                    "http://" + httpAddress + "/api/" + operatorpb.NeighbourService_ServiceDesc.ServiceName,
	}
}

// Test_NeighbourService_ListThroughGateway verifies IP-priority merging and
// metadata through both proxy hops, including incremental static overrides.
func Test_NeighbourService_ListThroughGateway(t *testing.T) {
	fixture := newNeighbourGatewayClient(t)
	client := fixture.Client
	first := replacementRequest("first", 100, "192.0.2.1", "192.0.2.2")
	second := replacementRequest("second", 200, "::ffff:192.0.2.1", "2001:db8::1")
	second.Entries[0].Device = "logical1"
	for _, request := range []*operatorpb.ReplaceNeighboursRequest{first, second} {
		require.NoError(t, sendNeighbourSnapshot(t.Context(), client, request))
	}
	_, err := client.CreateTable(t.Context(), &operatorpb.CreateNeighbourTableRequest{Name: "static", DefaultPriority: 10})
	require.NoError(t, err)
	static := replacementRequest("static", 10, "192.0.2.2", "192.0.2.3")
	static.Entries[0].Device = "logical2"
	_, err = client.UpdateNeighbours(t.Context(), &operatorpb.UpdateNeighboursRequest{Table: "static", Entries: static.Entries})
	require.NoError(t, err)
	for _, table := range []string{"first", "second", "static", ""} {
		response, err := client.List(t.Context(), &operatorpb.ListNeighboursRequest{Table: table})
		require.NoError(t, err)
		expected := map[netip.Addr]*operatorpb.NeighbourEntry{}
		for _, request := range []*operatorpb.ReplaceNeighboursRequest{second, first, static} {
			if table != "" && table != request.Table {
				continue
			}
			for _, entry := range request.Entries {
				address, err := entry.NextHop.ToAddr()
				require.NoError(t, err)
				copy := proto.Clone(entry).(*operatorpb.NeighbourEntry)
				copy.NextHop = commonpb.NewIPAddressFromAddr(address.Unmap())
				copy.Source, copy.Priority, copy.State = request.Table, request.DefaultPriority, operatorpb.NeighbourState_NUD_PERMANENT
				expected[address.Unmap()] = copy
			}
		}
		require.Len(t, response.GetNeighbours(), len(expected))
		for _, entry := range response.GetNeighbours() {
			address, err := entry.NextHop.ToAddr()
			require.NoError(t, err)
			wanted := expected[address]
			require.NotNil(t, wanted)
			require.Positive(t, entry.UpdatedAt)
			wanted.UpdatedAt = entry.UpdatedAt
			require.True(t, proto.Equal(wanted, entry))
			delete(expected, address)
		}
		require.Empty(t, expected)
	}
}

// Test_NeighbourService_ListUnaryLimits verifies inclusive full-response sizing
// through the real gateway and independent named and merged overflow failures.
func Test_NeighbourService_ListUnaryLimits(t *testing.T) {
	fixture := newNeighbourGatewayClient(t)
	table := strings.Repeat("t", operatorpb.NeighbourTableNameBytes)
	_, err := fixture.Table.CreateSource(table, math.MaxUint32, false)
	require.NoError(t, err)
	address := netip.MustParseAddr("2001:db8::1")
	wire := numberedReplacement(table, 1, address).Entries[0]
	wire.Source, wire.State, wire.UpdatedAt = table, operatorpb.NeighbourState_NUD_PERMANENT, 1<<32
	entryBytes := protowire.SizeTag(1) + protowire.SizeBytes(proto.Size(wire))
	count := operatorpb.NeighbourListUnaryBytes/entryBytes + 1
	excess := count*entryBytes - operatorpb.NeighbourListUnaryBytes
	entries := make([]neigh.NeighbourEntry, 0, count)
	for range count {
		device := wire.Device
		shrink := min(excess, len(device)-1)
		device = device[:len(device)-shrink]
		excess -= shrink
		entries = append(entries, neigh.NeighbourEntry{
			NextHop: address, UpdatedAt: time.Unix(wire.UpdatedAt, 0), Priority: wire.Priority,
			State:         neigh.NeighbourStatePermanent,
			HardwareRoute: neigh.HardwareRoute{Device: device, SourceMAC: wire.HardwareAddr.EUI48(), DestinationMAC: wire.LinkAddr.EUI48()},
		})
		address = address.Next()
	}
	require.Zero(t, excess)
	require.NoError(t, fixture.Table.Add(table, entries))
	for _, name := range []string{table, ""} {
		response, err := fixture.Client.List(t.Context(), &operatorpb.ListNeighboursRequest{Table: name})
		require.NoError(t, err)
		require.Len(t, response.GetNeighbours(), count)
		require.Equal(t, operatorpb.NeighbourListUnaryBytes, proto.Size(response))
	}
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("small", 100, "192.0.2.1")))
	response, err := fixture.Client.List(t.Context(), &operatorpb.ListNeighboursRequest{})
	require.Nil(t, response)
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	for _, tc := range []struct {
		name  string
		count int
	}{{name: table, count: count}, {name: "small", count: 1}} {
		response, err := fixture.Client.List(t.Context(), &operatorpb.ListNeighboursRequest{Table: tc.name})
		require.NoError(t, err)
		require.Len(t, response.GetNeighbours(), tc.count)
	}
	next := entries[0]
	next.NextHop = address
	require.NoError(t, fixture.Table.Add(table, []neigh.NeighbourEntry{next}))
	for _, name := range []string{table, ""} {
		response, err := fixture.Service.List(t.Context(), &operatorpb.ListNeighboursRequest{Table: name})
		require.Nil(t, response)
		require.Equal(t, codes.ResourceExhausted, status.Code(err))
		response, err = fixture.Client.List(t.Context(), &operatorpb.ListNeighboursRequest{Table: name})
		require.Nil(t, response)
		require.Equal(t, codes.ResourceExhausted, status.Code(err))
		checkNeighbourHTTPList(t, fixture.HTTP, name, 0, codes.ResourceExhausted)
		t.Run("CLI overflow/"+name, func(t *testing.T) {
			checkNeighbourCLIError(t, fixture.Endpoint, "ResourceExhausted", "--table", name)
		})
	}
	checkNeighbourHTTPList(t, fixture.HTTP, "small", 1, codes.OK)
}

// checkNeighbourHTTPList checks JSON data or real gRPC-to-HTTP error translation.
func checkNeighbourHTTPList(t *testing.T, endpoint, table string, count int, code codes.Code) {
	t.Helper()
	httpClient := &http.Client{Timeout: 10 * time.Second}
	t.Cleanup(httpClient.CloseIdleConnections)
	var response *http.Response
	require.Eventually(t, func() bool {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint+"/List",
			strings.NewReader(fmt.Sprintf(`{"table":%q}`, table)),
		)
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		response, err = httpClient.Do(request)
		return err == nil
	}, 10*time.Second, time.Millisecond)
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	if code == codes.OK {
		require.Equal(t, http.StatusOK, response.StatusCode, string(data))
		var result operatorpb.ListNeighboursResponse
		require.NoError(t, json.Unmarshal(data, &result))
		require.Len(t, result.GetNeighbours(), count)
	} else {
		wantedStatus := map[codes.Code]int{codes.NotFound: http.StatusNotFound, codes.ResourceExhausted: http.StatusTooManyRequests}
		require.Equal(t, wantedStatus[code], response.StatusCode, string(data))
		require.Contains(t, response.Header.Get("Content-Type"), "text/plain")
		require.NotEmpty(t, data)
		require.NotContains(t, string(data), `"neighbours":`)
	}
}

// Test_NeighbourService_ListUnaryHTTP verifies that real JSON reads distinguish
// populated named and merged views, empty data and unknown sources.
func Test_NeighbourService_ListUnaryHTTP(t *testing.T) {
	fixture := newNeighbourGatewayClient(t)
	for _, request := range []*operatorpb.ReplaceNeighboursRequest{
		replacementRequest("first", 100, "192.0.2.1"), replacementRequest("second", 100, "2001:db8::1"), replacementRequest("empty", 100),
	} {
		require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, request))
	}
	checkNeighbourHTTPList(t, fixture.HTTP, "", 2, codes.OK)
	checkNeighbourHTTPList(t, fixture.HTTP, "first", 1, codes.OK)
	checkNeighbourHTTPList(t, fixture.HTTP, "empty", 0, codes.OK)
	checkNeighbourHTTPList(t, fixture.HTTP, "missing", 0, codes.NotFound)
}

// Test_NeighbourService_ListCancellation verifies that cancelled and unknown
// reads fail rather than returning a successful empty response.
func Test_NeighbourService_ListCancellation(t *testing.T) {
	fixture := newNeighbourServiceFixture(t)
	response, err := fixture.Client.List(t.Context(), &operatorpb.ListNeighboursRequest{})
	require.NoError(t, err)
	require.Empty(t, response.GetNeighbours())
	response, err = fixture.Client.List(t.Context(), &operatorpb.ListNeighboursRequest{Table: "missing"})
	require.Nil(t, response)
	require.Equal(t, codes.NotFound, status.Code(err))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	response, err = fixture.Service.List(ctx, &operatorpb.ListNeighboursRequest{})
	require.Nil(t, response)
	require.Equal(t, codes.Canceled, status.Code(err))
}

// checkedNeighbourContext signals entry into a real handler's context checks.
type checkedNeighbourContext struct {
	context.Context
	OnCheck func()
}

func (m checkedNeighbourContext) Err() error {
	err := m.Context.Err()
	m.OnCheck()
	return err
}

// Test_NeighbourService_CancellationBeforeCommit verifies that cancellation
// behind a table writer cannot change content or refresh expired input.
func Test_NeighbourService_CancellationBeforeCommit(t *testing.T) {
	fixture, input, _, _ := newReadinessFixture(t, 20*time.Millisecond)
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("remote", 100, "192.0.2.1")))
	before := sourceEntries(t, fixture.Table, "remote")
	generation, _ := input.Generation()
	require.Eventually(t, func() bool { return !input.Available() }, time.Second, time.Millisecond)
	locked, release := make(chan struct{}), make(chan struct{})
	resume := sync.OnceFunc(func() { close(release) })
	writerContext := checkedNeighbourContext{Context: t.Context(), OnCheck: sync.OnceFunc(func() {
		close(locked)
		<-release
	})}
	t.Cleanup(resume)
	first := make(chan error, 1)
	go func() {
		_, err := fixture.Table.ReplaceSource(writerContext, "remote", 100, before)
		first <- err
	}()
	awaitGatewayResult(t, locked)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	checked := make(chan struct{})
	var checks atomic.Int32
	requestContext := checkedNeighbourContext{Context: ctx, OnCheck: func() {
		if checks.Add(1) == 2 {
			close(checked)
		}
	}}
	second := make(chan error, 1)
	go func() {
		_, err := fixture.Service.ReplaceNeighbours(requestContext, replacementRequest("remote", 200, "192.0.2.2"))
		second <- err
	}()
	awaitGatewayResult(t, checked)
	cancel()
	resume()
	require.NoError(t, awaitGatewayResult(t, first))
	require.Equal(t, codes.Canceled, status.Code(awaitGatewayResult(t, second)))
	require.Equal(t, before, sourceEntries(t, fixture.Table, "remote"))
	current, available := input.Generation()
	require.False(t, available)
	require.Equal(t, generation, current)
	require.Equal(t, int64(1), fixture.Changes.Load())
}

// runNeighbourCLI invokes the opt-in official binary against a real gateway.
func runNeighbourCLI(t *testing.T, endpoint string, arguments ...string) (string, string, error) {
	t.Helper()
	binary := os.Getenv("YANET_NEIGHBOUR_CLI_BINARY")
	if binary == "" {
		t.Skip("set YANET_NEIGHBOUR_CLI_BINARY to the built neighbour CLI")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, append([]string{"--endpoint", endpoint, "show"}, arguments...)...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	return stdout.String(), stderr.String(), err
}

// checkNeighbourCLIError rejects partial output in both table and JSON formats.
func checkNeighbourCLIError(t *testing.T, endpoint, code string, arguments ...string) {
	t.Helper()
	stdout, stderr, err := runNeighbourCLI(t, endpoint, arguments...)
	require.Error(t, err)
	require.NotEmpty(t, stderr)
	require.Empty(t, stdout)
	stdout, stderr, err = runNeighbourCLI(t, endpoint, append(arguments, "--format", "json")...)
	require.Error(t, err, stderr)
	var failure struct {
		OK    *bool `json:"ok"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &failure), stdout)
	require.NotNil(t, failure.OK)
	require.False(t, *failure.OK)
	require.Equal(t, code, failure.Error.Code)
}

// Test_NeighbourService_UnaryCLI verifies that the official client preserves
// named and merged JSON shapes and reports missing sources without partial data.
func Test_NeighbourService_UnaryCLI(t *testing.T) {
	fixture := newNeighbourGatewayClient(t)
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("snapshot", 100, "192.0.2.1", "2001:db8::1")))
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementRequest("other", 100, "2001:db8::2")))
	for _, table := range []string{"", "snapshot"} {
		arguments := []string{}
		addresses := []string{"192.0.2.1", "2001:db8::1"}
		if table != "" {
			arguments = append(arguments, "--table", table)
		} else {
			addresses = append(addresses, "2001:db8::2")
		}
		stdout, stderr, err := runNeighbourCLI(t, fixture.Endpoint, arguments...)
		require.NoError(t, err, stderr)
		for _, address := range addresses {
			require.Contains(t, stdout, address)
		}
		if table != "" {
			require.NotContains(t, stdout, "2001:db8::2")
		}
		stdout, stderr, err = runNeighbourCLI(t, fixture.Endpoint, append(arguments, "--format", "json")...)
		require.NoError(t, err, stderr)
		var entries []map[string]any
		require.NoError(t, json.Unmarshal([]byte(stdout), &entries))
		require.Len(t, entries, len(addresses))
		listed := []string{}
		for _, entry := range entries {
			require.NotContains(t, entry, "ifindex")
			address, ok := entry["next_hop"].(string)
			require.True(t, ok)
			listed = append(listed, address)
			require.Equal(t, "PERMANENT", entry["state"])
		}
		require.ElementsMatch(t, addresses, listed)
	}
	checkNeighbourCLIError(t, fixture.Endpoint, "NotFound", "--table", "missing")
}
