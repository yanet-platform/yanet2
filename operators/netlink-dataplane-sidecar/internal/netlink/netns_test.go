package netlink_test

import (
	"fmt"
	"net/netip"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	netreconcile "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netlink"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

// Test_Reconciler_NetnsMTU verifies that Linux enforces the intended parent and
// child MTUs without changing unrelated interfaces or the veth peer.
func Test_Reconciler_NetnsMTU(t *testing.T) {
	if os.Getenv("YANET_NETNS_TESTS") != "1" {
		t.Skip("requires a disposable network namespace")
	}
	for _, tc := range []struct {
		name          string
		initialParent int
		initialChild  int
		desiredParent int
		desiredChild  int
		wantParent    int
		wantChild     int
		foreignChild  bool
		wantError     bool
	}{
		{name: "increase existing VLAN", initialParent: 1500, initialChild: 1500, desiredParent: 9000, desiredChild: 9000, wantParent: 9000, wantChild: 9000},
		{name: "decrease existing VLAN", initialParent: 9000, initialChild: 9000, desiredParent: 1500, desiredChild: 1500, wantParent: 1500, wantChild: 1500},
		{name: "new VLAN inherits increased parent", initialParent: 1500, desiredParent: 9000, wantParent: 9000, wantChild: 9000},
		{name: "new VLAN inherits decreased parent", initialParent: 9000, desiredParent: 1500, wantParent: 1500, wantChild: 1500},
		{name: "new VLAN inherits observed parent", initialParent: 9000, wantParent: 9000, wantChild: 9000},
		{name: "unspecified existing MTUs are preserved", initialParent: 9000, initialChild: 1500, wantParent: 9000, wantChild: 1500},
		{name: "explicit child below parent", initialParent: 9000, initialChild: 9000, desiredParent: 9000, desiredChild: 1500, wantParent: 9000, wantChild: 1500},
		{name: "IPv6 minimum", initialParent: 9000, initialChild: 9000, desiredParent: 1280, desiredChild: 1280, wantParent: 1280, wantChild: 1280},
		{name: "IPv6 disabling MTU rejected", initialParent: 1500, initialChild: 1500, desiredParent: 1279, desiredChild: 1279, wantParent: 1500, wantChild: 1500, wantError: true},
		{name: "child above observed parent rejected", initialParent: 1500, initialChild: 1500, desiredChild: 9000, wantParent: 1500, wantChild: 1500, wantError: true},
		{name: "unspecified large child blocks parent decrease", initialParent: 9000, initialChild: 9000, desiredParent: 1500, wantParent: 9000, wantChild: 9000, wantError: true},
		{name: "foreign VLAN blocks parent decrease", initialParent: 9000, initialChild: 1500, desiredParent: 1500, desiredChild: 1500, wantParent: 9000, wantChild: 1500, foreignChild: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handle, err := vnetlink.NewHandle()
			require.NoError(t, err)
			t.Cleanup(handle.Close)
			pair := &vnetlink.Veth{LinkAttrs: vnetlink.LinkAttrs{Name: "kni9", MTU: tc.initialParent}, PeerName: "testpeer9"}
			require.NoError(t, handle.LinkAdd(pair))
			t.Cleanup(func() { _ = handle.LinkDel(pair) })
			parent, err := handle.LinkByName("kni9")
			require.NoError(t, err)
			peer, err := handle.LinkByName("testpeer9")
			require.NoError(t, err)
			peerMTU := peer.Attrs().MTU
			unmanaged := &vnetlink.Dummy{LinkAttrs: vnetlink.LinkAttrs{Name: "eth0", MTU: 2000}}
			require.NoError(t, handle.LinkAdd(unmanaged))
			t.Cleanup(func() { _ = handle.LinkDel(unmanaged) })
			if tc.initialChild != 0 {
				require.NoError(t, handle.LinkAdd(&vnetlink.Vlan{
					LinkAttrs: vnetlink.LinkAttrs{Name: "aaa9", ParentIndex: parent.Attrs().Index, MTU: tc.initialChild},
					VlanId:    100, VlanProtocol: vnetlink.VLAN_PROTOCOL_8021Q,
				}))
			}
			if tc.foreignChild {
				require.NoError(t, handle.LinkAdd(&vnetlink.Vlan{
					LinkAttrs: vnetlink.LinkAttrs{Name: "foreign9", ParentIndex: parent.Attrs().Index, MTU: 9000},
					VlanId:    101, VlanProtocol: vnetlink.VLAN_PROTOCOL_8021Q,
				}))
			}
			state := netplan.State{Links: []netplan.Link{
				{Name: "aaa9", Kind: netplan.LinkKindVLAN, Parent: "kni9", VLANID: 100, MTU: tc.desiredChild},
				{Name: "kni9", MTU: tc.desiredParent},
			}}
			reconciler := netreconcile.NewReconciler(handle, netreconcile.NewProcSysctl())
			for idx := range 2 {
				err := reconciler.Apply(t.Context(), state)
				if tc.wantError {
					require.Error(t, err, "pass %d", idx)
				} else {
					require.NoError(t, err, "pass %d", idx)
				}
				for name, expected := range map[string]int{"kni9": tc.wantParent, "aaa9": tc.wantChild, "testpeer9": peerMTU, "eth0": 2000} {
					link, err := handle.LinkByName(name)
					require.NoError(t, err)
					require.Equal(t, expected, link.Attrs().MTU, "%s pass %d", name, idx)
				}
				if tc.foreignChild {
					foreign, err := handle.LinkByName("foreign9")
					require.NoError(t, err)
					require.Equal(t, 9000, foreign.Attrs().MTU)
				}
			}
		})
	}
}

// Test_Reconciler_NetnsMTURestoration verifies that recreated KNI and VLAN links
// recover explicit jumbo MTUs while loopback and dummy drift is repaired.
func Test_Reconciler_NetnsMTURestoration(t *testing.T) {
	if os.Getenv("YANET_NETNS_TESTS") != "1" {
		t.Skip("requires a disposable network namespace")
	}
	handle, err := vnetlink.NewHandle()
	require.NoError(t, err)
	t.Cleanup(handle.Close)
	loopback, err := handle.LinkByName("lo")
	require.NoError(t, err)
	originalLoopbackMTU := loopback.Attrs().MTU
	t.Cleanup(func() { _ = handle.LinkSetMTU(loopback, originalLoopbackMTU) })
	state := netplan.State{Links: []netplan.Link{
		{Name: "kni9", MTU: 9000},
		{Name: "aaa9", Kind: netplan.LinkKindVLAN, Parent: "kni9", VLANID: 100, MTU: 9000},
		{Name: "lo", Kind: netplan.LinkKindLoopback, MTU: 9000},
		{Name: "dummy9", Kind: netplan.LinkKindDummy, MTU: 9000},
	}}
	reconciler := netreconcile.NewReconciler(handle, netreconcile.NewProcSysctl())
	t.Cleanup(func() {
		if dummy, err := handle.LinkByName("dummy9"); err == nil {
			_ = handle.LinkDel(dummy)
		}
	})
	for idx := range 2 {
		pair := &vnetlink.Veth{LinkAttrs: vnetlink.LinkAttrs{Name: "kni9", MTU: 1500}, PeerName: fmt.Sprintf("peer9%d", idx)}
		require.NoError(t, handle.LinkAdd(pair))
		t.Cleanup(func() { _ = handle.LinkDel(pair) })
		require.NoError(t, reconciler.Apply(t.Context(), state))
		for _, wanted := range state.Links {
			link, err := handle.LinkByName(wanted.Name)
			require.NoError(t, err)
			require.Equal(t, 9000, link.Attrs().MTU, wanted.Name)
			if wanted.Kind != netplan.LinkKindKNI {
				require.NoError(t, handle.LinkSetMTU(link, 1500))
			}
		}
		require.NoError(t, reconciler.Apply(t.Context(), state))
		for _, wanted := range state.Links {
			link, err := handle.LinkByName(wanted.Name)
			require.NoError(t, err)
			require.Equal(t, 9000, link.Attrs().MTU, wanted.Name)
		}
		require.NoError(t, handle.LinkDel(pair))
	}
}

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
