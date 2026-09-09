package neighbour_test

import (
	"net"
	"net/netip"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

// Test_Discover_Netns verifies that a real neighbour dump carries the observed
// link index and a complete empty dump follows explicit kernel withdrawal.
func Test_Discover_Netns(t *testing.T) {
	if os.Getenv("YANET_NETNS_TESTS") != "1" {
		t.Skip("requires a disposable network namespace")
	}
	pair := &vnetlink.Veth{LinkAttrs: vnetlink.LinkAttrs{Name: "kni9"}, PeerName: "testpeer9"}
	require.NoError(t, vnetlink.LinkAdd(pair))
	t.Cleanup(func() { _ = vnetlink.LinkDel(pair) })
	handle, err := vnetlink.NewHandle()
	require.NoError(t, err)
	t.Cleanup(handle.Close)
	link, err := handle.LinkByName("kni9")
	require.NoError(t, err)
	wanted := vnetlink.Neigh{LinkIndex: link.Attrs().Index, IP: net.ParseIP("192.0.2.1"), HardwareAddr: net.HardwareAddr{2, 0, 0, 0, 0, 1}, State: vnetlink.NUD_PERMANENT}
	require.NoError(t, handle.NeighSet(&wanted))
	state := netplan.State{Links: []netplan.Link{{Name: "kni9"}}}
	entries, err := neighbour.Discover(handle, state, map[string]string{"kni9": "logical9"})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, netip.MustParseAddr("192.0.2.1"), entries[0].NextHop)
	require.Equal(t, uint32(link.Attrs().Index), entries[0].Ifindex)
	require.Equal(t, "logical9", entries[0].HardwareRoute.Device)
	require.NoError(t, handle.NeighDel(&wanted))
	entries, err = neighbour.Discover(handle, state, map[string]string{"kni9": "logical9"})
	require.NoError(t, err)
	require.Empty(t, entries)
}
