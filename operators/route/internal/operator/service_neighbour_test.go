package operator_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net"
	"net/netip"
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
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/readiness"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/operators/route/internal/operator"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// Test_NeighbourService_RemoteRemoval verifies that incremental deletion cannot
// mutate complete remote input while explicit and implicit static edits work.
func Test_NeighbourService_RemoteRemoval(t *testing.T) {
	tracker := readiness.NewTracker([]readiness.ScopeSpec{{Name: "neighbours"}})
	input := operator.NewNeighbourReadiness("remote", time.Minute, tracker)
	fixture := newNeighbourServiceFixture(t,
		operator.WithNeighbourServiceRemoteSource("remote", []string{"logical0"}),
		operator.WithNeighbourServiceReadiness(input),
	)
	chunk := replacementChunk("remote", 100, "192.0.2.1")
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, chunk))
	before := sourceEntries(t, fixture.Table, "remote")
	generation, available := input.Generation()
	require.True(t, available)
	changes := fixture.Changes.Load()
	_, err := fixture.Client.RemoveNeighbours(t.Context(), &operatorpb.RemoveNeighboursRequest{
		Table: "remote", NextHops: []*commonpb.IPAddress{chunk.Entries[0].NextHop},
	})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Equal(t, before, sourceEntries(t, fixture.Table, "remote"))
	require.Equal(t, changes, fixture.Changes.Load())
	current, available := input.Generation()
	require.True(t, available)
	require.Equal(t, generation, current)
	_, err = fixture.Table.CreateSource("static", 10, true)
	require.NoError(t, err)
	for _, table := range []string{"static", ""} {
		_, err = fixture.Client.UpdateNeighbours(t.Context(), &operatorpb.UpdateNeighboursRequest{Table: table, Entries: chunk.Entries})
		require.NoError(t, err)
		_, err = fixture.Client.RemoveNeighbours(t.Context(), &operatorpb.RemoveNeighboursRequest{
			Table: table, NextHops: []*commonpb.IPAddress{chunk.Entries[0].NextHop},
		})
		require.NoError(t, err)
		require.Empty(t, sourceEntries(t, fixture.Table, "static"))
	}
	require.Equal(t, before, sourceEntries(t, fixture.Table, "remote"))
}

type replacementProgress struct {
	Chunks   int
	Finished bool
	Error    error
}

// replacementObserver records when the real handler requests another chunk.
//
// The next receive starts only after validation and staging of the prior chunk,
// so assertions about incomplete streams do not depend on transport timing.
type replacementObserver struct {
	mu       sync.Mutex
	progress map[string]replacementProgress
}

// Record serializes progress updates from independent server streams.
func (m *replacementObserver) Record(identity string, finished bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	progress := m.progress[identity]
	if finished {
		progress.Finished, progress.Error = true, err
	} else {
		progress.Chunks++
	}
	m.progress[identity] = progress
}

// Progress returns an independent observation for a single stream.
func (m *replacementObserver) Progress(identity string) replacementProgress {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.progress[identity]
}

type observedReplacementStream struct {
	grpc.ServerStream
	Observer *replacementObserver
	Identity string
	received bool
}

// RecvMsg observes accepted chunks without replacing any service logic.
func (m *observedReplacementStream) RecvMsg(message any) error {
	if m.received {
		m.Observer.Record(m.Identity, false, nil)
	}
	err := m.ServerStream.RecvMsg(message)
	m.received = err == nil
	return err
}

type neighbourServiceFixture struct {
	Table    *neigh.NeighTable
	Client   operatorpb.NeighbourServiceClient
	Changes  atomic.Int64
	Observer *replacementObserver
	Sequence atomic.Int64
}

// newNeighbourServiceFixture serves the production handler with default caps.
func newNeighbourServiceFixture(t *testing.T, options ...operator.NeighbourServiceOption) *neighbourServiceFixture {
	t.Helper()
	fixture := &neighbourServiceFixture{
		Table:    neigh.NewNeighTable(),
		Observer: &replacementObserver{progress: map[string]replacementProgress{}},
	}
	options = append(options, operator.WithNeighbourServiceOnChanged(func() { fixture.Changes.Add(1) }))
	service := operator.NewNeighbourService(fixture.Table, options...)
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer(grpc.StreamInterceptor(func(
		service any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler,
	) error {
		identities := metadata.ValueFromIncomingContext(stream.Context(), "snapshot-id")
		identity := ""
		if len(identities) != 0 {
			identity = identities[0]
		}
		err := handler(service, &observedReplacementStream{
			ServerStream: stream, Observer: fixture.Observer, Identity: identity,
		})
		fixture.Observer.Record(identity, true, err)
		return err
	}))
	operatorpb.RegisterNeighbourServiceServer(server, service)
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
	connection, err := grpc.NewClient("passthrough:///neighbour-service",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	fixture.Client = operatorpb.NewNeighbourServiceClient(connection)
	return fixture
}

// Open starts an independently observable generated-client stream.
func (m *neighbourServiceFixture) Open(t *testing.T, ctx context.Context) (grpc.ClientStreamingClient[operatorpb.ReplaceNeighboursRequest, operatorpb.ReplaceNeighboursResponse], string) {
	t.Helper()
	identity := fmt.Sprint(m.Sequence.Add(1))
	stream, err := m.Client.ReplaceNeighbours(metadata.AppendToOutgoingContext(ctx, "snapshot-id", identity))
	require.NoError(t, err)
	return stream, identity
}

// WaitStaged waits for the handler to accept the specified number of chunks.
func (m *neighbourServiceFixture) WaitStaged(t *testing.T, identity string, count int) {
	t.Helper()
	require.Eventually(t, func() bool {
		return m.Observer.Progress(identity).Chunks >= count
	}, 5*time.Second, time.Millisecond)
}

// WaitFinished waits until an aborted handler has released its staging slot.
func (m *neighbourServiceFixture) WaitFinished(t *testing.T, identity string) error {
	t.Helper()
	require.Eventually(t, func() bool {
		return m.Observer.Progress(identity).Finished
	}, 5*time.Second, time.Millisecond)
	return m.Observer.Progress(identity).Error
}

// replacementChunk returns bounded entries with server-owned metadata unset.
func replacementChunk(table string, priority uint32, addresses ...string) *operatorpb.ReplaceNeighboursRequest {
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

// sendNeighbourSnapshot returns the final server status, including send EOFs.
func sendNeighbourSnapshot(ctx context.Context, client operatorpb.NeighbourServiceClient, chunks ...*operatorpb.ReplaceNeighboursRequest) error {
	stream, err := client.ReplaceNeighbours(ctx)
	if err != nil {
		return err
	}
	for _, chunk := range chunks {
		if err := stream.Send(chunk); err != nil {
			if !errors.Is(err, io.EOF) {
				return err
			}
			break
		}
	}
	_, err = stream.CloseAndRecv()
	return err
}

// sourceEntries copies a published view for timestamp and alias assertions.
func sourceEntries(t *testing.T, table *neigh.NeighTable, name string) map[neigh.Key]neigh.NeighbourEntry {
	t.Helper()
	view, found := table.SourceView(name)
	require.True(t, found)
	entries, _ := view.All()
	return maps.Collect(entries)
}

// Test_NeighbourService_AtomicReplacement verifies that entries and priority
// become visible together only on EOF.
func Test_NeighbourService_AtomicReplacement(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing=%t", existing), func(t *testing.T) {
			fixture := newNeighbourServiceFixture(t)
			if existing {
				require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client,
					replacementChunk("snapshot", 10, "192.0.2.1"),
				))
			}
			before := fixture.Table.ListSources()
			oldMerged := fixture.Table.View()
			changes := fixture.Changes.Load()
			first := replacementChunk("snapshot", 200, "192.0.2.2")
			second := replacementChunk("snapshot", 200, "2001:db8::1")
			second.Entries[0].Priority = 7
			stream, identity := fixture.Open(t, t.Context())
			for idx, chunk := range []*operatorpb.ReplaceNeighboursRequest{first, second} {
				require.NoError(t, stream.Send(chunk))
				fixture.WaitStaged(t, identity, idx+1)
				require.Equal(t, before, fixture.Table.ListSources())
				require.Equal(t, changes, fixture.Changes.Load())
				_, found := fixture.Table.View().Lookup(neigh.NewKey(netip.MustParseAddr("192.0.2.2"), "logical0"))
				require.False(t, found)
			}
			_, err := stream.CloseAndRecv()
			require.NoError(t, err)
			require.Equal(t, changes+1, fixture.Changes.Load())
			require.Equal(t, []neigh.SourceInfo{{Name: "snapshot", DefaultPriority: 200, EntryCount: 2}}, fixture.Table.ListSources())
			entries := sourceEntries(t, fixture.Table, "snapshot")
			require.Equal(t, uint32(200), entries[neigh.NewKey(netip.MustParseAddr("192.0.2.2"), "logical0")].Priority)
			require.Equal(t, uint32(7), entries[neigh.NewKey(netip.MustParseAddr("2001:db8::1"), "logical0")].Priority)
			for _, entry := range entries {
				require.Equal(t, neigh.NeighbourStatePermanent, entry.State)
				require.False(t, entry.UpdatedAt.IsZero())
			}
			_, found := oldMerged.Lookup(neigh.NewKey(netip.MustParseAddr("192.0.2.2"), "logical0"))
			require.False(t, found)
			_, found = fixture.Table.View().Lookup(neigh.NewKey(netip.MustParseAddr("192.0.2.1"), "logical0"))
			require.False(t, found)

			response, err := fixture.Client.List(t.Context(), &operatorpb.ListNeighboursRequest{Table: "snapshot"})
			require.NoError(t, err)
			require.Len(t, response.GetNeighbours(), 2)
			for _, entry := range response.GetNeighbours() {
				require.Equal(t, "snapshot", entry.GetSource())
			}
		})
	}
}

// Test_NeighbourService_EquivalentReplacement verifies that equivalent content
// in a different chunk order retains timestamps and produces no wake.
func Test_NeighbourService_EquivalentReplacement(t *testing.T) {
	fixture := newNeighbourServiceFixture(t)
	first := replacementChunk("snapshot", 100, "192.0.2.1")
	second := replacementChunk("snapshot", 100, "2001:db8::1")
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, first, second))
	before := sourceEntries(t, fixture.Table, "snapshot")
	changes := fixture.Changes.Load()
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, second, first))
	require.Equal(t, before, sourceEntries(t, fixture.Table, "snapshot"))
	require.Equal(t, changes, fixture.Changes.Load())
}

// Test_NeighbourService_EmptyReplacement verifies that an explicit empty
// snapshot clears a source once and an equivalent empty refresh stays silent.
func Test_NeighbourService_EmptyReplacement(t *testing.T) {
	fixture := newNeighbourServiceFixture(t)
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementChunk("snapshot", 100, "192.0.2.1")))
	for range 2 {
		require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementChunk("snapshot", 100)))
		require.Empty(t, sourceEntries(t, fixture.Table, "snapshot"))
		require.Equal(t, []neigh.SourceInfo{{Name: "snapshot", DefaultPriority: 100}}, fixture.Table.ListSources())
		require.Equal(t, int64(2), fixture.Changes.Load())
	}
}

// Test_NeighbourService_DeviceNameBoundary verifies that both incremental and
// complete updates preserve byte-exact device names within the dataplane ABI.
func Test_NeighbourService_DeviceNameBoundary(t *testing.T) {
	for _, test := range []struct {
		name   string
		device string
		valid  bool
	}{
		{name: "79 ASCII bytes", device: strings.Repeat("d", 79), valid: true},
		{name: "80 ASCII bytes", device: strings.Repeat("d", 80)},
		{name: "79 UTF-8 bytes", device: strings.Repeat("é", 39) + "a", valid: true},
		{name: "80 UTF-8 bytes", device: strings.Repeat("é", 40)},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newNeighbourServiceFixture(t)
			_, err := fixture.Table.CreateSource("static", 100, true)
			require.NoError(t, err)
			chunk := replacementChunk("remote", 100, "192.0.2.1")
			chunk.Entries[0].Device = test.device
			for _, replace := range []bool{false, true} {
				table := "static"
				if replace {
					table = "remote"
					err = sendNeighbourSnapshot(t.Context(), fixture.Client, chunk)
				} else {
					_, err = fixture.Client.UpdateNeighbours(t.Context(), &operatorpb.UpdateNeighboursRequest{Table: table, Entries: chunk.Entries})
				}
				if test.valid {
					require.NoError(t, err)
					entries := sourceEntries(t, fixture.Table, table)
					require.Contains(t, entries, neigh.NewKey(netip.MustParseAddr("192.0.2.1"), test.device))
				} else {
					require.Equal(t, codes.InvalidArgument, status.Code(err))
				}
			}
		})
	}
}

// Test_NeighbourService_PairIdentity verifies that equal IPs on distinct devices
// retain their scope and mapped IPv4 duplicates cannot replace last-good data.
func Test_NeighbourService_PairIdentity(t *testing.T) {
	fixture := newNeighbourServiceFixture(t)
	first := replacementChunk("snapshot", 100, "192.0.2.1")
	first.Entries[0].Ifindex = 10
	second := replacementChunk("snapshot", 100, "192.0.2.1")
	second.Entries[0].Device = "logical1"
	second.Entries[0].Ifindex = 20
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, first, second))
	before := sourceEntries(t, fixture.Table, "snapshot")
	require.Len(t, before, 2)
	require.Equal(t, uint32(10), before[neigh.NewKey(netip.MustParseAddr("192.0.2.1"), "logical0")].Ifindex)
	require.Equal(t, uint32(20), before[neigh.NewKey(netip.MustParseAddr("192.0.2.1"), "logical1")].Ifindex)
	duplicate := replacementChunk("snapshot", 100, "::ffff:192.0.2.1")
	require.Equal(t, codes.InvalidArgument, status.Code(sendNeighbourSnapshot(t.Context(), fixture.Client, first, duplicate)))
	require.Equal(t, before, sourceEntries(t, fixture.Table, "snapshot"))
}

// Test_NeighbourService_InvalidReplacement verifies that malformed later chunks
// cannot publish any staged entries or change last-good table metadata.
func Test_NeighbourService_InvalidReplacement(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*operatorpb.ReplaceNeighboursRequest, *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest
	}{
		{name: "empty stream", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			return nil
		}},
		{name: "missing table", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			first.Table = ""
			return []*operatorpb.ReplaceNeighboursRequest{first}
		}},
		{name: "overlong table", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			first.Table = strings.Repeat("t", 129)
			return []*operatorpb.ReplaceNeighboursRequest{first}
		}},
		{name: "invalid table prefix", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			first.Table = "_snapshot"
			return []*operatorpb.ReplaceNeighboursRequest{first}
		}},
		{name: "non ASCII table", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			first.Table = "\u0442\u0430\u0431\u043b\u0438\u0446\u0430"
			return []*operatorpb.ReplaceNeighboursRequest{first}
		}},
		{name: "different table", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			second.Table = "another"
			return []*operatorpb.ReplaceNeighboursRequest{first, second}
		}},
		{name: "different priority", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			second.DefaultPriority++
			return []*operatorpb.ReplaceNeighboursRequest{first, second}
		}},
		{name: "nonempty then empty", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			second.Entries = nil
			return []*operatorpb.ReplaceNeighboursRequest{first, second}
		}},
		{name: "empty then nonempty", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			first.Entries = nil
			return []*operatorpb.ReplaceNeighboursRequest{first, second}
		}},
		{name: "two empty chunks", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			first.Entries, second.Entries = nil, nil
			return []*operatorpb.ReplaceNeighboursRequest{first, second}
		}},
		{name: "duplicate across chunks", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			return []*operatorpb.ReplaceNeighboursRequest{first, first}
		}},
		{name: "duplicate within chunk", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			second.Entries = append(second.Entries, second.Entries[0])
			return []*operatorpb.ReplaceNeighboursRequest{first, second}
		}},
		{name: "nil entry", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			second.Entries[0] = nil
			return []*operatorpb.ReplaceNeighboursRequest{first, second}
		}},
		{name: "nil next hop", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			second.Entries[0].NextHop = nil
			return []*operatorpb.ReplaceNeighboursRequest{first, second}
		}},
		{name: "short next hop", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			second.Entries[0].NextHop.Addr = []byte{1, 2, 3}
			return []*operatorpb.ReplaceNeighboursRequest{first, second}
		}},
		{name: "nil source MAC", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			second.Entries[0].HardwareAddr = nil
			return []*operatorpb.ReplaceNeighboursRequest{first, second}
		}},
		{name: "nil destination MAC", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			second.Entries[0].LinkAddr = nil
			return []*operatorpb.ReplaceNeighboursRequest{first, second}
		}},
		{name: "source MAC above EUI48", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			second.Entries[0].HardwareAddr.Addr = 1 << 48
			return []*operatorpb.ReplaceNeighboursRequest{first, second}
		}},
		{name: "destination MAC above EUI48", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			second.Entries[0].LinkAddr.Addr = 1 << 48
			return []*operatorpb.ReplaceNeighboursRequest{first, second}
		}},
		{name: "overlong device", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			second.Entries[0].Device = strings.Repeat("d", 80)
			return []*operatorpb.ReplaceNeighboursRequest{first, second}
		}},
		{name: "client owned source", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			second.Entries[0].Source = "snapshot"
			return []*operatorpb.ReplaceNeighboursRequest{first, second}
		}},
		{name: "client owned timestamp", mutate: func(first, second *operatorpb.ReplaceNeighboursRequest) []*operatorpb.ReplaceNeighboursRequest {
			second.Entries[0].UpdatedAt = 1
			return []*operatorpb.ReplaceNeighboursRequest{first, second}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newNeighbourServiceFixture(t)
			good := replacementChunk("snapshot", 10, "192.0.2.1")
			require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, good))
			before := sourceEntries(t, fixture.Table, "snapshot")
			metadata := fixture.Table.ListSources()
			chunks := test.mutate(replacementChunk("snapshot", 200, "192.0.2.2"), replacementChunk("snapshot", 200, "192.0.2.3"))
			err := sendNeighbourSnapshot(t.Context(), fixture.Client, chunks...)
			require.Equal(t, codes.InvalidArgument, status.Code(err), "%v", err)
			require.Equal(t, before, sourceEntries(t, fixture.Table, "snapshot"))
			require.Equal(t, metadata, fixture.Table.ListSources())
			require.Equal(t, int64(1), fixture.Changes.Load())
		})
	}
}

// sizedReplacement adds unknown protobuf data to exercise serialized byte caps.
func sizedReplacement(t *testing.T, request *operatorpb.ReplaceNeighboursRequest, size int) *operatorpb.ReplaceNeighboursRequest {
	t.Helper()
	unknown := protowire.AppendTag(nil, 100, protowire.BytesType)
	unknown = protowire.AppendBytes(unknown, make([]byte, size-proto.Size(request)-len(unknown)-3))
	request.ProtoReflect().SetUnknown(unknown)
	require.Equal(t, size, proto.Size(request))
	return request
}

// Test_NeighbourService_ReplacementLimits verifies that per-chunk and aggregate
// limits reject the whole snapshot and release the staging slot for a retry.
func Test_NeighbourService_ReplacementLimits(t *testing.T) {
	first := replacementChunk("snapshot", 200, "192.0.2.2")
	second := replacementChunk("snapshot", 200, "192.0.2.3")
	tooMany := replacementChunk("snapshot", 200)
	for idx := range 1001 {
		tooMany.Entries = append(tooMany.Entries, replacementChunk("snapshot", 200,
			fmt.Sprintf("2001:db8::%x", idx+1),
		).Entries[0])
	}
	for _, test := range []struct {
		name   string
		limits operator.NeighbourReplacementLimits
		chunks []*operatorpb.ReplaceNeighboursRequest
	}{
		{name: "chunk entry count", chunks: []*operatorpb.ReplaceNeighboursRequest{first, tooMany}},
		{name: "chunk serialized bytes", chunks: []*operatorpb.ReplaceNeighboursRequest{first,
			sizedReplacement(t, proto.Clone(second).(*operatorpb.ReplaceNeighboursRequest), 256*1024+1),
		}},
		{name: "staged entry count", limits: operator.NeighbourReplacementLimits{MaxEntries: 1}, chunks: []*operatorpb.ReplaceNeighboursRequest{first, second}},
		{name: "staged serialized bytes", limits: operator.NeighbourReplacementLimits{MaxBytes: proto.Size(first) + proto.Size(second) - 1}, chunks: []*operatorpb.ReplaceNeighboursRequest{first, second}},
	} {
		t.Run(test.name, func(t *testing.T) {
			limits := test.limits
			limits.MaxConcurrentStreams = 1
			fixture := newNeighbourServiceFixture(t, operator.WithNeighbourReplacementLimits(limits))
			good := replacementChunk("snapshot", 10, "192.0.2.1")
			require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, good))
			before := sourceEntries(t, fixture.Table, "snapshot")
			metadata := fixture.Table.ListSources()
			err := sendNeighbourSnapshot(t.Context(), fixture.Client, test.chunks...)
			require.Equal(t, codes.ResourceExhausted, status.Code(err), "%v", err)
			require.Equal(t, before, sourceEntries(t, fixture.Table, "snapshot"))
			require.Equal(t, metadata, fixture.Table.ListSources())
			require.Equal(t, int64(1), fixture.Changes.Load())
			require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, good))
		})
	}

	t.Run("inclusive entry and byte boundaries", func(t *testing.T) {
		boundary := sizedReplacement(t, second, 256*1024)
		fixture := newNeighbourServiceFixture(t, operator.WithNeighbourReplacementLimits(operator.NeighbourReplacementLimits{
			MaxEntries: 2, MaxBytes: proto.Size(first) + proto.Size(boundary),
		}))
		require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, first, boundary))
		require.Len(t, sourceEntries(t, fixture.Table, "snapshot"), 2)
	})
}

// Test_NeighbourService_InterruptedReplacement verifies that cancellation and
// deadlines preserve the previous table, including when no source existed.
func Test_NeighbourService_InterruptedReplacement(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, deadline := range []bool{false, true} {
			t.Run(fmt.Sprintf("existing=%t/deadline=%t", existing, deadline), func(t *testing.T) {
				fixture := newNeighbourServiceFixture(t, operator.WithNeighbourReplacementLimits(operator.NeighbourReplacementLimits{MaxConcurrentStreams: 1}))
				if existing {
					require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementChunk("snapshot", 10, "192.0.2.1")))
				}
				before := fixture.Table.ListSources()
				oldView := fixture.Table.View()
				changes := fixture.Changes.Load()
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if deadline {
					ctx, cancel = context.WithTimeout(ctx, time.Second)
					defer cancel()
				}
				stream, identity := fixture.Open(t, ctx)
				require.NoError(t, stream.Send(replacementChunk("snapshot", 200, "192.0.2.2")))
				fixture.WaitStaged(t, identity, 1)
				code := codes.Canceled
				if deadline {
					code = codes.DeadlineExceeded
					<-ctx.Done()
				} else {
					cancel()
				}
				// Observe the transport abort before a graceful half-close could
				// race it and commit a clean EOF instead.
				serverError := fixture.WaitFinished(t, identity)
				_, err := stream.CloseAndRecv()
				require.Equal(t, code, status.Code(err))
				if deadline {
					// Client expiry can reach the server as a transport cancellation
					// before its independently scheduled deadline fires.
					require.Contains(t, []codes.Code{codes.Canceled, codes.DeadlineExceeded}, status.Code(serverError))
				} else {
					require.Equal(t, code, status.Code(serverError))
				}
				require.Equal(t, before, fixture.Table.ListSources())
				oldEntries, _ := oldView.All()
				currentEntries, _ := fixture.Table.View().All()
				require.Equal(t, maps.Collect(oldEntries), maps.Collect(currentEntries))
				require.Equal(t, changes, fixture.Changes.Load())
				require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementChunk("snapshot", 200)))
			})
		}
	}
}

// Test_NeighbourService_BuiltInReplacement verifies that even an empty complete
// snapshot cannot change entries or priority of a protected source.
func Test_NeighbourService_BuiltInReplacement(t *testing.T) {
	fixture := newNeighbourServiceFixture(t)
	_, err := fixture.Table.CreateSource("kernel", 10, true)
	require.NoError(t, err)
	entry := neigh.NeighbourEntry{NextHop: netip.MustParseAddr("192.0.2.1"), UpdatedAt: time.Now()}
	require.NoError(t, fixture.Table.Add("kernel", []neigh.NeighbourEntry{entry}))
	before := sourceEntries(t, fixture.Table, "kernel")
	for _, chunk := range []*operatorpb.ReplaceNeighboursRequest{replacementChunk("kernel", 200, "192.0.2.2"), replacementChunk("kernel", 200)} {
		err := sendNeighbourSnapshot(t.Context(), fixture.Client, chunk)
		require.Equal(t, codes.FailedPrecondition, status.Code(err))
		require.Equal(t, before, sourceEntries(t, fixture.Table, "kernel"))
		require.Equal(t, []neigh.SourceInfo{{Name: "kernel", DefaultPriority: 10, EntryCount: 1, BuiltIn: true}}, fixture.Table.ListSources())
		require.Zero(t, fixture.Changes.Load())
	}
}

// Test_NeighbourService_ConcurrencyAndCompletionOrder verifies that the default
// four slots apply across tables, and a slow older stream can commit last.
func Test_NeighbourService_ConcurrencyAndCompletionOrder(t *testing.T) {
	fixture := newNeighbourServiceFixture(t, operator.WithNeighbourReplacementLimits(operator.NeighbourReplacementLimits{
		MaxEntries: -1, MaxBytes: -1, MaxConcurrentStreams: -1,
	}))
	streams := make([]grpc.ClientStreamingClient[operatorpb.ReplaceNeighboursRequest, operatorpb.ReplaceNeighboursResponse], 4)
	for idx := range streams {
		stream, identity := fixture.Open(t, t.Context())
		table := "snapshot"
		if idx >= 2 {
			table = fmt.Sprintf("other-%d", idx)
		}
		require.NoError(t, stream.Send(replacementChunk(table, uint32(100+idx), fmt.Sprintf("192.0.2.%d", idx+1))))
		fixture.WaitStaged(t, identity, 1)
		streams[idx] = stream
	}
	require.Empty(t, fixture.Table.ListSources())
	err := sendNeighbourSnapshot(t.Context(), fixture.Client, replacementChunk("fifth", 200))
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	for _, idx := range []int{1, 2, 3, 0} {
		_, err := streams[idx].CloseAndRecv()
		require.NoError(t, err)
		if idx == 1 || idx == 0 {
			entries := sourceEntries(t, fixture.Table, "snapshot")
			require.Len(t, entries, 1)
			require.Equal(t, uint32(100+idx), entries[neigh.NewKey(netip.MustParseAddr(fmt.Sprintf("192.0.2.%d", idx+1)), "logical0")].Priority)
		}
	}
	require.Equal(t, int64(4), fixture.Changes.Load())
	require.NoError(t, sendNeighbourSnapshot(t.Context(), fixture.Client, replacementChunk("fifth", 200)))
}

// newNeighbourGatewayClient exposes a real receiver behind a registered TCP
// backend and the production gateway, including both proxy message boundaries.
func newNeighbourGatewayClient(t *testing.T) operatorpb.NeighbourServiceClient {
	t.Helper()
	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = backendListener.Close() })
	server := grpc.NewServer()
	operatorpb.RegisterNeighbourServiceServer(server, operator.NewNeighbourService(neigh.NewNeighTable()))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	proxy, err := gateway.NewGateway(gateway.DefaultConfig(), gateway.WithListener(listener))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, proxy.Close()) })
	ctx, cancel := context.WithCancel(t.Context())
	var group errgroup.Group
	group.Go(func() error {
		err := server.Serve(backendListener)
		if errors.Is(err, grpc.ErrServerStopped) {
			return nil
		}
		return err
	})
	group.Go(func() error { return proxy.Run(ctx) })
	t.Cleanup(func() {
		cancel()
		server.Stop()
		require.NoError(t, group.Wait())
	})
	connection, err := grpc.NewClient(listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(512*1024*1024)),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	registration, stopRegistration := context.WithTimeout(t.Context(), 10*time.Second)
	defer stopRegistration()
	_, err = ynpb.NewGatewayClient(connection).Register(
		registration,
		&ynpb.RegisterRequest{Backend: &ynpb.BackendDesc{
			Name:     operatorpb.NeighbourService_ServiceDesc.ServiceName,
			Endpoint: backendListener.Addr().String(),
		}},
		grpc.WaitForReady(true),
	)
	require.NoError(t, err)
	return operatorpb.NewNeighbourServiceClient(connection)
}

// Test_NeighbourService_ListThroughGateway verifies that an admitted million-row
// snapshot remains listable after server metadata expands it beyond 256 MiB.
func Test_NeighbourService_ListThroughGateway(t *testing.T) {
	client := newNeighbourGatewayClient(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	table := strings.Repeat("t", operatorpb.NeighbourTableNameBytes)
	device := strings.Repeat("d", 79)
	stream, err := client.ReplaceNeighbours(ctx)
	require.NoError(t, err)
	address := netip.MustParseAddr("2001:db8::1")
	totalBytes := 0
	for range operatorpb.NeighbourSnapshotEntries / operatorpb.NeighbourChunkEntries {
		chunk := replacementChunk(table, math.MaxUint32)
		for range operatorpb.NeighbourChunkEntries {
			chunk.Entries = append(chunk.Entries, &operatorpb.NeighbourEntry{
				NextHop:      commonpb.NewIPAddressFromAddr(address),
				HardwareAddr: commonpb.NewMACAddressEUI48([6]byte{0xfe, 0xff, 0xff, 0xff, 0xff, 1}),
				LinkAddr:     commonpb.NewMACAddressEUI48([6]byte{0xfe, 0xff, 0xff, 0xff, 0xff, 2}),
				Device:       device, State: operatorpb.NeighbourState_NUD_PERMANENT, Ifindex: math.MaxInt32,
			})
			address = address.Next()
		}
		require.LessOrEqual(t, proto.Size(chunk), operatorpb.NeighbourChunkBytes)
		totalBytes += proto.Size(chunk)
		require.NoError(t, stream.Send(chunk))
	}
	require.LessOrEqual(t, totalBytes, operatorpb.NeighbourSnapshotBytes)
	_, err = stream.CloseAndRecv()
	require.NoError(t, err)
	response, err := client.List(ctx, &operatorpb.ListNeighboursRequest{Table: table})
	require.NoError(t, err)
	require.Len(t, response.GetNeighbours(), operatorpb.NeighbourSnapshotEntries)
	require.Greater(t, proto.Size(response), 256*1024*1024)
	seen := make([]bool, operatorpb.NeighbourSnapshotEntries+1)
	for _, entry := range response.GetNeighbours() {
		address, err := entry.GetNextHop().ToAddr()
		if err != nil || !address.Is6() {
			t.Fatalf("listed address is not IPv6: %v", entry.GetNextHop())
		}
		raw := address.As16()
		idx := binary.BigEndian.Uint64(raw[8:])
		if binary.BigEndian.Uint64(raw[:8]) != 0x20010db800000000 ||
			idx == 0 || idx > operatorpb.NeighbourSnapshotEntries || seen[idx] {
			t.Fatalf("unexpected or duplicate listed address %s", address)
		}
		seen[idx] = true
		if entry.GetSource() != table || entry.GetDevice() != device ||
			entry.GetPriority() != math.MaxUint32 || entry.GetIfindex() != math.MaxInt32 ||
			entry.GetUpdatedAt() == 0 {
			t.Fatalf("listed metadata changed for %s", address)
		}
	}
}
