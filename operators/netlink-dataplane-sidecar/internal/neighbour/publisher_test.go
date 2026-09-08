package neighbour_test

import (
	"context"
	"errors"
	"io"
	"maps"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"

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
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

type publicationCall struct {
	Method  string
	Table   string
	Context context.Context
	Chunk   *operatorpb.ReplaceNeighboursRequest
}

type publicationTable struct {
	Priority uint32
	BuiltIn  bool
	Entries  []*operatorpb.NeighbourEntry
}

// publicationService exposes only replacement and cleanup over real transport.
//
// The route operator has its own internal-package boundary and tests the real
// commit implementation there. This shared fixture records transport and stores
// complete snapshots without emulating the removed neighbour CRUD path.
type publicationService struct {
	operatorpb.UnimplementedNeighbourServiceServer
	mu     sync.Mutex
	tables map[string]publicationTable
	calls  []publicationCall
	hook   func(publicationCall) error
}

// newPublicationService uses default gRPC message limits for every scenario.
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
		grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	return service, operatorpb.NewNeighbourServiceClient(connection)
}

// SetHook injects a failure or cancellation at a recorded transport boundary.
func (m *publicationService) SetHook(hook func(publicationCall) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hook = hook
}

// Store atomically seeds or commits one fixture table.
func (m *publicationService) Store(name string, table publicationTable) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tables[name] = table
}

// Tables returns read-only snapshots; commits replace rather than mutate entries.
func (m *publicationService) Tables() map[string]publicationTable {
	m.mu.Lock()
	defer m.mu.Unlock()
	return maps.Clone(m.tables)
}

// Calls returns read-only requests from completed receives.
func (m *publicationService) Calls() []publicationCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]publicationCall(nil), m.calls...)
}

// record captures requests before calling a potentially blocking hook unlocked.
func (m *publicationService) record(call publicationCall) error {
	hook := m.appendCall(call)
	if hook != nil {
		if err := hook(call); err != nil {
			return err
		}
	}
	return status.FromContextError(call.Context.Err()).Err()
}

// appendCall serializes history and returns the current failure hook.
func (m *publicationService) appendCall(call publicationCall) func(publicationCall) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, call)
	return m.hook
}

// ReplaceNeighbours stores only streams that reach a successful commit boundary.
func (m *publicationService) ReplaceNeighbours(
	stream grpc.ClientStreamingServer[operatorpb.ReplaceNeighboursRequest, operatorpb.ReplaceNeighboursResponse],
) error {
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
		if err := m.record(publicationCall{Method: "chunk", Table: chunk.GetTable(), Context: stream.Context(), Chunk: chunk}); err != nil {
			return err
		}
		entries = append(entries, chunk.GetEntries()...)
	}
	if first == nil {
		return status.Error(codes.InvalidArgument, "missing snapshot")
	}
	if err := m.record(publicationCall{Method: "commit", Table: first.GetTable(), Context: stream.Context()}); err != nil {
		return err
	}
	m.Store(first.GetTable(), publicationTable{Priority: first.GetDefaultPriority(), Entries: entries})
	return stream.SendAndClose(&operatorpb.ReplaceNeighboursResponse{})
}

// ListTables returns only bounded metadata, never neighbouring addresses.
func (m *publicationService) ListTables(ctx context.Context, request *operatorpb.ListNeighbourTablesRequest) (*operatorpb.ListNeighbourTablesResponse, error) {
	if err := m.record(publicationCall{Method: "list_tables", Context: ctx}); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	response := &operatorpb.ListNeighbourTablesResponse{}
	for name, table := range m.tables {
		response.Tables = append(response.Tables, &operatorpb.NeighbourTableInfo{
			Name: name, DefaultPriority: table.Priority, BuiltIn: table.BuiltIn, EntryCount: int64(len(table.Entries)),
		})
	}
	return response, nil
}

// RemoveTable deletes only the requested table after its transport hook succeeds.
func (m *publicationService) RemoveTable(ctx context.Context, request *operatorpb.RemoveNeighbourTableRequest) (*operatorpb.RemoveNeighbourTableResponse, error) {
	if err := m.record(publicationCall{Method: "remove_table", Table: request.GetName(), Context: ctx}); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tables, request.GetName())
	return &operatorpb.RemoveNeighbourTableResponse{}, nil
}

// newPublisherTarget assigns one logical device to a sidecar-owned table.
func newPublisherTarget(name, device string, client neighbour.Client) neighbour.GatewayTarget {
	return neighbour.GatewayTarget{Name: name, TableName: "netlink-dataplane-" + name, DefaultPriority: 100, Devices: []string{device}, Client: client}
}

// testDesiredEntry returns a complete forwarding identity without a local alias.
func testDesiredEntry(nextHop, device string) neighbour.Entry {
	return neighbour.Entry{
		NextHop: netip.MustParseAddr(nextHop),
		HardwareRoute: hwroute.HardwareRoute{
			SourceMAC: [6]byte{2, 0, 0, 0, 0, 1}, DestinationMAC: [6]byte{2, 0, 0, 0, 0, 2}, Device: device,
		},
		State: neighbour.NeighbourState(operatorpb.NeighbourState_NUD_REACHABLE),
	}
}

// wireEntry describes the exact publication payload, excluding server metadata.
func wireEntry(entry neighbour.Entry) *operatorpb.NeighbourEntry {
	return &operatorpb.NeighbourEntry{
		NextHop:      commonpb.NewIPAddressFromAddr(entry.NextHop),
		HardwareAddr: commonpb.NewMACAddressEUI48(entry.HardwareRoute.SourceMAC),
		LinkAddr:     commonpb.NewMACAddressEUI48(entry.HardwareRoute.DestinationMAC),
		Device:       entry.HardwareRoute.Device, State: operatorpb.NeighbourState(entry.State),
	}
}

// callMethods extracts ordered RPC boundaries without comparing gRPC contexts.
func callMethods(calls []publicationCall) []string {
	var methods []string
	for _, call := range calls {
		methods = append(methods, call.Method)
	}
	return methods
}

// Test_Publish_FilteringAndCleanup verifies that shared link-local next hops
// survive per-device filtering and all configured tables are globally protected.
func Test_Publish_FilteringAndCleanup(t *testing.T) {
	service, client := newPublicationService(t)
	first := newPublisherTarget("first", "logical0", client)
	second := newPublisherTarget("second", "logical1", client)
	second.DefaultPriority = 200
	for _, name := range []string{"netlink-dataplane-old-z", "netlink-dataplane-old-a", "static", "foreign"} {
		service.Store(name, publicationTable{})
	}
	firstEntry := testDesiredEntry("fe80::1", "logical0")
	secondEntry := testDesiredEntry("fe80::1", "logical1")
	secondEntry.HardwareRoute.DestinationMAC[5] = 3
	entries := []neighbour.Entry{secondEntry, firstEntry, testDesiredEntry("192.0.2.1", "logical0")}
	require.NoError(t, neighbour.Publish(t.Context(), entries, []neighbour.GatewayTarget{first, second}))
	tables := service.Tables()
	require.Len(t, tables, 4)
	require.Contains(t, tables, "static")
	require.Contains(t, tables, "foreign")
	require.Equal(t, uint32(200), tables[second.TableName].Priority)
	require.True(t, proto.Equal(wireEntry(secondEntry), tables[second.TableName].Entries[0]))
	require.True(t, proto.Equal(wireEntry(entries[2]), tables[first.TableName].Entries[0]))
	require.True(t, proto.Equal(wireEntry(firstEntry), tables[first.TableName].Entries[1]))
	calls := service.Calls()
	require.Equal(t, []string{"chunk", "commit", "chunk", "commit", "list_tables", "remove_table", "remove_table"}, callMethods(calls))
	require.Equal(t, "netlink-dataplane-old-a", calls[5].Table)
	require.Equal(t, "netlink-dataplane-old-z", calls[6].Table)
}

// Test_Publish_InvalidOwnership verifies that malformed desired ownership and
// unbounded fields are rejected before any table can be changed.
func Test_Publish_InvalidOwnership(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func([]neighbour.GatewayTarget, []neighbour.Entry) ([]neighbour.GatewayTarget, []neighbour.Entry)
	}{
		{name: "no targets", configure: func(targets []neighbour.GatewayTarget, entries []neighbour.Entry) ([]neighbour.GatewayTarget, []neighbour.Entry) {
			return nil, entries
		}},
		{name: "foreign namespace", configure: func(targets []neighbour.GatewayTarget, entries []neighbour.Entry) ([]neighbour.GatewayTarget, []neighbour.Entry) {
			targets[0].TableName = "static"
			return targets, entries
		}},
		{name: "overlong table", configure: func(targets []neighbour.GatewayTarget, entries []neighbour.Entry) ([]neighbour.GatewayTarget, []neighbour.Entry) {
			targets[0].TableName += strings.Repeat("x", 128)
			return targets, entries
		}},
		{name: "invalid table characters", configure: func(targets []neighbour.GatewayTarget, entries []neighbour.Entry) ([]neighbour.GatewayTarget, []neighbour.Entry) {
			targets[0].TableName += "/bad"
			return targets, entries
		}},
		{name: "missing client", configure: func(targets []neighbour.GatewayTarget, entries []neighbour.Entry) ([]neighbour.GatewayTarget, []neighbour.Entry) {
			targets[0].Client = nil
			return targets, entries
		}},
		{name: "duplicate table", configure: func(targets []neighbour.GatewayTarget, entries []neighbour.Entry) ([]neighbour.GatewayTarget, []neighbour.Entry) {
			targets[1].TableName = targets[0].TableName
			return targets, entries
		}},
		{name: "duplicate device", configure: func(targets []neighbour.GatewayTarget, entries []neighbour.Entry) ([]neighbour.GatewayTarget, []neighbour.Entry) {
			targets[1].Devices = targets[0].Devices
			return targets, entries
		}},
		{name: "missing devices with multiple targets", configure: func(targets []neighbour.GatewayTarget, entries []neighbour.Entry) ([]neighbour.GatewayTarget, []neighbour.Entry) {
			targets[0].Devices = nil
			return targets, entries
		}},
		{name: "unowned device", configure: func(targets []neighbour.GatewayTarget, entries []neighbour.Entry) ([]neighbour.GatewayTarget, []neighbour.Entry) {
			entries[0].HardwareRoute.Device = "foreign"
			return targets, entries
		}},
		{name: "duplicate next hop", configure: func(targets []neighbour.GatewayTarget, entries []neighbour.Entry) ([]neighbour.GatewayTarget, []neighbour.Entry) {
			return targets[:1], append(entries, entries[0])
		}},
		{name: "zoned next hop", configure: func(targets []neighbour.GatewayTarget, entries []neighbour.Entry) ([]neighbour.GatewayTarget, []neighbour.Entry) {
			entries[0].NextHop = netip.MustParseAddr("fe80::1%logical0")
			return targets[:1], entries
		}},
		{name: "missing next hop", configure: func(targets []neighbour.GatewayTarget, entries []neighbour.Entry) ([]neighbour.GatewayTarget, []neighbour.Entry) {
			entries[0].NextHop = netip.Addr{}
			return targets[:1], entries
		}},
		{name: "unbounded device with wildcard target", configure: func(targets []neighbour.GatewayTarget, entries []neighbour.Entry) ([]neighbour.GatewayTarget, []neighbour.Entry) {
			targets[0].Devices = nil
			entries[0].HardwareRoute.Device = strings.Repeat("x", 129)
			return targets[:1], entries
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, client := newPublicationService(t)
			targets, entries := test.configure([]neighbour.GatewayTarget{
				newPublisherTarget("first", "logical0", client), newPublisherTarget("second", "logical1", client),
			}, []neighbour.Entry{testDesiredEntry("192.0.2.1", "logical0")})
			require.Error(t, neighbour.Publish(t.Context(), entries, targets))
			require.Empty(t, service.Calls())
		})
	}
}

// Test_ValidateManagedDeviceOwnership_UnownedLink verifies that a managed link
// requires an owner even before any neighbours have been discovered.
func Test_ValidateManagedDeviceOwnership_UnownedLink(t *testing.T) {
	service, client := newPublicationService(t)
	err := neighbour.ValidateManagedDeviceOwnership(
		netplan.State{Links: []netplan.Link{{Name: "kni0"}, {Name: "kni1"}}},
		map[string]string{"kni0": "logical0"},
		[]neighbour.GatewayTarget{newPublisherTarget("first", "logical0", client)},
	)
	require.ErrorContains(t, err, `managed link "kni1" logical device "kni1" has no gateway owner`)
	require.Empty(t, service.Calls())
}

// Test_Publish_ObsoleteBuiltIn verifies that reserved-looking names never
// authorize deleting a source protected by the route operator.
func Test_Publish_ObsoleteBuiltIn(t *testing.T) {
	service, client := newPublicationService(t)
	service.Store("netlink-dataplane-builtin", publicationTable{BuiltIn: true})
	service.Store("netlink-dataplane-old", publicationTable{})
	err := neighbour.Publish(t.Context(), nil, []neighbour.GatewayTarget{newPublisherTarget("first", "logical0", client)})
	require.ErrorContains(t, err, "is built in")
	require.Equal(t, []string{"chunk", "commit", "list_tables"}, callMethods(service.Calls()))
}
