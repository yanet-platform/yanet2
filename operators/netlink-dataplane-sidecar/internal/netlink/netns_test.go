package netlink_test

import (
	"errors"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/desired"
	netreconcile "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netlink"
)

// Test_Reconciler_NetnsMTU verifies that Linux enforces the intended parent and
// child MTUs without changing unrelated interfaces.
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
			newKernelTAP(t, handle, "kni9", tc.initialParent)
			parent, err := handle.LinkByName("kni9")
			require.NoError(t, err)
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
			state := desired.State{Links: []desired.Link{
				{Name: "aaa9", Kind: desired.LinkKindVLAN, Parent: "kni9", VLANID: 100, MTU: tc.desiredChild},
				{Name: "kni9", MTU: tc.desiredParent},
			}}
			reconciler := netreconcile.NewReconciler(handle, netreconcile.NewProcSysctl())
			for idx := range 2 {
				setup := func() error {
					return errors.Join(reconciler.Create(t.Context(), state), reconciler.Configure(t.Context(), state))
				}
				if tc.wantError {
					require.Error(t, setup(), "pass %d", idx)
				} else {
					require.Eventually(t, func() bool { return setup() == nil }, 5*time.Second, 20*time.Millisecond)
				}
				for name, expected := range map[string]int{"kni9": tc.wantParent, "aaa9": tc.wantChild, "eth0": 2000} {
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

// newKernelTAP keeps carrier present and explicitly sets the initial MTU.
func newKernelTAP(t *testing.T, handle *vnetlink.Handle, name string, mtu int) *vnetlink.Tuntap {
	t.Helper()
	link := &vnetlink.Tuntap{LinkAttrs: vnetlink.LinkAttrs{Name: name}, Mode: vnetlink.TUNTAP_MODE_TAP, Queues: 1}
	require.NoError(t, handle.LinkAdd(link))
	t.Cleanup(func() {
		_ = handle.LinkDel(link)
		for _, descriptor := range link.Fds {
			_ = descriptor.Close()
		}
	})
	require.NoError(t, handle.LinkSetMTU(link, mtu))
	return link
}

// Test_Reconciler_Netns verifies that late KNI permits a later VLAN creation
// and configuration retry while loopback and dummy setup proceeds immediately.
func Test_Reconciler_Netns(t *testing.T) {
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
	t.Cleanup(func() {
		if dummy, err := handle.LinkByName("dummy9"); err == nil {
			_ = handle.LinkDel(dummy)
		}
	})
	state := desired.State{Links: []desired.Link{
		{Name: "kni9", MTU: 1500, Addresses: []netip.Prefix{netip.MustParsePrefix("fe80::f1/64")}},
		{Name: "vlan9", Kind: desired.LinkKindVLAN, Parent: "kni9", VLANID: 100},
		{Name: "lo", Kind: desired.LinkKindLoopback, MTU: 9000},
		{Name: "dummy9", Kind: desired.LinkKindDummy, MTU: 9000},
	}}
	reconciler := netreconcile.NewReconciler(handle, netreconcile.NewProcSysctl())
	require.Error(t, reconciler.Create(t.Context(), state))
	require.Error(t, reconciler.Configure(t.Context(), state))
	for _, name := range []string{"lo", "dummy9"} {
		link, err := handle.LinkByName(name)
		require.NoError(t, err)
		require.Equal(t, 9000, link.Attrs().MTU)
	}
	parent := newKernelTAP(t, handle, "kni9", 9000)
	require.NoError(t, reconciler.Create(t.Context(), state))
	require.Eventually(t, func() bool { return reconciler.Configure(t.Context(), state) == nil }, 5*time.Second, 20*time.Millisecond)
	for name, mtu := range map[string]int{"kni9": 1500, "vlan9": 1500, "lo": 9000, "dummy9": 9000} {
		link, err := handle.LinkByName(name)
		require.NoError(t, err)
		require.Equal(t, mtu, link.Attrs().MTU, name)
	}
	addresses, err := handle.AddrList(parent, vnetlink.FAMILY_V6)
	require.NoError(t, err)
	require.Len(t, addresses, 1)
	require.Equal(t, "fe80::f1/64", addresses[0].IPNet.String())
}
