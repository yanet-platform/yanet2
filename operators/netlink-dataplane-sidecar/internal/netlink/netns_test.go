package netlink_test

import (
	"net/netip"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	netreconcile "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netlink"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

// Test_Reconciler_Netns verifies that real Linux restoration retains explicit
// IPv6LL and converges after a new VLAN inherits a decreasing parent's MTU.
func Test_Reconciler_Netns(t *testing.T) {
	if os.Getenv("YANET_NETNS_TESTS") != "1" {
		t.Skip("requires a disposable network namespace")
	}
	pair := &vnetlink.Veth{LinkAttrs: vnetlink.LinkAttrs{Name: "kni9", MTU: 9000}, PeerName: "testpeer9"}
	require.NoError(t, vnetlink.LinkAdd(pair))
	t.Cleanup(func() { _ = vnetlink.LinkDel(pair) })
	handle, err := vnetlink.NewHandle()
	require.NoError(t, err)
	t.Cleanup(handle.Close)
	state := netplan.State{Links: []netplan.Link{
		{Name: "kni9", MTU: 1500, Addresses: []netip.Prefix{netip.MustParsePrefix("fe80::f1/64")}},
		{Name: "vlan9", Kind: netplan.LinkKindVLAN, Parent: "kni9", VLANID: 100},
	}}
	reconciler := netreconcile.NewReconciler(handle, netreconcile.NewProcSysctl())
	for range 2 {
		require.NoError(t, reconciler.Apply(t.Context(), state))
	}
	parent, err := handle.LinkByName("kni9")
	require.NoError(t, err)
	child, err := handle.LinkByName("vlan9")
	require.NoError(t, err)
	require.Equal(t, 1500, parent.Attrs().MTU)
	require.Equal(t, 1500, child.Attrs().MTU)
	addresses, err := handle.AddrList(parent, vnetlink.FAMILY_V6)
	require.NoError(t, err)
	require.Len(t, addresses, 1)
	require.Equal(t, "fe80::f1/64", addresses[0].IPNet.String())
	require.NoError(t, handle.AddrDel(parent, &addresses[0]))
	require.NoError(t, reconciler.Apply(t.Context(), state))
	addresses, err = handle.AddrList(parent, vnetlink.FAMILY_V6)
	require.NoError(t, err)
	require.Len(t, addresses, 1)
	require.Equal(t, "fe80::f1/64", addresses[0].IPNet.String())
}
