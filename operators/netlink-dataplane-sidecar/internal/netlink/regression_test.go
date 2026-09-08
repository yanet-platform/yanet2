package netlink_test

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	netreconcile "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netlink"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

// Test_Reconciler_BaseBeforeVLAN verifies that a down parent is enabled before
// its lexicographically earlier VLAN, including on repeated applies.
func Test_Reconciler_BaseBeforeVLAN(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
	state := netplan.State{Links: []netplan.Link{
		{Name: "a100", Parent: "kni0", VLANID: 100},
		{Name: "kni0"},
	}}

	for range 2 {
		require.NoError(t, reconciler.Apply(t.Context(), state))
	}
	require.Equal(t, []string{"kni0", "a100", "kni0", "a100"}, backend.up)
	require.NotZero(t, backend.links["a100"].Attrs().Flags&net.FlagUp)
}

// Test_Reconciler_SwapsVLANIdentities verifies that occupied identities are
// released before any replacement is created and retries do not recreate them.
func Test_Reconciler_SwapsVLANIdentities(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addLink(vlan("tenant100", 20, 10, 100, ownedAlias))
	backend.addLink(vlan("tenant200", 21, 10, 200, ownedAlias))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
	state := netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "tenant100", Parent: "kni0", VLANID: 200},
		{Name: "tenant200", Parent: "kni0", VLANID: 100},
	}}

	for range 2 {
		require.NoError(t, reconciler.Apply(t.Context(), state))
	}
	require.Equal(t, []string{"tenant100", "tenant200"}, backend.deleted)
	require.Equal(t, []string{"tenant100", "tenant200"}, backend.added)
	require.Equal(t, 200, backend.links["tenant100"].(*vnetlink.Vlan).VlanId)
	require.Equal(t, 100, backend.links["tenant200"].(*vnetlink.Vlan).VlanId)
}

// Test_Reconciler_PreservesDependentLinks verifies that foreign children block
// deletion and recreation before any configured link changes.
func Test_Reconciler_PreservesDependentLinks(t *testing.T) {
	for _, test := range []struct {
		name  string
		links []netplan.Link
	}{
		{name: "stale VLAN with foreign child"},
		{
			name: "recreated VLAN with foreign child",
			links: []netplan.Link{
				{Name: "kni0", MTU: 9000},
				{Name: "tenant100", Parent: "kni0", VLANID: 200},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeBackend()
			backend.addLink(dummy("kni0", 10, ""))
			backend.addLink(vlan("tenant100", 20, 10, 100, ownedAlias))
			backend.addLink(vlan("foreign", 21, 20, 300, "foreign-owner"))
			reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

			err := reconciler.Apply(t.Context(), netplan.State{Links: test.links})

			require.ErrorContains(t, err, "dependent link")
			require.Contains(t, backend.links, "foreign")
			require.Empty(t, backend.deleted)
			require.Empty(t, backend.added)
			require.Zero(t, backend.links["kni0"].Attrs().MTU)
		})
	}
}

// Test_Reconciler_ForgetsReusedLinkIdentity verifies that address ownership
// does not follow a reused index or a changed hardware address between passes.
func Test_Reconciler_ForgetsReusedLinkIdentity(t *testing.T) {
	for _, keepConfigured := range []bool{false, true} {
		for _, replaceLink := range []bool{false, true} {
			name := "removed base"
			if keepConfigured {
				name = "configured base"
			}
			if replaceLink {
				name += " with reused index"
			} else {
				name += " with changed hardware address"
			}
			t.Run(name, func(t *testing.T) {
				backend := newFakeBackend()
				original := dummy("kni0", 10, "")
				original.HardwareAddr = net.HardwareAddr{2, 0, 0, 0, 0, 1}
				backend.addLink(original)
				reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
				state := netplan.State{Links: []netplan.Link{{
					Name:      "kni0",
					Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")},
				}}}
				require.NoError(t, reconciler.Apply(t.Context(), state))

				if replaceLink {
					backend.addLink(dummy("kni0", 10, "foreign-owner"))
				} else {
					original.HardwareAddr[5] = 2
				}
				state.Links[0].Addresses = nil
				if !keepConfigured {
					state.Links = nil
				}

				require.NoError(t, reconciler.Apply(t.Context(), state))
				require.Equal(t, []string{"192.0.2.1/24"}, addressStrings(backend.addresses["kni0"]))
				require.Empty(t, backend.deletedAddresses)
			})
		}
	}
}

// Test_Reconciler_ChangesIPv6Prefix verifies that an owned address gets its new
// prefix in one successful pass, including after an interrupted recreation.
func Test_Reconciler_ChangesIPv6Prefix(t *testing.T) {
	for _, failReplacement := range []bool{false, true} {
		name := "prefix changed in one pass"
		if failReplacement {
			name = "failed replacement converges on retry"
		}
		t.Run(name, func(t *testing.T) {
			backend := newFakeBackend()
			backend.addLink(dummy("kni0", 10, ""))
			reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
			state := netplan.State{Links: []netplan.Link{{
				Name:      "kni0",
				Addresses: []netip.Prefix{netip.MustParsePrefix("2001:db8::1/64")},
			}}}
			require.NoError(t, reconciler.Apply(t.Context(), state))
			state.Links[0].Addresses[0] = netip.MustParsePrefix("2001:db8::1/128")
			if failReplacement {
				backend.addrReplaceErr["kni0/2001:db8::1/128"] = errors.New("injected failure")
				require.Error(t, reconciler.Apply(t.Context(), state))
				delete(backend.addrReplaceErr, "kni0/2001:db8::1/128")
			}

			for range 2 {
				require.NoError(t, reconciler.Apply(t.Context(), state))
				require.Equal(t, []string{"2001:db8::1/128"}, addressStrings(backend.addresses["kni0"]))
			}
		})
	}
}

// Test_Reconciler_RejectsForeignIPv6PrefixConflict verifies that a conflicting
// unowned prefix is preserved instead of falsely reporting the desired prefix.
func Test_Reconciler_RejectsForeignIPv6PrefixConflict(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addresses["kni0"] = []vnetlink.Addr{mustAddr("2001:db8::1/64")}
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name:      "kni0",
		Addresses: []netip.Prefix{netip.MustParsePrefix("2001:db8::1/128")},
	}}})

	require.ErrorContains(t, err, "unowned IPv6 address")
	require.Empty(t, backend.deletedAddresses)
	require.Empty(t, backend.replacedAddresses)
	require.Equal(t, []string{"2001:db8::1/64"}, addressStrings(backend.addresses["kni0"]))
}

// Test_Reconciler_ReplacesIPv4Primary verifies that desired addresses survive
// withdrawing their primary even when Linux would delete its secondaries.
func Test_Reconciler_ReplacesIPv4Primary(t *testing.T) {
	for _, test := range []struct {
		name    string
		owned   bool
		foreign bool
	}{
		{name: "new address in the same subnet"},
		{name: "retained owned secondary", owned: true},
		{name: "newly desired foreign secondary", foreign: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeBackend()
			backend.addLink(dummy("kni0", 10, ""))
			reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
			state := netplan.State{Links: []netplan.Link{{
				Name:      "kni0",
				Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")},
			}}}
			if test.owned {
				state.Links[0].Addresses = append(state.Links[0].Addresses, netip.MustParsePrefix("192.0.2.2/24"))
			}
			require.NoError(t, reconciler.Apply(t.Context(), state))
			if test.foreign {
				secondary := mustAddr("192.0.2.2/24")
				require.NoError(t, backend.AddrReplace(backend.links["kni0"], &secondary))
				require.NotZero(t, backend.addresses["kni0"][1].Flags&unix.IFA_F_SECONDARY)
			}
			state.Links[0].Addresses = []netip.Prefix{netip.MustParsePrefix("192.0.2.2/24")}

			for range 2 {
				require.NoError(t, reconciler.Apply(t.Context(), state))
				require.Equal(t, []string{"192.0.2.2/24"}, addressStrings(backend.addresses["kni0"]))
			}
		})
	}
}

// Test_Reconciler_PreservesForeignIPv4Secondary verifies that a foreign
// secondary blocks withdrawal of its primary to prevent cascading deletion.
func Test_Reconciler_PreservesForeignIPv4Secondary(t *testing.T) {
	for _, removeBase := range []bool{false, true} {
		name := "primary replaced on configured base"
		if removeBase {
			name = "base removed from desired state"
		}
		t.Run(name, func(t *testing.T) {
			backend := newFakeBackend()
			base := dummy("kni0", 10, "")
			backend.addLink(base)
			reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
			state := netplan.State{Links: []netplan.Link{{
				Name:      "kni0",
				Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")},
			}}}
			require.NoError(t, reconciler.Apply(t.Context(), state))
			foreign := mustAddr("192.0.2.9/24")
			require.NoError(t, backend.AddrReplace(base, &foreign))
			require.NotZero(t, backend.addresses["kni0"][1].Flags&unix.IFA_F_SECONDARY)
			state.Links[0].Addresses = []netip.Prefix{netip.MustParsePrefix("192.0.2.2/24")}
			if removeBase {
				state.Links = nil
			}

			err := reconciler.Apply(t.Context(), state)

			require.ErrorContains(t, err, "unowned secondary")
			require.Empty(t, backend.deletedAddresses)
			require.ElementsMatch(t, []string{"192.0.2.1/24", "192.0.2.9/24"}, addressStrings(backend.addresses["kni0"]))
		})
	}
}

// Test_Reconciler_RetriesVLANSwapAfterCreateFailure verifies that a failed
// creation does not leave another desired VLAN permanently blocking retries.
func Test_Reconciler_RetriesVLANSwapAfterCreateFailure(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addLink(vlan("tenant100", 20, 10, 100, ownedAlias))
	backend.addLink(vlan("tenant200", 21, 10, 200, ownedAlias))
	backend.linkAddErr["tenant100"] = errors.New("injected creation failure")
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
	state := netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "tenant100", Parent: "kni0", VLANID: 200},
		{Name: "tenant200", Parent: "kni0", VLANID: 100},
	}}

	require.ErrorContains(t, reconciler.Apply(t.Context(), state), "injected creation failure")
	delete(backend.linkAddErr, "tenant100")
	require.NoError(t, reconciler.Apply(t.Context(), state))
	require.Equal(t, []string{"tenant100", "tenant200"}, backend.deleted)
	require.Equal(t, []string{"tenant100", "tenant200"}, backend.added)
}

// Test_Reconciler_ValidatesWholeVLANReplacementPlan verifies that a foreign
// identity conflict blocks unrelated planned deletions and MTU changes.
func Test_Reconciler_ValidatesWholeVLANReplacementPlan(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addLink(vlan("owned", 20, 10, 100, ownedAlias))
	backend.addLink(vlan("zforeign", 21, 10, 200, "foreign-owner"))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0", MTU: 9000},
		{Name: "owned", Parent: "kni0", VLANID: 300},
		{Name: "target", Parent: "kni0", VLANID: 200},
	}})

	require.ErrorContains(t, err, "identity is occupied by unowned link")
	require.Empty(t, backend.deleted)
	require.Empty(t, backend.added)
	require.Zero(t, backend.links["kni0"].Attrs().MTU)
}

// Test_Reconciler_RechecksDependentLinks verifies that a newly observed child
// prevents deletion even when the initial dump contained no dependent links.
func Test_Reconciler_RechecksDependentLinks(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(vlan("stale", 20, 10, 100, ownedAlias))
	backend.beforeLinkList = func(call int) {
		if call == 2 {
			backend.addLink(vlan("foreign", 21, 20, 200, "foreign-owner"))
		}
	}
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{})

	require.ErrorContains(t, err, "dependent link")
	require.Empty(t, backend.deleted)
	require.Contains(t, backend.links, "foreign")
}

// Test_Reconciler_PreservesOldSubnetOnAddFailure verifies that independent
// subnet replacement installs the new address before withdrawing the old.
func Test_Reconciler_PreservesOldSubnetOnAddFailure(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
	state := netplan.State{Links: []netplan.Link{{
		Name:      "kni0",
		Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")},
	}}}
	require.NoError(t, reconciler.Apply(t.Context(), state))
	state.Links[0].Addresses[0] = netip.MustParsePrefix("198.51.100.1/24")
	backend.addrReplaceErr["kni0/198.51.100.1/24"] = errors.New("injected address failure")

	require.Error(t, reconciler.Apply(t.Context(), state))
	require.Empty(t, backend.deletedAddresses)
	require.Equal(t, []string{"192.0.2.1/24"}, addressStrings(backend.addresses["kni0"]))
}

// Test_Reconciler_CancelsDuringIPv6AddressRevalidation verifies that canceling
// a fresh address dump prevents the pending replacement from being sent.
func Test_Reconciler_CancelsDuringIPv6AddressRevalidation(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	backend.beforeAddrList = func(name string, call int) {
		if name == "kni0" && call == 3 {
			cancel()
		}
	}
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(ctx, netplan.State{Links: []netplan.Link{{
		Name:      "kni0",
		Addresses: []netip.Prefix{netip.MustParsePrefix("2001:db8::1/64")},
	}}})

	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, backend.replacedAddresses)
	require.Empty(t, backend.deletedAddresses)
}

// Test_Reconciler_RechecksForeignIPv4Secondaries verifies that a fresh address
// dump prevents primary deletion when a foreign secondary appears mid-pass.
func Test_Reconciler_RechecksForeignIPv4Secondaries(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
	state := netplan.State{Links: []netplan.Link{{
		Name:      "kni0",
		Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")},
	}}}
	require.NoError(t, reconciler.Apply(t.Context(), state))
	backend.addrListCalls = map[string]int{}
	backend.beforeAddrList = func(name string, call int) {
		if name == "kni0" && call == 3 {
			foreign := mustAddr("192.0.2.9/24")
			foreign.Flags = unix.IFA_F_SECONDARY
			backend.addresses[name] = append(backend.addresses[name], foreign)
		}
	}
	state.Links[0].Addresses[0] = netip.MustParsePrefix("192.0.2.2/24")

	err := reconciler.Apply(t.Context(), state)

	require.ErrorContains(t, err, "unowned secondary")
	require.Empty(t, backend.deletedAddresses)
	require.ElementsMatch(t, []string{"192.0.2.1/24", "192.0.2.9/24"}, addressStrings(backend.addresses["kni0"]))
}

// Test_Reconciler_PreservesVLANOnParentMTUFailure verifies that a failed parent
// increase leaves the existing VLAN intact rather than deleting it prematurely.
func Test_Reconciler_PreservesVLANOnParentMTUFailure(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addLink(vlan("tenant100", 20, 10, 100, ownedAlias))
	backend.mtuErr["kni0"] = errors.New("injected MTU failure")
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0", MTU: 9000},
		{Name: "tenant100", Parent: "kni0", VLANID: 200},
	}})

	require.ErrorContains(t, err, "injected MTU failure")
	require.Empty(t, backend.deleted)
	require.Equal(t, 100, backend.links["tenant100"].(*vnetlink.Vlan).VlanId)
}
