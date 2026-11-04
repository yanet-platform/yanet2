package mpls_test

import (
	"context"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/modules/route-mpls/controlplane/routemplspb/v1"
	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/mpls"
	"github.com/yanet-platform/yanet2/operators/bird-adapter/internal/rib"
)

var (
	sourceV4 = netip.MustParseAddr("192.0.2.1")
	sourceV6 = netip.MustParseAddr("2001:db8:ffff::1")
)

// fakeClient records the route-mpls calls a sync makes and answers each
// update with the next queued error, succeeding once the queue is empty.
type fakeClient struct {
	updateErrs []error
	updates    []*routemplspb.UpdateConfigRequest
	creates    []*routemplspb.CreateConfigRequest
}

func (m *fakeClient) UpdateConfig(
	ctx context.Context,
	in *routemplspb.UpdateConfigRequest,
	opts ...grpc.CallOption,
) (*routemplspb.UpdateConfigResponse, error) {
	m.updates = append(m.updates, in)
	if len(m.updateErrs) > 0 {
		err := m.updateErrs[0]
		m.updateErrs = m.updateErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	return &routemplspb.UpdateConfigResponse{}, nil
}

func (m *fakeClient) CreateConfig(
	ctx context.Context,
	in *routemplspb.CreateConfigRequest,
	opts ...grpc.CallOption,
) (*routemplspb.CreateConfigResponse, error) {
	m.creates = append(m.creates, in)
	return &routemplspb.CreateConfigResponse{}, nil
}

// mplsRoute returns an IPv6 MPLS route to the prefix through a nexthop,
// with one label.
func mplsRoute(prefix string, nexthop string, label uint32) rib.Route {
	return rib.Route{
		Prefix:         netip.MustParsePrefix(prefix),
		NextHop:        netip.MustParseAddr(nexthop),
		Peer:           netip.MustParseAddr(nexthop),
		RD:             1,
		MplsLabelStack: []uint32{label},
	}
}

// Test_Sync_Push_UnknownConfigSendsWholeTable verifies that when route-mpls
// does not know the config, as after a control-plane restart, the sync
// sends every route instead of the latest changes alone.
func Test_Sync_Push_UnknownConfigSendsWholeTable(t *testing.T) {
	ctx := t.Context()
	client := &fakeClient{updateErrs: []error{nil, status.Error(codes.NotFound, "config not found")}}
	mplsSync := mpls.NewSync("mpls0", client, sourceV4, sourceV6)

	mplsSync.Apply(mplsRoute("2001:db8:1::/48", "2001:db8::1", 100))
	require.NoError(t, mplsSync.Push(ctx))
	mplsSync.Apply(mplsRoute("2001:db8:2::/48", "2001:db8::2", 200))
	require.NoError(t, mplsSync.Push(ctx))

	require.Len(t, client.updates, 2)
	require.Len(t, client.creates, 1)
	require.Equal(t, "mpls0", client.creates[0].Name)
	require.Len(t, client.creates[0].Rules, 2, "the create carries every route, not only the latest change")
}

// Test_Sync_Push_FailedPushKeepsChanges verifies that the changes a failed
// push could not deliver are sent again by the next push, together with
// newer ones, without replacing the whole table.
func Test_Sync_Push_FailedPushKeepsChanges(t *testing.T) {
	ctx := t.Context()
	client := &fakeClient{updateErrs: []error{status.Error(codes.Unavailable, "control plane restarting")}}
	mplsSync := mpls.NewSync("mpls0", client, sourceV4, sourceV6)

	mplsSync.Apply(mplsRoute("2001:db8:1::/48", "2001:db8::1", 100))
	require.Error(t, mplsSync.Push(ctx))
	mplsSync.Apply(mplsRoute("2001:db8:2::/48", "2001:db8::2", 200))
	require.NoError(t, mplsSync.Push(ctx))

	require.Len(t, client.updates, 2)
	require.Len(t, client.updates[1].Updates, 2, "the retry carries the undelivered change and the newer one")
	require.Empty(t, client.creates)

	require.NoError(t, mplsSync.Push(ctx))
	require.Len(t, client.updates, 2, "nothing is left to push once delivered")
}
