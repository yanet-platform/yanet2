package netlink_test

import (
	"errors"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	netreconcile "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netlink"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

// Test_Reconciler_KernelMTUReductionPreservesUnownedIPv6 verifies that lowering a
// base MTU cannot indirectly remove an IPv6 address outside process ownership.
//
// This requires explicit opt-in inside a disposable Docker network namespace
// containing only loopback. The fixture creates and deletes its own base link.
func Test_Reconciler_KernelMTUReductionPreservesUnownedIPv6(t *testing.T) {
	if os.Getenv("YANET_REVIEW_NETNS") != "1" {
		t.Skip("requires YANET_REVIEW_NETNS=1 in Docker --network none --cap-add NET_ADMIN")
	}
	_, err := os.Stat("/.dockerenv")
	require.NoError(t, err, "kernel review tests must run inside a disposable Docker container")
	backend, err := vnetlink.NewHandle(unix.NETLINK_ROUTE)
	require.NoError(t, err)
	t.Cleanup(backend.Close)
	require.NoError(t, backend.SetSocketTimeout(5*time.Second))
	links, err := backend.LinkList()
	require.NoError(t, err)
	require.Len(t, links, 1, "refusing to mutate a namespace containing existing non-loopback links")
	require.Equal(t, "lo", links[0].Attrs().Name)

	created := dummy("kni0", 0, "")
	created.MTU = 1500
	require.NoError(t, backend.LinkAdd(created))
	t.Cleanup(func() {
		require.NoError(t, backend.LinkDel(created))
	})
	base, err := backend.LinkByName("kni0")
	require.NoError(t, err)
	created.Index = base.Attrs().Index
	require.NoError(t, backend.LinkSetUp(base))
	foreign := mustAddr("2001:db8::9/64")
	foreign.Flags = unix.IFA_F_NODAD
	require.NoError(t, backend.AddrAdd(base, &foreign))
	before, err := backend.AddrList(base, vnetlink.FAMILY_V6)
	require.NoError(t, err)
	require.Contains(t, addressStrings(before), "2001:db8::9/64")

	// MTU and address effects use the kernel; policy writes are faked so the
	// container's read-only procfs is not part of this focused regression.
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
	applyError := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name:      "kni0",
		MTU:       1200,
		AcceptRA:  boolPointer(false),
		LinkLocal: []string{},
	}}})
	after, err := backend.AddrList(base, vnetlink.FAMILY_V6)
	require.NoError(t, err)
	current, err := backend.LinkByName("kni0")
	require.NoError(t, err)

	require.Contains(
		t,
		addressStrings(after),
		"2001:db8::9/64",
		"an unsafe MTU reduction must be rejected without removing unowned IPv6 addresses",
	)
	require.ErrorContains(t, applyError, "MTU must be within 1280..")
	require.Equal(t, 1500, current.Attrs().MTU)
}

// Test_Reconciler_PreservesPrimaryWhenSecondaryClaimFails verifies that a failed
// ownership claim cannot authorize primary or cascading secondary deletion.
func Test_Reconciler_PreservesPrimaryWhenSecondaryClaimFails(t *testing.T) {
	backend := newFakeBackend()
	base := dummy("kni0", 10, "")
	backend.addLink(base)
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
	state := netplan.State{Links: []netplan.Link{{
		Name:      "kni0",
		Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")},
	}}}
	require.NoError(t, reconciler.Apply(t.Context(), state))
	secondary := mustAddr("192.0.2.2/24")
	require.NoError(t, backend.AddrReplace(base, &secondary))
	state.Links[0].Addresses[0] = netip.MustParsePrefix("192.0.2.2/24")
	claimError := errors.New("secondary claim failed")
	backend.addrReplaceErr["kni0/192.0.2.2/24"] = claimError

	require.ErrorIs(t, reconciler.Apply(t.Context(), state), claimError)
	require.Empty(t, backend.deletedAddresses)
	require.ElementsMatch(t, []string{"192.0.2.1/24", "192.0.2.2/24"}, addressStrings(backend.addresses["kni0"]))
	delete(backend.addrReplaceErr, "kni0/192.0.2.2/24")
	require.ErrorContains(t, reconciler.Apply(t.Context(), netplan.State{}), "unowned secondary")
	require.Empty(t, backend.deletedAddresses)
	require.NoError(t, reconciler.Apply(t.Context(), state))
	require.Equal(t, []string{"192.0.2.2/24"}, addressStrings(backend.addresses["kni0"]))
}

// Test_Reconciler_PreservesOtherForeignSecondaries verifies that claiming a
// requested secondary does not authorize deletion of other foreign addresses.
func Test_Reconciler_PreservesOtherForeignSecondaries(t *testing.T) {
	backend := newFakeBackend()
	base := dummy("kni0", 10, "")
	backend.addLink(base)
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
	state := netplan.State{Links: []netplan.Link{{
		Name:      "kni0",
		Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")},
	}}}
	require.NoError(t, reconciler.Apply(t.Context(), state))
	for _, prefix := range []string{"192.0.2.2/24", "192.0.2.9/24"} {
		address := mustAddr(prefix)
		require.NoError(t, backend.AddrReplace(base, &address))
	}
	state.Links[0].Addresses[0] = netip.MustParsePrefix("192.0.2.2/24")

	for range 2 {
		require.ErrorContains(t, reconciler.Apply(t.Context(), state), `unowned secondary "192.0.2.9/24"`)
		require.Empty(t, backend.deletedAddresses)
		require.ElementsMatch(
			t,
			[]string{"192.0.2.1/24", "192.0.2.2/24", "192.0.2.9/24"},
			addressStrings(backend.addresses["kni0"]),
		)
	}
}

// Test_Reconciler_RejectsInvalidDesiredAddresses verifies that typed state
// cannot bypass address validation or cause kernel mutations before rejection.
func Test_Reconciler_RejectsInvalidDesiredAddresses(t *testing.T) {
	for _, test := range []struct {
		name      string
		addresses []netip.Prefix
	}{
		{name: "invalid prefix", addresses: []netip.Prefix{{}}},
		{
			name: "conflicting IPv6 prefix lengths",
			addresses: []netip.Prefix{
				netip.MustParsePrefix("2001:db8::1/64"),
				netip.MustParsePrefix("2001:db8::1/128"),
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeBackend()
			sysctl := &fakeSysctl{}
			reconciler := netreconcile.NewReconciler(backend, sysctl)

			err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
				Name:      "kni0",
				Addresses: test.addresses,
			}}})

			require.ErrorContains(t, err, `validate link "kni0"`)
			require.Empty(t, backend.listCalls)
			require.Empty(t, backend.replacedAddresses)
			require.Empty(t, backend.deletedAddresses)
			require.Empty(t, sysctl.writes)
		})
	}
}

// Test_Reconciler_RejectsUndersizedMTU verifies that explicit, preserved, and
// inherited sizes below the IPv6 minimum fail before any mutation.
func Test_Reconciler_RejectsUndersizedMTU(t *testing.T) {
	for _, test := range []struct {
		name          string
		parentMTU     int
		childMTU      int
		recreateChild bool
		desired       []netplan.Link
	}{
		{
			name:      "explicit base MTU",
			parentMTU: 1500,
			desired:   []netplan.Link{{Name: "kni0", MTU: 1200}},
		},
		{
			name:      "preserved base MTU",
			parentMTU: 1200,
			desired:   []netplan.Link{{Name: "kni0"}},
		},
		{
			name:      "inherited child MTU",
			parentMTU: 1200,
			desired: []netplan.Link{
				{Name: "kni0"},
				{Name: "tenant100", Parent: "kni0", VLANID: 100},
			},
		},
		{
			name:      "explicit child MTU",
			parentMTU: 1500,
			desired: []netplan.Link{
				{Name: "kni0"},
				{Name: "tenant100", Parent: "kni0", VLANID: 100, MTU: 1200},
			},
		},
		{
			name:      "preserved child MTU",
			parentMTU: 1500,
			childMTU:  1200,
			desired: []netplan.Link{
				{Name: "kni0"},
				{Name: "tenant100", Parent: "kni0", VLANID: 100},
			},
		},
		{
			name:          "recreated child preserves MTU",
			parentMTU:     1500,
			childMTU:      1200,
			recreateChild: true,
			desired: []netplan.Link{
				{Name: "kni0"},
				{Name: "tenant100", Parent: "kni0", VLANID: 100},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeBackend()
			parent := dummy("kni0", 10, "")
			parent.MTU = test.parentMTU
			backend.addLink(parent)
			if test.childMTU != 0 {
				child := vlan("tenant100", 20, 10, 100, ownedAlias)
				child.MTU = test.childMTU
				if test.recreateChild {
					child.VlanId = 200
				}
				backend.addLink(child)
			}
			backend.addLink(vlan("stale", 30, 10, 300, ownedAlias))
			sysctl := &fakeSysctl{}
			reconciler := netreconcile.NewReconciler(backend, sysctl)

			err := reconciler.Apply(t.Context(), netplan.State{Links: test.desired})

			require.ErrorContains(t, err, "MTU must be within 1280..")
			require.Equal(t, test.parentMTU, parent.MTU)
			require.Empty(t, backend.up)
			require.Empty(t, backend.added)
			require.Empty(t, backend.deleted)
			require.Empty(t, backend.replacedAddresses)
			require.Empty(t, backend.deletedAddresses)
			require.Empty(t, sysctl.writes)
		})
	}
}

// Test_Reconciler_RaisesUndersizedMTU verifies that a valid explicit size can
// recover a small parent and safely supply the omitted size of a new VLAN.
func Test_Reconciler_RaisesUndersizedMTU(t *testing.T) {
	backend := newFakeBackend()
	parent := dummy("kni0", 10, "")
	parent.MTU = 1200
	backend.addLink(parent)
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0", MTU: 1280},
		{Name: "tenant100", Parent: "kni0", VLANID: 100},
	}})

	require.NoError(t, err)
	require.Equal(t, 1280, backend.links["kni0"].Attrs().MTU)
	require.Equal(t, 1280, backend.links["tenant100"].Attrs().MTU)
}
