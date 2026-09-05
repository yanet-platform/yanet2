package neighbour_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// Test_Publish_BlockedTargetDoesNotStarveHealthyTarget verifies that a stuck
// gateway exhausts its own deadline while the next gateway still converges.
func Test_Publish_BlockedTargetDoesNotStarveHealthyTarget(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	parentDeadline, _ := ctx.Deadline()
	client := newNeighbourTableClient()
	first := newPublisherTarget("first", "dataplane-a", client)
	second := newPublisherTarget("second", "dataplane-b", client)
	entry := testDesiredEntry("192.0.2.2", "dataplane-b", 2, 22, vnetlink.NUD_REACHABLE)
	client.BeforeCall = func(ctx context.Context, method, table string) error {
		deadline, present := ctx.Deadline()
		require.True(t, present)
		require.True(t, deadline.Before(parentDeadline), "gateway needs its own deadline")
		if len(client.Calls) == 1 {
			<-ctx.Done()
			return ctx.Err()
		}
		require.NoError(t, ctx.Err(), "later targets need a fresh attempt context")
		return nil
	}

	err := neighbour.Publish(ctx, []neighbour.Entry{entry}, []neighbour.GatewayTarget{first, second})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, ctx.Err())
	require.NotContains(t, client.Tables, first.TableName)
	require.Contains(t, client.Tables, second.TableName)
	require.Contains(t, client.Tables[second.TableName].Entries, entry.NextHop)
	require.ErrorIs(t, client.Calls[0].Context.Err(), context.DeadlineExceeded)
	for _, call := range client.Calls[1:] {
		require.ErrorIs(t, call.Context.Err(), context.Canceled)
	}
}

// Test_Publish_SharedReplacementFailurePreservesLastGoodTables verifies that
// obsolete neighbours survive a failed replacement and retire after recovery.
func Test_Publish_SharedReplacementFailurePreservesLastGoodTables(t *testing.T) {
	for _, method := range []string{"create_table", "update_neighbours"} {
		t.Run("second replacement fails during "+method, func(t *testing.T) {
			client := newNeighbourTableClient()
			first := newPublisherTarget("first", "dataplane-a", client)
			second := newPublisherTarget("second", "dataplane-b", client)
			firstEntry := testDesiredEntry("192.0.2.1", "dataplane-a", 1, 11, vnetlink.NUD_REACHABLE)
			secondEntry := testDesiredEntry("192.0.2.2", "dataplane-b", 2, 22, vnetlink.NUD_REACHABLE)
			const firstOldTable = "netlink-dataplane-old-first"
			const secondOldTable = "netlink-dataplane-old-second"
			client.Tables[firstOldTable] = newStoredNeighbourTable(firstEntry)
			client.Tables[secondOldTable] = newStoredNeighbourTable(secondEntry)
			failure := errors.New("replacement failed")
			client.BeforeCall = func(ctx context.Context, operation, table string) error {
				if operation == method && table == second.TableName {
					return failure
				}
				return nil
			}
			entries := []neighbour.Entry{firstEntry, secondEntry}
			targets := []neighbour.GatewayTarget{first, second}

			err := neighbour.Publish(t.Context(), entries, targets)

			require.ErrorIs(t, err, failure)
			require.Contains(t, client.Tables, first.TableName)
			require.Contains(t, client.Tables[first.TableName].Entries, firstEntry.NextHop)
			require.Contains(t, client.Tables, firstOldTable)
			require.Contains(t, client.Tables, secondOldTable)
			require.Equal(
				t, wireCurrentEntry(firstEntry, 100),
				client.Tables[firstOldTable].Entries[firstEntry.NextHop],
			)
			require.Equal(
				t, wireCurrentEntry(secondEntry, 100),
				client.Tables[secondOldTable].Entries[secondEntry.NextHop],
			)
			for _, call := range client.Calls {
				require.NotEqual(t, "remove_table", call.Method)
			}

			client.BeforeCall = nil
			client.Calls = nil
			require.NoError(t, neighbour.Publish(t.Context(), entries, targets))

			require.NotContains(t, client.Tables, firstOldTable)
			require.NotContains(t, client.Tables, secondOldTable)
			require.Contains(t, client.Tables, first.TableName)
			require.Contains(t, client.Tables, second.TableName)
			require.Contains(t, client.Tables[first.TableName].Entries, firstEntry.NextHop)
			require.Contains(t, client.Tables[second.TableName].Entries, secondEntry.NextHop)
			var removedTables []string
			for _, call := range client.Calls {
				if call.Method == "remove_table" {
					removedTables = append(removedTables, call.Table)
				}
			}
			require.Equal(t, []string{firstOldTable, secondOldTable}, removedTables)
		})
	}
}

// Test_Publish_CancellationBoundaries verifies that cancellation prevents
// subsequent RPCs, including later targets and obsolete-table cleanup.
func Test_Publish_CancellationBoundaries(t *testing.T) {
	const firstTable = "netlink-dataplane-first"
	const secondTable = "netlink-dataplane-second"
	const firstOldTable = "netlink-dataplane-old-first"
	const secondOldTable = "netlink-dataplane-old-second"
	tests := []struct {
		Name          string
		CancelBefore  bool
		CancelMethod  string
		CancelTable   string
		MissingFirst  bool
		WantedMethods []string
	}{
		{Name: "cancelled before publication", CancelBefore: true},
		{
			Name:          "cancelled after table listing",
			CancelMethod:  "list_tables",
			WantedMethods: []string{"list_tables"},
		},
		{
			Name:          "cancelled after table creation",
			CancelMethod:  "create_table",
			CancelTable:   firstTable,
			MissingFirst:  true,
			WantedMethods: []string{"list_tables", "create_table"},
		},
		{
			Name:          "cancelled before neighbour upserts",
			CancelMethod:  "list",
			CancelTable:   firstTable,
			WantedMethods: []string{"list_tables", "list"},
		},
		{
			Name:          "cancelled before stale neighbour removal",
			CancelMethod:  "update_neighbours",
			CancelTable:   firstTable,
			WantedMethods: []string{"list_tables", "list", "update_neighbours"},
		},
		{
			Name:          "cancelled before the next gateway",
			CancelMethod:  "remove_neighbours",
			CancelTable:   firstTable,
			WantedMethods: []string{"list_tables", "list", "update_neighbours", "remove_neighbours"},
		},
		{
			Name:         "cancelled after the last gateway before table cleanup",
			CancelMethod: "list",
			CancelTable:  secondTable,
			WantedMethods: []string{
				"list_tables", "list", "update_neighbours", "remove_neighbours",
				"list_tables", "list",
			},
		},
		{
			Name:         "cancelled between obsolete table removals",
			CancelMethod: "remove_table",
			CancelTable:  firstOldTable,
			WantedMethods: []string{
				"list_tables", "list", "update_neighbours", "remove_neighbours",
				"list_tables", "list", "remove_table",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.Name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			client := newNeighbourTableClient()
			first := newPublisherTarget("first", "dataplane-a", client)
			second := newPublisherTarget("second", "dataplane-b", client)
			desired := testDesiredEntry("192.0.2.1", "dataplane-a", 1, 11, vnetlink.NUD_REACHABLE)
			stale := testDesiredEntry("192.0.2.2", "dataplane-a", 1, 22, vnetlink.NUD_REACHABLE)
			if !test.MissingFirst {
				client.Tables[firstTable] = newStoredNeighbourTable(stale)
			}
			client.Tables[secondTable] = newStoredNeighbourTable()
			client.Tables[firstOldTable] = newStoredNeighbourTable(stale)
			client.Tables[secondOldTable] = newStoredNeighbourTable(stale)
			client.BeforeCall = func(ctx context.Context, method, table string) error {
				if method == test.CancelMethod && table == test.CancelTable {
					cancel()
				}
				return nil
			}
			if test.CancelBefore {
				cancel()
			}

			err := neighbour.Publish(ctx, []neighbour.Entry{desired}, []neighbour.GatewayTarget{first, second})

			require.ErrorIs(t, err, context.Canceled)
			var methods []string
			for _, call := range client.Calls {
				methods = append(methods, call.Method)
			}
			require.Equal(t, test.WantedMethods, methods)
			require.Contains(t, client.Tables, secondOldTable)
		})
	}
}

// Test_Publish_ReleasesAttemptContexts verifies that successful publication
// releases its timers and uses a fresh bounded context for shared-table cleanup.
func Test_Publish_ReleasesAttemptContexts(t *testing.T) {
	client := newNeighbourTableClient()
	target := newPublisherTarget("first", "dataplane-a", client)
	client.Tables["netlink-dataplane-old"] = newStoredNeighbourTable()
	var publicationContext context.Context
	client.BeforeCall = func(ctx context.Context, method, table string) error {
		_, present := ctx.Deadline()
		require.True(t, present)
		require.NoError(t, ctx.Err())
		if method == "list_tables" {
			publicationContext = ctx
		}
		if method == "remove_table" {
			require.ErrorIs(t, publicationContext.Err(), context.Canceled)
		}
		return nil
	}

	require.NoError(t, neighbour.Publish(t.Context(), nil, []neighbour.GatewayTarget{target}))

	require.NoError(t, t.Context().Err())
	for _, call := range client.Calls {
		require.ErrorIs(t, call.Context.Err(), context.Canceled)
	}
}

type neighbourTableCall struct {
	Method  string
	Table   string
	Context context.Context
}

type storedNeighbourTable struct {
	Priority uint32
	Entries  map[netip.Addr]*operatorpb.NeighbourEntry
}

type neighbourTableClient struct {
	Tables     map[string]*storedNeighbourTable
	Calls      []neighbourTableCall
	BeforeCall func(context.Context, string, string) error
}

// newNeighbourTableClient returns a serial, shared table store with an RPC hook.
func newNeighbourTableClient() *neighbourTableClient {
	return &neighbourTableClient{Tables: map[string]*storedNeighbourTable{}}
}

// newStoredNeighbourTable returns a populated table with server-normalized data.
func newStoredNeighbourTable(entries ...neighbour.Entry) *storedNeighbourTable {
	table := &storedNeighbourTable{
		Priority: 100,
		Entries:  map[netip.Addr]*operatorpb.NeighbourEntry{},
	}
	for _, entry := range entries {
		table.Entries[entry.NextHop] = wireCurrentEntry(entry, table.Priority)
	}
	return table
}

// newPublisherTarget assigns one logical device to a sidecar-owned table.
func newPublisherTarget(name, device string, client neighbour.Client) neighbour.GatewayTarget {
	return neighbour.GatewayTarget{
		Name:            name,
		TableName:       "netlink-dataplane-" + name,
		DefaultPriority: 100,
		Devices:         []string{device},
		Client:          client,
	}
}

// recordCall invokes the hook before completing the recorded RPC.
func (m *neighbourTableClient) recordCall(ctx context.Context, method, table string) error {
	m.Calls = append(m.Calls, neighbourTableCall{Method: method, Table: table, Context: ctx})
	if m.BeforeCall != nil {
		return m.BeforeCall(ctx, method, table)
	}
	return nil
}

// ListTables returns independent metadata for the shared table store.
func (m *neighbourTableClient) ListTables(
	ctx context.Context,
	request *operatorpb.ListNeighbourTablesRequest,
	options ...grpc.CallOption,
) (*operatorpb.ListNeighbourTablesResponse, error) {
	if err := m.recordCall(ctx, "list_tables", ""); err != nil {
		return nil, err
	}
	response := &operatorpb.ListNeighbourTablesResponse{}
	for name, table := range m.Tables {
		response.Tables = append(response.Tables, &operatorpb.NeighbourTableInfo{
			Name:            name,
			DefaultPriority: table.Priority,
			EntryCount:      int64(len(table.Entries)),
		})
	}
	return response, nil
}

// CreateTable refuses duplicate names and creates an empty table.
func (m *neighbourTableClient) CreateTable(
	ctx context.Context,
	request *operatorpb.CreateNeighbourTableRequest,
	options ...grpc.CallOption,
) (*operatorpb.CreateNeighbourTableResponse, error) {
	name := request.GetName()
	if err := m.recordCall(ctx, "create_table", name); err != nil {
		return nil, err
	}
	if _, found := m.Tables[name]; found {
		return nil, fmt.Errorf("table %q already exists", name)
	}
	m.Tables[name] = &storedNeighbourTable{
		Priority: request.GetDefaultPriority(),
		Entries:  map[netip.Addr]*operatorpb.NeighbourEntry{},
	}
	return &operatorpb.CreateNeighbourTableResponse{}, nil
}

// UpdateTable changes only the priority assigned to future upserts.
func (m *neighbourTableClient) UpdateTable(
	ctx context.Context,
	request *operatorpb.UpdateNeighbourTableRequest,
	options ...grpc.CallOption,
) (*operatorpb.UpdateNeighbourTableResponse, error) {
	if err := m.recordCall(ctx, "update_table", request.GetName()); err != nil {
		return nil, err
	}
	m.Tables[request.GetName()].Priority = request.GetDefaultPriority()
	return &operatorpb.UpdateNeighbourTableResponse{}, nil
}

// List returns copies of the requested table's neighbours.
func (m *neighbourTableClient) List(
	ctx context.Context,
	request *operatorpb.ListNeighboursRequest,
	options ...grpc.CallOption,
) (*operatorpb.ListNeighboursResponse, error) {
	if err := m.recordCall(ctx, "list", request.GetTable()); err != nil {
		return nil, err
	}
	response := &operatorpb.ListNeighboursResponse{}
	for _, entry := range m.Tables[request.GetTable()].Entries {
		response.Neighbours = append(
			response.Neighbours, proto.Clone(entry).(*operatorpb.NeighbourEntry),
		)
	}
	return response, nil
}

// UpdateNeighbours copies upserts and applies the server's state and priority.
func (m *neighbourTableClient) UpdateNeighbours(
	ctx context.Context,
	request *operatorpb.UpdateNeighboursRequest,
	options ...grpc.CallOption,
) (*operatorpb.UpdateNeighboursResponse, error) {
	if err := m.recordCall(ctx, "update_neighbours", request.GetTable()); err != nil {
		return nil, err
	}
	table := m.Tables[request.GetTable()]
	for _, entry := range request.GetEntries() {
		nextHop, err := entry.GetNextHop().ToAddr()
		if err != nil {
			return nil, err
		}
		copied := proto.Clone(entry).(*operatorpb.NeighbourEntry)
		copied.State = operatorpb.NeighbourState_NUD_PERMANENT
		if copied.Priority == 0 {
			copied.Priority = table.Priority
		}
		table.Entries[nextHop] = copied
	}
	return &operatorpb.UpdateNeighboursResponse{}, nil
}

// RemoveNeighbours removes only the requested next hops from their table.
func (m *neighbourTableClient) RemoveNeighbours(
	ctx context.Context,
	request *operatorpb.RemoveNeighboursRequest,
	options ...grpc.CallOption,
) (*operatorpb.RemoveNeighboursResponse, error) {
	if err := m.recordCall(ctx, "remove_neighbours", request.GetTable()); err != nil {
		return nil, err
	}
	for _, address := range request.GetNextHops() {
		nextHop, err := address.ToAddr()
		if err != nil {
			return nil, err
		}
		delete(m.Tables[request.GetTable()].Entries, nextHop)
	}
	return &operatorpb.RemoveNeighboursResponse{}, nil
}

// RemoveTable deletes both a table and every neighbour it held.
func (m *neighbourTableClient) RemoveTable(
	ctx context.Context,
	request *operatorpb.RemoveNeighbourTableRequest,
	options ...grpc.CallOption,
) (*operatorpb.RemoveNeighbourTableResponse, error) {
	if err := m.recordCall(ctx, "remove_table", request.GetName()); err != nil {
		return nil, err
	}
	if _, found := m.Tables[request.GetName()]; !found {
		return nil, fmt.Errorf("table %q does not exist", request.GetName())
	}
	delete(m.Tables, request.GetName())
	return &operatorpb.RemoveNeighbourTableResponse{}, nil
}

var _ neighbour.Client = (*neighbourTableClient)(nil)
