package neighbour_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	"google.golang.org/grpc"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

const testOwnedTable = "netlink-dataplane-kernel"

type fakeClient struct {
	listTablesResponse  *operatorpb.ListNeighbourTablesResponse
	listTablesError     error
	listResponse        *operatorpb.ListNeighboursResponse
	listError           error
	createResponse      *operatorpb.CreateNeighbourTableResponse
	createError         error
	updateTableResponse *operatorpb.UpdateNeighbourTableResponse
	updateTableError    error
	removeTableResponse *operatorpb.RemoveNeighbourTableResponse
	removeTableError    error
	updateResponse      *operatorpb.UpdateNeighboursResponse
	updateError         error
	removeResponse      *operatorpb.RemoveNeighboursResponse
	removeError         error

	operations          []string
	createRequests      []*operatorpb.CreateNeighbourTableRequest
	updateTableRequests []*operatorpb.UpdateNeighbourTableRequest
	removeTableRequests []*operatorpb.RemoveNeighbourTableRequest
	listRequests        []*operatorpb.ListNeighboursRequest
	updateRequests      []*operatorpb.UpdateNeighboursRequest
	removeRequests      []*operatorpb.RemoveNeighboursRequest
}

// RemoveTable returns the configured result and records obsolete table cleanup.
func (m *fakeClient) RemoveTable(
	ctx context.Context,
	request *operatorpb.RemoveNeighbourTableRequest,
	options ...grpc.CallOption,
) (*operatorpb.RemoveNeighbourTableResponse, error) {
	m.operations = append(m.operations, "remove_table")
	m.removeTableRequests = append(m.removeTableRequests, request)
	if m.removeTableError == nil && m.removeTableResponse != nil {
		remaining := m.listTablesResponse.Tables[:0]
		for _, table := range m.listTablesResponse.Tables {
			if table.GetName() != request.GetName() {
				remaining = append(remaining, table)
			}
		}
		m.listTablesResponse.Tables = remaining
	}
	return m.removeTableResponse, m.removeTableError
}

// ListTables returns configured table metadata and records the RPC attempt.
func (m *fakeClient) ListTables(
	ctx context.Context,
	request *operatorpb.ListNeighbourTablesRequest,
	options ...grpc.CallOption,
) (*operatorpb.ListNeighbourTablesResponse, error) {
	m.operations = append(m.operations, "list_tables")
	return m.listTablesResponse, m.listTablesError
}

// CreateTable returns the configured result and records the requested table.
func (m *fakeClient) CreateTable(
	ctx context.Context,
	request *operatorpb.CreateNeighbourTableRequest,
	options ...grpc.CallOption,
) (*operatorpb.CreateNeighbourTableResponse, error) {
	m.operations = append(m.operations, "create_table")
	m.createRequests = append(m.createRequests, request)
	return m.createResponse, m.createError
}

// UpdateTable returns the configured result and records the metadata update.
func (m *fakeClient) UpdateTable(
	ctx context.Context,
	request *operatorpb.UpdateNeighbourTableRequest,
	options ...grpc.CallOption,
) (*operatorpb.UpdateNeighbourTableResponse, error) {
	m.operations = append(m.operations, "update_table")
	m.updateTableRequests = append(m.updateTableRequests, request)
	return m.updateTableResponse, m.updateTableError
}

// List returns the configured table snapshot and records the table name.
func (m *fakeClient) List(
	ctx context.Context,
	request *operatorpb.ListNeighboursRequest,
	options ...grpc.CallOption,
) (*operatorpb.ListNeighboursResponse, error) {
	m.operations = append(m.operations, "list")
	m.listRequests = append(m.listRequests, request)
	return m.listResponse, m.listError
}

// UpdateNeighbours returns the configured result and records exact upserts.
func (m *fakeClient) UpdateNeighbours(
	ctx context.Context,
	request *operatorpb.UpdateNeighboursRequest,
	options ...grpc.CallOption,
) (*operatorpb.UpdateNeighboursResponse, error) {
	m.operations = append(m.operations, "update_neighbours")
	m.updateRequests = append(m.updateRequests, request)
	return m.updateResponse, m.updateError
}

// RemoveNeighbours returns the configured result and records exact removals.
func (m *fakeClient) RemoveNeighbours(
	ctx context.Context,
	request *operatorpb.RemoveNeighboursRequest,
	options ...grpc.CallOption,
) (*operatorpb.RemoveNeighboursResponse, error) {
	m.operations = append(m.operations, "remove_neighbours")
	m.removeRequests = append(m.removeRequests, request)
	return m.removeResponse, m.removeError
}

// Test_Publish_FiltersEachGatewayByOwnedDevices verifies that equal next hops
// on disjoint devices are independently scoped before collision validation.
func Test_Publish_FiltersEachGatewayByOwnedDevices(t *testing.T) {
	firstClient := newFakeClient("netlink-dataplane-first", 100)
	secondClient := newFakeClient("netlink-dataplane-second", 100)
	firstEntry := testDesiredEntry(
		"192.0.2.1",
		"dataplane-a",
		1,
		11,
		vnetlink.NUD_REACHABLE,
	)
	secondEntry := testDesiredEntry(
		"192.0.2.1",
		"dataplane-b",
		2,
		22,
		vnetlink.NUD_STALE,
	)

	err := neighbour.Publish(t.Context(), []neighbour.Entry{
		secondEntry,
		firstEntry,
	}, []neighbour.GatewayTarget{
		{
			Name:            "first",
			TableName:       "netlink-dataplane-first",
			DefaultPriority: 100,
			Devices:         []string{"dataplane-a"},
			Client:          firstClient,
		},
		{
			Name:            "second",
			TableName:       "netlink-dataplane-second",
			DefaultPriority: 100,
			Devices:         []string{"dataplane-b"},
			Client:          secondClient,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []*operatorpb.UpdateNeighboursRequest{{
		Table:   "netlink-dataplane-first",
		Entries: []*operatorpb.NeighbourEntry{wireEntry(firstEntry)},
	}}, firstClient.updateRequests)
	require.Equal(t, []*operatorpb.UpdateNeighboursRequest{{
		Table:   "netlink-dataplane-second",
		Entries: []*operatorpb.NeighbourEntry{wireEntry(secondEntry)},
	}}, secondClient.updateRequests)
}

// Test_Publish_CreatesAndUpdatesOwnedTables verifies that prefixed absent
// tables are created and existing owned tables are updated before listing.
func Test_Publish_CreatesAndUpdatesOwnedTables(t *testing.T) {
	createClient := newFakeClient("", 0)
	updateClient := newFakeClient("netlink-dataplane-existing", 10)

	err := neighbour.Publish(t.Context(), nil, []neighbour.GatewayTarget{
		{
			Name:            "create",
			TableName:       "netlink-dataplane-new",
			DefaultPriority: 20,
			Devices:         []string{"dataplane-a"},
			Client:          createClient,
		},
		{
			Name:            "update",
			TableName:       "netlink-dataplane-existing",
			DefaultPriority: 30,
			Devices:         []string{"dataplane-b"},
			Client:          updateClient,
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"list_tables", "create_table", "list"}, createClient.operations)
	require.Equal(t, []*operatorpb.CreateNeighbourTableRequest{{
		Name:            "netlink-dataplane-new",
		DefaultPriority: 20,
	}}, createClient.createRequests)
	require.Equal(t, []string{"list_tables", "update_table", "list"}, updateClient.operations)
	require.Equal(t, []*operatorpb.UpdateNeighbourTableRequest{{
		Name:            "netlink-dataplane-existing",
		DefaultPriority: 30,
	}}, updateClient.updateTableRequests)
}

// Test_Publish_ExactUpsertAndRemovalDiff verifies that server-owned metadata
// is ignored while forwarding changes, priority changes, and stale hops diff.
func Test_Publish_ExactUpsertAndRemovalDiff(t *testing.T) {
	client := newFakeClient(testOwnedTable, 50)
	unchanged := testDesiredEntry(
		"192.0.2.1",
		"dataplane-a",
		1,
		11,
		vnetlink.NUD_REACHABLE,
	)
	changed := testDesiredEntry(
		"192.0.2.2",
		"dataplane-a",
		1,
		22,
		vnetlink.NUD_STALE,
	)
	added := testDesiredEntry(
		"192.0.2.3",
		"dataplane-a",
		1,
		33,
		vnetlink.NUD_DELAY,
	)
	reprioritized := testDesiredEntry(
		"192.0.2.5",
		"dataplane-a",
		1,
		55,
		vnetlink.NUD_PROBE,
	)
	oldChanged := testDesiredEntry(
		"192.0.2.2",
		"dataplane-a",
		1,
		99,
		vnetlink.NUD_PERMANENT,
	)
	stale := testDesiredEntry(
		"192.0.2.4",
		"dataplane-a",
		1,
		44,
		vnetlink.NUD_PERMANENT,
	)
	client.listResponse.Neighbours = []*operatorpb.NeighbourEntry{
		wireCurrentEntry(stale, 50),
		wireCurrentEntry(reprioritized, 40),
		wireCurrentEntry(oldChanged, 50),
		wireCurrentEntry(unchanged, 50),
	}

	err := neighbour.Publish(
		t.Context(),
		[]neighbour.Entry{reprioritized, added, unchanged, changed},
		[]neighbour.GatewayTarget{{
			Name:            "gateway",
			TableName:       testOwnedTable,
			DefaultPriority: 50,
			Client:          client,
		}},
	)
	require.NoError(t, err)
	require.Equal(t, []string{
		"list_tables",
		"list",
		"update_neighbours",
		"remove_neighbours",
	}, client.operations)
	require.Equal(t, []*operatorpb.UpdateNeighboursRequest{{
		Table: testOwnedTable,
		Entries: []*operatorpb.NeighbourEntry{
			wireEntry(changed),
			wireEntry(added),
			wireEntry(reprioritized),
		},
	}}, client.updateRequests)
	require.Equal(t, []*operatorpb.RemoveNeighboursRequest{{
		Table: testOwnedTable,
		NextHops: []*commonpb.IPAddress{
			commonpb.NewIPAddressFromAddr(stale.NextHop),
		},
	}}, client.removeRequests)
}

// Test_Publish_EmptySnapshotClearsTable verifies that no desired entries
// produces one deterministic stale-hop removal and no empty update call.
func Test_Publish_EmptySnapshotClearsTable(t *testing.T) {
	client := newFakeClient(testOwnedTable, 100)
	first := testDesiredEntry(
		"192.0.2.1",
		"dataplane-a",
		1,
		11,
		vnetlink.NUD_PERMANENT,
	)
	second := testDesiredEntry(
		"192.0.2.2",
		"dataplane-a",
		1,
		22,
		vnetlink.NUD_PERMANENT,
	)
	client.listResponse.Neighbours = []*operatorpb.NeighbourEntry{
		wireCurrentEntry(second, 100),
		wireCurrentEntry(first, 100),
	}

	err := neighbour.Publish(t.Context(), nil, []neighbour.GatewayTarget{{
		Name:            "gateway",
		TableName:       testOwnedTable,
		DefaultPriority: 100,
		Client:          client,
	}})
	require.NoError(t, err)
	require.Empty(t, client.updateRequests)
	require.Equal(t, []*operatorpb.RemoveNeighboursRequest{{
		Table: testOwnedTable,
		NextHops: []*commonpb.IPAddress{
			commonpb.NewIPAddressFromAddr(first.NextHop),
			commonpb.NewIPAddressFromAddr(second.NextHop),
		},
	}}, client.removeRequests)
}

// Test_Publish_RemovesObsoleteOwnedTablesAfterCurrentTableReconciles verifies that
// stale owned tables are deleted in name order only after current table reads.
func Test_Publish_RemovesObsoleteOwnedTablesAfterCurrentTableReconciles(t *testing.T) {
	client := newFakeClient(testOwnedTable, 100)
	client.listTablesResponse.Tables = append(client.listTablesResponse.Tables,
		&operatorpb.NeighbourTableInfo{Name: "netlink-dataplane-z-old"},
		&operatorpb.NeighbourTableInfo{Name: "static"},
		&operatorpb.NeighbourTableInfo{Name: "netlink-dataplane-a-old"},
	)

	err := neighbour.Publish(t.Context(), nil, []neighbour.GatewayTarget{{
		Name:            "gateway",
		TableName:       testOwnedTable,
		DefaultPriority: 100,
		Client:          client,
	}})

	require.NoError(t, err)
	require.Equal(t, []string{"list_tables", "list", "remove_table", "remove_table"}, client.operations)
	require.Equal(t, []*operatorpb.RemoveNeighbourTableRequest{
		{Name: "netlink-dataplane-a-old"},
		{Name: "netlink-dataplane-z-old"},
	}, client.removeTableRequests)
}

// Test_Publish_PreservesAllConfiguredTablesOnSharedEndpoint verifies that one
// target's cleanup preserves its peer's configured table and foreign tables.
func Test_Publish_PreservesAllConfiguredTablesOnSharedEndpoint(t *testing.T) {
	const (
		firstTable  = "netlink-dataplane-first"
		secondTable = "netlink-dataplane-second"
	)
	tables := []*operatorpb.NeighbourTableInfo{
		{Name: firstTable, DefaultPriority: 100},
		{Name: secondTable, DefaultPriority: 100},
		{Name: "netlink-dataplane-obsolete", DefaultPriority: 100},
		{Name: "static", DefaultPriority: 100},
	}
	client := newFakeClient("", 100)
	client.listTablesResponse.Tables = tables

	err := neighbour.Publish(t.Context(), nil, []neighbour.GatewayTarget{
		{
			Name:            "first",
			TableName:       firstTable,
			DefaultPriority: 100,
			Devices:         []string{"dataplane-a"},
			Client:          client,
		},
		{
			Name:            "second",
			TableName:       secondTable,
			DefaultPriority: 100,
			Devices:         []string{"dataplane-b"},
			Client:          client,
		},
	})

	require.NoError(t, err)
	require.Equal(t, []*operatorpb.RemoveNeighbourTableRequest{{
		Name: "netlink-dataplane-obsolete",
	}}, client.removeTableRequests)
}

// Test_Publish_DoesNotRemoveObsoleteBuiltInTable verifies that an owned-looking
// name cannot authorize deletion of a table protected as built in.
func Test_Publish_DoesNotRemoveObsoleteBuiltInTable(t *testing.T) {
	client := newFakeClient(testOwnedTable, 100)
	client.listTablesResponse.Tables = append(client.listTablesResponse.Tables,
		&operatorpb.NeighbourTableInfo{
			Name:    "netlink-dataplane-built-in",
			BuiltIn: true,
		},
	)

	err := neighbour.Publish(t.Context(), nil, []neighbour.GatewayTarget{{
		Name:            "gateway",
		TableName:       testOwnedTable,
		DefaultPriority: 100,
		Client:          client,
	}})

	require.ErrorContains(t, err, `obsolete neighbour table "netlink-dataplane-built-in" is built in`)
	require.Empty(t, client.removeTableRequests)
}

// Test_Publish_NoRemovalAfterListOrUpdateFailure verifies that stale entries
// remain untouched whenever the prerequisite snapshot or upsert is uncertain.
func Test_Publish_NoRemovalAfterListOrUpdateFailure(t *testing.T) {
	failure := errors.New("injected failure")
	desired := testDesiredEntry(
		"192.0.2.1",
		"dataplane-a",
		1,
		11,
		vnetlink.NUD_REACHABLE,
	)
	stale := testDesiredEntry(
		"192.0.2.2",
		"dataplane-a",
		1,
		22,
		vnetlink.NUD_PERMANENT,
	)
	tests := []struct {
		name            string
		configure       func(*fakeClient)
		wantUpdateCount int
		wantRemoveCount int
	}{
		{
			name: "failed list with partial response",
			configure: func(client *fakeClient) {
				client.listResponse.Neighbours = []*operatorpb.NeighbourEntry{
					wireCurrentEntry(stale, 100),
				}
				client.listError = failure
			},
		},
		{
			name: "incomplete list response",
			configure: func(client *fakeClient) {
				client.listResponse = nil
			},
		},
		{
			name: "failed update",
			configure: func(client *fakeClient) {
				client.listResponse.Neighbours = []*operatorpb.NeighbourEntry{
					wireCurrentEntry(stale, 100),
				}
				client.updateError = failure
			},
			wantUpdateCount: 1,
		},
		{
			name: "incomplete update response",
			configure: func(client *fakeClient) {
				client.listResponse.Neighbours = []*operatorpb.NeighbourEntry{
					wireCurrentEntry(stale, 100),
				}
				client.updateResponse = nil
			},
			wantUpdateCount: 1,
		},
		{
			name: "failed removal",
			configure: func(client *fakeClient) {
				client.listResponse.Neighbours = []*operatorpb.NeighbourEntry{
					wireCurrentEntry(stale, 100),
				}
				client.removeError = failure
			},
			wantUpdateCount: 1,
			wantRemoveCount: 1,
		},
		{
			name: "incomplete removal response",
			configure: func(client *fakeClient) {
				client.listResponse.Neighbours = []*operatorpb.NeighbourEntry{
					wireCurrentEntry(stale, 100),
				}
				client.removeResponse = nil
			},
			wantUpdateCount: 1,
			wantRemoveCount: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newFakeClient(testOwnedTable, 100)
			client.listTablesResponse.Tables = append(
				client.listTablesResponse.Tables,
				&operatorpb.NeighbourTableInfo{Name: "netlink-dataplane-obsolete"},
			)
			test.configure(client)
			err := neighbour.Publish(
				t.Context(),
				[]neighbour.Entry{desired},
				[]neighbour.GatewayTarget{{
					Name:            "gateway",
					TableName:       testOwnedTable,
					DefaultPriority: 100,
					Client:          client,
				}},
			)
			require.Error(t, err)
			require.Len(t, client.updateRequests, test.wantUpdateCount)
			require.Len(t, client.removeRequests, test.wantRemoveCount)
			require.Empty(t, client.removeTableRequests)
		})
	}
}

// Test_Publish_RejectsDuplicateNextHopWithinTarget verifies that ambiguous
// desired forwarding identities cannot mutate a gateway-owned table.
func Test_Publish_RejectsDuplicateNextHopWithinTarget(t *testing.T) {
	client := newFakeClient(testOwnedTable, 100)
	first := testDesiredEntry(
		"192.0.2.1",
		"dataplane-a",
		1,
		11,
		vnetlink.NUD_REACHABLE,
	)
	second := testDesiredEntry(
		"192.0.2.1",
		"dataplane-b",
		2,
		22,
		vnetlink.NUD_STALE,
	)

	err := neighbour.Publish(t.Context(), []neighbour.Entry{
		first,
		second,
	}, []neighbour.GatewayTarget{{
		Name:            "gateway",
		TableName:       testOwnedTable,
		DefaultPriority: 100,
		Devices:         []string{"dataplane-a", "dataplane-b"},
		Client:          client,
	}})
	require.ErrorContains(t, err, "duplicate desired next hop")
	require.Empty(t, client.operations)
}

// Test_Publish_RejectsDeviceWithoutGatewayOwnerBeforeRPC verifies that a desired
// neighbour on an unassigned device prevents calls to every gateway.
func Test_Publish_RejectsDeviceWithoutGatewayOwnerBeforeRPC(t *testing.T) {
	firstClient := newFakeClient("netlink-dataplane-first", 100)
	secondClient := newFakeClient("netlink-dataplane-second", 100)
	entry := testDesiredEntry(
		"192.0.2.1",
		"dataplane-c",
		1,
		11,
		vnetlink.NUD_REACHABLE,
	)

	err := neighbour.Publish(t.Context(), []neighbour.Entry{entry}, []neighbour.GatewayTarget{
		{
			Name:      "first",
			TableName: "netlink-dataplane-first",
			Devices:   []string{"dataplane-a"},
			Client:    firstClient,
		},
		{
			Name:      "second",
			TableName: "netlink-dataplane-second",
			Devices:   []string{"dataplane-b"},
			Client:    secondClient,
		},
	})
	require.ErrorContains(t, err, `device "dataplane-c" has no gateway owner`)
	require.Empty(t, firstClient.operations)
	require.Empty(t, secondClient.operations)
}

// Test_ValidateManagedDeviceOwnership_RejectsUnownedLink verifies that every
// managed link needs a gateway owner even without any discovered neighbours.
func Test_ValidateManagedDeviceOwnership_RejectsUnownedLink(t *testing.T) {
	firstClient := newFakeClient("netlink-dataplane-first", 100)
	secondClient := newFakeClient("netlink-dataplane-second", 100)

	err := neighbour.ValidateManagedDeviceOwnership(
		netplan.State{Links: []netplan.Link{{Name: "kni0"}, {Name: "kni1"}}},
		map[string]string{"kni0": "logical0"},
		[]neighbour.GatewayTarget{
			{
				Name:      "first",
				TableName: "netlink-dataplane-first",
				Devices:   []string{"logical0"},
				Client:    firstClient,
			},
			{
				Name:      "second",
				TableName: "netlink-dataplane-second",
				Devices:   []string{"logical1"},
				Client:    secondClient,
			},
		},
	)

	require.ErrorContains(t, err, `managed link "kni1" logical device "kni1" has no gateway owner`)
	require.Empty(t, firstClient.operations)
	require.Empty(t, secondClient.operations)
}

// Test_Publish_RejectsDuplicateDeviceOwnershipBeforeRPC verifies that an empty
// snapshot cannot bypass exclusive device ownership across gateways.
func Test_Publish_RejectsDuplicateDeviceOwnershipBeforeRPC(t *testing.T) {
	firstClient := newFakeClient("netlink-dataplane-first", 100)
	secondClient := newFakeClient("netlink-dataplane-second", 100)

	err := neighbour.Publish(t.Context(), nil, []neighbour.GatewayTarget{
		{
			Name:      "first",
			TableName: "netlink-dataplane-first",
			Devices:   []string{"dataplane-a"},
			Client:    firstClient,
		},
		{
			Name:      "second",
			TableName: "netlink-dataplane-second",
			Devices:   []string{"dataplane-a"},
			Client:    secondClient,
		},
	})
	require.ErrorContains(t, err, `device "dataplane-a" is already owned by "first"`)
	require.Empty(t, firstClient.operations)
	require.Empty(t, secondClient.operations)
}

// Test_Publish_ContinuesAndJoinsGatewayErrors verifies that all gateways are
// attempted and independently wrapped failures remain discoverable.
func Test_Publish_ContinuesAndJoinsGatewayErrors(t *testing.T) {
	firstError := errors.New("first gateway failed")
	secondError := errors.New("second gateway failed")
	firstClient := newFakeClient("netlink-dataplane-first", 100)
	firstClient.listTablesError = firstError
	secondClient := newFakeClient("netlink-dataplane-second", 100)
	secondClient.listTablesError = secondError
	thirdClient := newFakeClient("netlink-dataplane-third", 100)
	thirdEntry := testDesiredEntry(
		"192.0.2.3",
		"dataplane-c",
		3,
		33,
		vnetlink.NUD_REACHABLE,
	)

	err := neighbour.Publish(
		t.Context(),
		[]neighbour.Entry{thirdEntry},
		[]neighbour.GatewayTarget{
			{
				Name:            "first",
				TableName:       "netlink-dataplane-first",
				DefaultPriority: 100,
				Devices:         []string{"dataplane-a"},
				Client:          firstClient,
			},
			{
				Name:            "second",
				TableName:       "netlink-dataplane-second",
				DefaultPriority: 100,
				Devices:         []string{"dataplane-b"},
				Client:          secondClient,
			},
			{
				Name:            "third",
				TableName:       "netlink-dataplane-third",
				DefaultPriority: 100,
				Devices:         []string{"dataplane-c"},
				Client:          thirdClient,
			},
		},
	)
	require.ErrorIs(t, err, firstError)
	require.ErrorIs(t, err, secondError)
	require.Equal(t, []string{"list_tables"}, firstClient.operations)
	require.Equal(t, []string{"list_tables"}, secondClient.operations)
	require.Equal(t, []string{
		"list_tables",
		"list",
		"update_neighbours",
	}, thirdClient.operations)
}

// Test_Publish_RejectsUnownedTableBeforeRPC verifies that an existing generic
// user table cannot be adopted or cleared by an empty sidecar snapshot.
func Test_Publish_RejectsUnownedTableBeforeRPC(t *testing.T) {
	const userTable = "user-neighbours"
	client := newFakeClient(userTable, 100)
	stale := testDesiredEntry(
		"192.0.2.1",
		"dataplane-a",
		1,
		11,
		vnetlink.NUD_PERMANENT,
	)
	client.listResponse.Neighbours = []*operatorpb.NeighbourEntry{
		wireCurrentEntry(stale, 100),
	}

	err := neighbour.Publish(t.Context(), nil, []neighbour.GatewayTarget{{
		Name:            "gateway",
		TableName:       userTable,
		DefaultPriority: 100,
		Client:          client,
	}})
	require.ErrorContains(t, err, `outside reserved "netlink-dataplane-" namespace`)
	require.Empty(t, client.operations)
	require.Empty(t, client.updateTableRequests)
	require.Empty(t, client.removeRequests)
}

// Test_Publish_RejectsBuiltInTable verifies that publication never claims a
// route-operator-owned source whose lifecycle is reserved by the server.
func Test_Publish_RejectsBuiltInTable(t *testing.T) {
	client := newFakeClient(testOwnedTable, 100)
	client.listTablesResponse.Tables[0].BuiltIn = true

	err := neighbour.Publish(t.Context(), nil, []neighbour.GatewayTarget{{
		Name:            "gateway",
		TableName:       testOwnedTable,
		DefaultPriority: 100,
		Client:          client,
	}})
	require.ErrorContains(t, err, "is built in")
	require.Equal(t, []string{"list_tables"}, client.operations)
}

// newFakeClient returns a successful client with an empty current table.
func newFakeClient(tableName string, defaultPriority uint32) *fakeClient {
	tables := []*operatorpb.NeighbourTableInfo{}
	if tableName != "" {
		tables = append(tables, &operatorpb.NeighbourTableInfo{
			Name:            tableName,
			DefaultPriority: defaultPriority,
		})
	}
	return &fakeClient{
		listTablesResponse:  &operatorpb.ListNeighbourTablesResponse{Tables: tables},
		listResponse:        &operatorpb.ListNeighboursResponse{},
		createResponse:      &operatorpb.CreateNeighbourTableResponse{},
		updateTableResponse: &operatorpb.UpdateNeighbourTableResponse{},
		removeTableResponse: &operatorpb.RemoveNeighbourTableResponse{},
		updateResponse:      &operatorpb.UpdateNeighboursResponse{},
		removeResponse:      &operatorpb.RemoveNeighboursResponse{},
	}
}

// testDesiredEntry returns one complete typed neighbour entry.
func testDesiredEntry(
	nextHop string,
	device string,
	sourceByte byte,
	destinationByte byte,
	state int,
) neighbour.Entry {
	return neighbour.Entry{
		NextHop: netip.MustParseAddr(nextHop),
		HardwareRoute: neighbour.HardwareRoute{
			SourceMAC:      testMACArray(sourceByte),
			DestinationMAC: testMACArray(destinationByte),
			Device:         device,
		},
		State: neighbour.NeighbourState(state),
	}
}

// wireEntry returns the expected route operator update representation.
func wireEntry(entry neighbour.Entry) *operatorpb.NeighbourEntry {
	return &operatorpb.NeighbourEntry{
		NextHop:      commonpb.NewIPAddressFromAddr(entry.NextHop),
		LinkAddr:     commonpb.NewMACAddressEUI48(entry.HardwareRoute.DestinationMAC),
		HardwareAddr: commonpb.NewMACAddressEUI48(entry.HardwareRoute.SourceMAC),
		State:        operatorpb.NeighbourState(entry.State),
		Device:       entry.HardwareRoute.Device,
	}
}

// wireCurrentEntry returns server-normalized metadata around a test entry.
func wireCurrentEntry(entry neighbour.Entry, priority uint32) *operatorpb.NeighbourEntry {
	result := wireEntry(entry)
	result.State = operatorpb.NeighbourState_NUD_PERMANENT
	result.UpdatedAt = 123
	result.Source = "server-owned"
	result.Priority = priority
	return result
}

var _ neighbour.Client = (*fakeClient)(nil)
