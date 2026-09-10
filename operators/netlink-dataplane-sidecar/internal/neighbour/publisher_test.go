package neighbour_test

import (
	"context"
	"errors"
	"io"
	"maps"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/hwroute"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

type publicationCall struct {
	Method  string
	Context context.Context
	Chunk   *operatorpb.ReplaceNeighboursRequest
}

type publicationTable struct {
	Priority uint32
	Entries  []*operatorpb.NeighbourEntry
}

// publicationService records real streaming transport and commits only at EOF.
type publicationService struct {
	operatorpb.UnimplementedNeighbourServiceServer
	mu     sync.Mutex
	tables map[string]publicationTable
	calls  []publicationCall
	hook   func(publicationCall) error
}

// newPublicationService serves the fixture with default gRPC message limits.
func newPublicationService(t *testing.T) (*publicationService, operatorpb.NeighbourServiceClient) {
	t.Helper()
	service := &publicationService{tables: map[string]publicationTable{}}
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
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
	connection, err := grpc.NewClient("passthrough:///publication",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) { return listener.DialContext(ctx) }),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	return service, operatorpb.NewNeighbourServiceClient(connection)
}

// SetHook injects failures at receive, commit and response boundaries.
func (m *publicationService) SetHook(hook func(publicationCall) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hook = hook
}

// Store replaces one complete fixture snapshot.
func (m *publicationService) Store(name string, table publicationTable) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tables[name] = table
}

// Tables captures immutable committed entries.
func (m *publicationService) Tables() map[string]publicationTable {
	m.mu.Lock()
	defer m.mu.Unlock()
	return maps.Clone(m.tables)
}

// Calls captures completed receive boundaries without sharing the history slice.
func (m *publicationService) Calls() []publicationCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.calls)
}

// record runs a potentially blocking hook outside the history lock.
func (m *publicationService) record(call publicationCall) error {
	hook := m.appendCall(call)
	if hook != nil {
		if err := hook(call); err != nil {
			return err
		}
	}
	return status.FromContextError(call.Context.Err()).Err()
}

// appendCall atomically records a boundary and captures its failure hook.
func (m *publicationService) appendCall(call publicationCall) func(publicationCall) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, call)
	return m.hook
}

func (m *publicationService) ReplaceNeighbours(stream grpc.ClientStreamingServer[operatorpb.ReplaceNeighboursRequest, operatorpb.ReplaceNeighboursResponse]) error {
	var first *operatorpb.ReplaceNeighboursRequest
	var entries []*operatorpb.NeighbourEntry
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if first == nil {
			first = chunk
		}
		if err := m.record(publicationCall{Method: "chunk", Context: stream.Context(), Chunk: chunk}); err != nil {
			return err
		}
		entries = append(entries, chunk.GetEntries()...)
	}
	if first == nil {
		return status.Error(codes.InvalidArgument, "missing snapshot")
	}
	if err := m.record(publicationCall{Method: "commit", Context: stream.Context()}); err != nil {
		return err
	}
	m.Store(first.GetTable(), publicationTable{Priority: first.GetDefaultPriority(), Entries: entries})
	if err := m.record(publicationCall{Method: "response", Context: stream.Context()}); err != nil {
		return err
	}
	return stream.SendAndClose(&operatorpb.ReplaceNeighboursResponse{})
}

// publicationConfig supplies a stable namespace identity for all transports.
func publicationConfig() neighbour.PublicationConfig {
	return neighbour.PublicationConfig{TableName: "netlink-dataplane-default", DefaultPriority: 100, Timeout: 5 * time.Second}
}

// newPublisherTarget provides an alternate connection without table ownership.
func newPublisherTarget(name string, client neighbour.Client) neighbour.GatewayTarget {
	return neighbour.GatewayTarget{Name: name, Client: client}
}

// testDesiredEntry carries a complete observed identity in the publisher namespace.
func testDesiredEntry(nextHop, device string) neighbour.Entry {
	return neighbour.Entry{
		NextHop: netip.MustParseAddr(nextHop), Ifindex: 10,
		HardwareRoute: hwroute.HardwareRoute{SourceMAC: [6]byte{2, 0, 0, 0, 0, 1}, DestinationMAC: [6]byte{2, 0, 0, 0, 0, 2}, Device: device},
	}
}

// wireEntry excludes receiver-generated metadata from the expected payload.
func wireEntry(entry neighbour.Entry) *operatorpb.NeighbourEntry {
	return &operatorpb.NeighbourEntry{
		NextHop:      commonpb.NewIPAddressFromAddr(entry.NextHop.Unmap()),
		HardwareAddr: commonpb.NewMACAddressEUI48(entry.HardwareRoute.SourceMAC),
		LinkAddr:     commonpb.NewMACAddressEUI48(entry.HardwareRoute.DestinationMAC),
		Device:       entry.HardwareRoute.Device, State: operatorpb.NeighbourState_NUD_PERMANENT, Ifindex: entry.Ifindex,
	}
}

// Test_Publish_SingleTablePairs verifies that equal IPs retain device scope
// while mapped IPv4 and observed interface indices survive wire conversion.
func Test_Publish_SingleTablePairs(t *testing.T) {
	service, client := newPublicationService(t)
	config := publicationConfig()
	first := testDesiredEntry("fe80::1", "logical0")
	second := testDesiredEntry("fe80::1", "logical1")
	second.Ifindex = 20
	second.HardwareRoute.DestinationMAC[5]++
	entries := []neighbour.Entry{second, first, testDesiredEntry("::ffff:192.0.2.1", "logical0")}
	targets := []neighbour.GatewayTarget{newPublisherTarget("first", client)}
	require.NoError(t, neighbour.Publish(t.Context(), entries, targets, config))
	actual := service.Tables()[config.TableName]
	require.Equal(t, config.DefaultPriority, actual.Priority)
	require.Len(t, actual.Entries, 3)
	for idx, entry := range []neighbour.Entry{entries[2], first, second} {
		require.True(t, proto.Equal(wireEntry(entry), actual.Entries[idx]))
	}
}

// Test_Publish_EmptyReplacement verifies that an empty complete snapshot clears
// only its own table and cannot enumerate or remove another producer's source.
func Test_Publish_EmptyReplacement(t *testing.T) {
	service, client := newPublicationService(t)
	config := publicationConfig()
	service.Store(config.TableName, publicationTable{Priority: 7, Entries: []*operatorpb.NeighbourEntry{wireEntry(testDesiredEntry("fe80::1", "logical0"))}})
	service.Store("netlink-dataplane-other", publicationTable{Priority: 7})
	targets := []neighbour.GatewayTarget{newPublisherTarget("first", client)}
	require.NoError(t, neighbour.Publish(t.Context(), nil, targets, config))
	require.Empty(t, service.Tables()[config.TableName].Entries)
	require.Equal(t, uint32(7), service.Tables()["netlink-dataplane-other"].Priority)
	require.Len(t, service.Tables(), 2)
}

// Test_Publish_InvalidSnapshot verifies that invalid pairs are rejected before
// any transport opens, including the two wire representations of IPv4.
func Test_Publish_InvalidSnapshot(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func([]neighbour.Entry) []neighbour.Entry
	}{
		{name: "duplicate pair", mutate: func(entries []neighbour.Entry) []neighbour.Entry { return append(entries, entries[0]) }},
		{name: "mapped duplicate", mutate: func(entries []neighbour.Entry) []neighbour.Entry {
			duplicate := entries[0]
			duplicate.NextHop = netip.MustParseAddr("::ffff:192.0.2.1")
			return append(entries, duplicate)
		}},
		{name: "invalid address", mutate: func(entries []neighbour.Entry) []neighbour.Entry { entries[0].NextHop = netip.Addr{}; return entries }},
		{name: "zoned address", mutate: func(entries []neighbour.Entry) []neighbour.Entry {
			entries[0].NextHop = netip.MustParseAddr("fe80::1%kni0")
			return entries
		}},
		{name: "overlong device", mutate: func(entries []neighbour.Entry) []neighbour.Entry {
			entries[0].HardwareRoute.Device = strings.Repeat("d", 80)
			return entries
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, client := newPublicationService(t)
			entries := test.mutate([]neighbour.Entry{testDesiredEntry("192.0.2.1", "logical0")})
			require.Error(t, neighbour.Publish(t.Context(), entries, []neighbour.GatewayTarget{newPublisherTarget("first", client)}, publicationConfig()))
			require.Empty(t, service.Calls())
		})
	}
}
