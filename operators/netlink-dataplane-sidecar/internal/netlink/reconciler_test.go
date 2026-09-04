package netlink_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	netreconcile "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netlink"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

const ownedAlias = "yanet-netlink-dataplane-sidecar"

func TestReconcilerLateKNI(t *testing.T) {
	backend := newFakeBackend()
	sysctl := &fakeSysctl{}
	reconciler := netreconcile.NewReconciler(backend, sysctl)
	state := netplan.State{Links: []netplan.Link{{Name: "kni0"}}}

	err := reconciler.Apply(t.Context(), state)
	require.ErrorContains(t, err, `find base link "kni0"`)
	require.ErrorContains(t, err, "base KNI link is not available yet")
	require.True(t, netreconcile.IsRetryable(err))
	require.Empty(t, backend.added)
	require.Empty(t, backend.deleted)

	backend.addLink(dummy("kni0", 10, ""))
	require.NoError(t, reconciler.Apply(t.Context(), state))
	require.True(t, backend.links["kni0"].Attrs().Flags&net.FlagUp != 0)
}

func TestZeroValueReconcilerReturnsInitializationError(t *testing.T) {
	var reconciler netreconcile.Reconciler

	err := reconciler.Apply(t.Context(), netplan.State{})

	require.ErrorContains(t, err, "nil apply slot")
	require.False(t, netreconcile.IsRetryable(err))
}

func TestReconcilerCreatesAndMarksVLAN(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
	state := netplan.State{Links: []netplan.Link{
		{Name: "kni0", MTU: 9000},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100, MTU: 8900},
	}}

	require.NoError(t, reconciler.Apply(t.Context(), state))
	require.Equal(t, []string{"tenant.100"}, backend.added)
	created, ok := backend.links["tenant.100"].(*vnetlink.Vlan)
	require.True(t, ok)
	require.Equal(t, 10, created.ParentIndex)
	require.Equal(t, 100, created.VlanId)
	require.Equal(t, ownedAlias, created.Alias)
	require.Equal(t, 8900, created.MTU)
	require.True(t, created.Flags&net.FlagUp != 0)
	require.Equal(t, 9000, backend.links["kni0"].Attrs().MTU)
	require.True(t, backend.links["kni0"].Attrs().Flags&net.FlagUp != 0)
}

func TestReconcilerCreatesZeroMTUVLANAtDesiredParentMTU(t *testing.T) {
	backend := newFakeBackend()
	parent := dummy("kni0", 10, "")
	parent.MTU = 9000
	backend.addLink(parent)
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0", MTU: 1500},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100},
	}})

	require.NoError(t, err)
	require.Equal(t, 1500, backend.links["tenant.100"].Attrs().MTU)
	require.Equal(t, 1500, backend.links["kni0"].Attrs().MTU)
}

func TestReconcilerLowersChildMTUBeforeParent(t *testing.T) {
	backend := newFakeBackend()
	parent := dummy("kni0", 10, "")
	parent.MTU = 9000
	child := vlan("tenant.100", 20, 10, 100, ownedAlias)
	child.MTU = 8900
	backend.addLink(parent)
	backend.addLink(child)
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0", MTU: 1500},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100, MTU: 1400},
	}})

	require.NoError(t, err)
	require.Equal(t, 1500, backend.links["kni0"].Attrs().MTU)
	require.Equal(t, 1400, backend.links["tenant.100"].Attrs().MTU)
}

func TestReconcilerRejectsVLANMTUAboveDesiredParentBeforeMutation(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0", MTU: 1500},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100, MTU: 2000},
	}})

	require.ErrorContains(t, err, "VLAN MTU 2000 exceeds parent")
	require.False(t, netreconcile.IsRetryable(err))
	require.Empty(t, backend.listCalls)
	require.Empty(t, backend.added)
	require.Zero(t, backend.links["kni0"].Attrs().MTU)
}

func TestReconcilerRejectsNegativeMTUBeforeMutation(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name: "kni0",
		MTU:  -1,
	}}})

	require.ErrorContains(t, err, "MTU must be within")
	require.False(t, netreconcile.IsRetryable(err))
	require.Empty(t, backend.listCalls)
}

func TestReconcilerRejectsOversizedMTUBeforeMutation(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("int cannot represent an MTU above MaxInt32")
	}
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
	oversized := uint64(1) << 31

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name: "kni0",
		MTU:  int(oversized),
	}}})

	require.ErrorContains(t, err, "MTU must be within")
	require.False(t, netreconcile.IsRetryable(err))
	require.Empty(t, backend.listCalls)
}

func TestReconcilerRejectsPreservedVLANMTUAboveDesiredParentBeforeMutation(t *testing.T) {
	backend := newFakeBackend()
	parent := dummy("kni0", 10, "")
	parent.MTU = 9000
	child := vlan("tenant.100", 20, 10, 100, ownedAlias)
	child.MTU = 8900
	backend.addLink(parent)
	backend.addLink(child)
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0", MTU: 1500},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100},
	}})

	require.ErrorContains(t, err, "effective VLAN MTU 8900 exceeds parent")
	require.False(t, netreconcile.IsRetryable(err))
	require.Equal(t, 9000, backend.links["kni0"].Attrs().MTU)
	require.Empty(t, backend.up)
}

func TestReconcilerRejectsParentMTUBelowUnmanagedChildBeforeMutation(t *testing.T) {
	backend := newFakeBackend()
	parent := dummy("kni0", 10, "")
	parent.MTU = 9000
	child := vlan("foreign.100", 20, 10, 100, "foreign-owner")
	child.MTU = 8900
	backend.addLink(parent)
	backend.addLink(child)
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name: "kni0",
		MTU:  1500,
	}}})

	require.ErrorContains(t, err, `current MTU 8900 exceeds desired parent "kni0" MTU 1500`)
	require.True(t, netreconcile.IsRetryable(err))
	require.Equal(t, 9000, backend.links["kni0"].Attrs().MTU)
	require.Empty(t, backend.up)
}

func TestReconcilerDoesNotExemptMarkedNonVLANFromParentMTUValidation(t *testing.T) {
	backend := newFakeBackend()
	parent := dummy("kni0", 10, "")
	parent.MTU = 9000
	child := dummy("marked-child", 20, ownedAlias)
	child.ParentIndex = 10
	child.MTU = 8900
	backend.addLink(parent)
	backend.addLink(child)
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name: "kni0",
		MTU:  1500,
	}}})

	require.ErrorContains(t, err, `current MTU 8900 exceeds desired parent "kni0" MTU 1500`)
	require.True(t, netreconcile.IsRetryable(err))
	require.Equal(t, 9000, backend.links["kni0"].Attrs().MTU)
}

func TestReconcilerRechecksChildMTUsImmediatelyBeforeLoweringParent(t *testing.T) {
	backend := newFakeBackend()
	parent := dummy("kni0", 10, "")
	parent.MTU = 9000
	child := vlan("foreign.100", 20, 10, 100, "foreign-owner")
	child.MTU = 1400
	backend.addLink(parent)
	backend.addLink(child)
	backend.beforeLinkList = func(call int) {
		if call == 2 {
			child.MTU = 8900
		}
	}
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name: "kni0",
		MTU:  1500,
	}}})

	require.ErrorContains(t, err, `child link "foreign.100" MTU 8900 exceeds desired parent MTU 1500`)
	require.True(t, netreconcile.IsRetryable(err))
	require.Equal(t, 9000, backend.links["kni0"].Attrs().MTU)
}

func TestReconcilerRejectsVLANWhoseParentIsMissingFromDesiredState(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(vlan("parent.100", 10, 9, 100, ownedAlias))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name:   "tenant.200",
		Parent: "parent.100",
		VLANID: 200,
	}}})

	require.ErrorContains(t, err, `parent "parent.100" is missing from desired state`)
	require.False(t, netreconcile.IsRetryable(err))
	require.Empty(t, backend.listCalls)
	require.Empty(t, backend.added)
	require.Empty(t, backend.deleted)
}

func TestReconcilerRejectsDuplicateDesiredVLANIdentityBeforeMutation(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "first.100", Parent: "kni0", VLANID: 100},
		{Name: "second.100", Parent: "kni0", VLANID: 100},
	}})

	require.ErrorContains(t, err, `VLAN parent "kni0" ID 100 is already used`)
	require.False(t, netreconcile.IsRetryable(err))
	require.Empty(t, backend.listCalls)
	require.Empty(t, backend.added)
}

func TestReconcilerRequiresExplicitOwnershipHandoffForExistingVLAN(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addLink(vlan("tenant.100", 20, 10, 100, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	state := netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100},
	}}
	err := reconciler.Apply(t.Context(), state)

	require.ErrorContains(t, err, "requires explicit ownership handoff")
	require.False(t, netreconcile.IsRetryable(err))
	require.Empty(t, backend.aliasChanges)
	require.Empty(t, backend.deleted)
}

func TestReconcilerRejectsUnownedVLANBeforeConfiguration(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addLink(vlan("tenant.100", 20, 10, 100, ""))
	sysctl := &fakeSysctl{}
	reconciler := netreconcile.NewReconciler(backend, sysctl)

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0", MTU: 9000},
		{
			Name:      "tenant.100",
			Parent:    "kni0",
			VLANID:    100,
			MTU:       8900,
			Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")},
		},
	}})
	require.ErrorContains(t, err, "requires explicit ownership handoff")
	require.Zero(t, backend.links["kni0"].Attrs().MTU)
	require.Zero(t, backend.links["tenant.100"].Attrs().MTU)
	require.Empty(t, backend.links["tenant.100"].Attrs().Alias)
	require.Empty(t, backend.up)
	require.Empty(t, backend.replacedAddresses)
	require.Empty(t, sysctl.writes)
}

func TestReconcilerRejectsMatchingVLANOwnedByAnotherManager(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addLink(vlan("tenant.100", 20, 10, 100, "foreign-owner"))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100},
	}})
	require.ErrorContains(t, err, `link alias "foreign-owner" belongs to another owner`)
	require.Empty(t, backend.aliasChanges)
	require.Empty(t, backend.deleted)
	require.Empty(t, backend.up)
}

func TestReconcilerRejectsMismatchedExistingVLAN(t *testing.T) {
	tests := []struct {
		name string
		link vnetlink.Link
		want string
	}{
		{name: "type", link: dummy("tenant.100", 20, ""), want: "is not vlan"},
		{name: "parent", link: vlan("tenant.100", 20, 99, 100, ""), want: "99/100, want 10/100"},
		{name: "ID", link: vlan("tenant.100", 20, 10, 200, ""), want: "10/200, want 10/100"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := newFakeBackend()
			backend.addLink(dummy("kni0", 10, ""))
			backend.addLink(tt.link)
			sysctl := &fakeSysctl{}
			reconciler := netreconcile.NewReconciler(backend, sysctl)

			err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
				{Name: "kni0"},
				{Name: "tenant.100", Parent: "kni0", VLANID: 100},
			}})
			require.ErrorContains(t, err, tt.want)
			require.False(t, netreconcile.IsRetryable(err))
			require.Empty(t, backend.added)
			require.Empty(t, backend.deleted)
			require.Empty(t, backend.aliasChanges)
			require.Empty(t, backend.up)
			require.Empty(t, backend.replacedAddresses)
			require.Empty(t, backend.deletedAddresses)
			require.Empty(t, sysctl.writes)
		})
	}
}

func TestReconcilerRejectsExistingVLANWithWrongProtocol(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	existing := vlan("tenant.100", 20, 10, 100, "")
	existing.VlanProtocol = vnetlink.VLAN_PROTOCOL_8021AD
	backend.addLink(existing)
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100},
	}})
	require.ErrorContains(t, err, "protocol is 802.1ad, want 802.1q")
	require.False(t, netreconcile.IsRetryable(err))
	require.Empty(t, backend.added)
	require.Empty(t, backend.deleted)
}

func TestReconcilerRejectsVLANMasqueradingAsBaseKNIBeforeMutation(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(vlan("kni0", 10, 9, 100, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name: "kni0",
		MTU:  9000,
	}}})
	require.ErrorContains(t, err, "base KNI link is unexpectedly a VLAN")
	require.False(t, netreconcile.IsRetryable(err))
	require.Zero(t, backend.links["kni0"].Attrs().MTU)
	require.Empty(t, backend.up)
}

func TestReconcilerRecreatesMismatchedOwnedVLAN(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addLink(vlan("tenant.100", 20, 99, 200, ownedAlias))
	backend.addresses["tenant.100"] = []vnetlink.Addr{mustAddr("192.0.2.9/24")}
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{
			Name:      "tenant.100",
			Parent:    "kni0",
			VLANID:    100,
			Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.9/24")},
		},
	}})
	require.NoError(t, err)
	require.Equal(t, []string{"tenant.100"}, backend.deleted)
	require.Equal(t, []string{"tenant.100"}, backend.added)
	recreated, ok := backend.links["tenant.100"].(*vnetlink.Vlan)
	require.True(t, ok)
	require.Equal(t, 10, recreated.ParentIndex)
	require.Equal(t, 100, recreated.VlanId)
	require.Equal(t, ownedAlias, recreated.Alias)
	require.Equal(t, []string{"192.0.2.9/24"}, addressStrings(backend.addresses["tenant.100"]))
}

func TestReconcilerDeletesConflictingStaleVLANBeforeRenamedReplacement(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addLink(vlan("old.100", 20, 10, 100, ownedAlias))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "new.100", Parent: "kni0", VLANID: 100},
	}})

	require.NoError(t, err)
	require.Equal(t, []string{"old.100"}, backend.deleted)
	require.Equal(t, []string{"new.100"}, backend.added)
	require.NotContains(t, backend.links, "old.100")
	require.Contains(t, backend.links, "new.100")
}

func TestReconcilerPreservesZeroMTUVLANValueDuringRecreation(t *testing.T) {
	backend := newFakeBackend()
	parent := dummy("kni0", 10, "")
	parent.MTU = 9000
	current := vlan("tenant.100", 20, 99, 200, ownedAlias)
	current.MTU = 8900
	backend.addLink(parent)
	backend.addLink(current)
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0", MTU: 9000},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100},
	}})

	require.NoError(t, err)
	require.Equal(t, 8900, backend.links["tenant.100"].Attrs().MTU)
}

func TestReconcilerRejectsPreservedRecreatedVLANMTUAboveParent(t *testing.T) {
	backend := newFakeBackend()
	parent := dummy("kni0", 10, "")
	parent.MTU = 9000
	current := vlan("tenant.100", 20, 99, 200, ownedAlias)
	current.MTU = 8900
	backend.addLink(parent)
	backend.addLink(current)
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0", MTU: 1500},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100},
	}})

	require.ErrorContains(t, err, "effective VLAN MTU 8900 exceeds parent")
	require.False(t, netreconcile.IsRetryable(err))
	require.Empty(t, backend.deleted)
	require.Empty(t, backend.added)
}

func TestReconcilerRecreatesExplicitlyOwnedVLANWithResidualAddress(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	original := vlan("tenant.100", 20, 99, 200, ownedAlias)
	backend.addLink(original)
	backend.addresses["tenant.100"] = []vnetlink.Addr{mustAddr("192.0.2.9/24")}
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100},
	}})

	require.NoError(t, err)
	require.NotSame(t, original, backend.links["tenant.100"])
	require.Equal(t, []string{"tenant.100"}, backend.deleted)
	require.Equal(t, []string{"tenant.100"}, backend.added)
	require.Empty(t, backend.addresses["tenant.100"])
}

func TestReconcilerDoesNotRecreateOwnedVLANAfterIncompleteAddressDump(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addLink(dummy("kni1", 11, ""))
	original := vlan("tenant.100", 20, 99, 200, ownedAlias)
	backend.addLink(original)
	backend.addrListErr["kni1"] = errors.New("dump interrupted")
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100},
		{Name: "kni1"},
	}})
	require.ErrorContains(t, err, `list addresses on link "kni1"`)
	require.Same(t, original, backend.links["tenant.100"])
	require.Empty(t, backend.deleted)
	require.Empty(t, backend.added)
	require.Empty(t, backend.aliasChanges)
	require.Empty(t, backend.up)
}

func TestReconcilerDoesNotDeleteReplacedVLANDuringRecreation(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addLink(vlan("tenant.100", 20, 99, 200, ownedAlias))
	backend.beforeLinkByName = func(name string, call int) {
		if name == "tenant.100" && call == 2 {
			backend.addLink(vlan("tenant.100", 21, 10, 100, "foreign-owner"))
		}
	}
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100},
	}})

	require.ErrorContains(t, err, "identity changed during reconciliation")
	require.True(t, netreconcile.IsRetryable(err))
	require.Empty(t, backend.deleted)
	require.Equal(t, 21, backend.links["tenant.100"].Attrs().Index)
}

func TestReconcilerDoesNotMutateVLANAfterParentReplacement(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addLink(vlan("tenant.100", 20, 10, 100, ownedAlias))
	backend.beforeLinkByName = func(name string, call int) {
		if name == "kni0" && call == 2 {
			backend.addLink(dummy("kni0", 11, ""))
		}
	}
	sysctl := &fakeSysctl{}
	reconciler := netreconcile.NewReconciler(backend, sysctl)

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100, MTU: 9000},
	}})

	require.ErrorContains(t, err, `link "kni0" identity changed during reconciliation`)
	require.True(t, netreconcile.IsRetryable(err))
	require.Zero(t, backend.links["tenant.100"].Attrs().MTU)
	require.Empty(t, backend.up)
	require.Empty(t, sysctl.writes)
}

func TestReconcilerPreservesAddressAddedToRecreatedVLAN(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addLink(vlan("tenant.100", 20, 99, 200, ownedAlias))
	backend.beforeAddrList = func(name string, call int) {
		if name == "tenant.100" && call == 2 {
			backend.addresses[name] = []vnetlink.Addr{mustAddr("192.0.2.9/24")}
		}
	}
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100},
	}})

	require.NoError(t, err)
	require.Equal(t, []string{"tenant.100"}, backend.deleted)
	require.Equal(t, []string{"tenant.100"}, backend.added)
	require.Equal(t, []string{"192.0.2.9/24"}, addressStrings(backend.addresses["tenant.100"]))
}

func TestReconcilerValidatesNewlyCreatedVLANBeforeMutation(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.beforeLinkByName = func(name string, call int) {
		if name == "tenant.100" && call == 1 {
			backend.addLink(vlan("tenant.100", 101, 10, 100, "foreign-owner"))
		}
	}
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100},
	}})

	require.ErrorContains(t, err, "validate newly created VLAN")
	require.Empty(t, backend.aliasChanges)
	require.Equal(t, "foreign-owner", backend.links["tenant.100"].Attrs().Alias)
}

func TestReconcilerPreservesForeignAddressesAndDeletesStaleOwnedAddresses(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addLink(dummy("management0", 11, ""))
	slaacAddress := mustAddr("2001:db8::9/64")
	slaacAddress.Flags = unix.IFA_F_TEMPORARY
	backend.addresses["kni0"] = []vnetlink.Addr{
		mustAddr("192.0.2.9/24"),
		slaacAddress,
		mustAddr("fe80::10/64"),
		mustAddr("198.51.100.7/25"),
	}
	backend.addresses["management0"] = []vnetlink.Addr{mustAddr("203.0.113.1/24")}
	sysctl := &fakeSysctl{}
	reconciler := netreconcile.NewReconciler(backend, sysctl)

	first := netplan.State{Links: []netplan.Link{{
		Name:      "kni0",
		AcceptRA:  boolPointer(true),
		LinkLocal: []string{"ipv6"},
		Addresses: []netip.Prefix{
			netip.MustParsePrefix("198.51.100.7/25"),
			netip.MustParsePrefix("2001:db8:1::7/64"),
		},
	}}}
	require.NoError(t, reconciler.Apply(t.Context(), first))
	require.ElementsMatch(t, []string{
		"192.0.2.9/24",
		"2001:db8::9/64",
		"fe80::10/64",
		"198.51.100.7/25",
		"2001:db8:1::7/64",
	}, addressStrings(backend.addresses["kni0"]))
	require.Equal(t, []string{"203.0.113.1/24"}, addressStrings(backend.addresses["management0"]))
	require.Empty(t, backend.deletedAddresses)

	second := netplan.State{Links: []netplan.Link{{
		Name:      "kni0",
		AcceptRA:  boolPointer(true),
		LinkLocal: []string{"ipv6"},
		Addresses: []netip.Prefix{
			netip.MustParsePrefix("2001:db8:1::7/64"),
			netip.MustParsePrefix("203.0.113.7/24"),
		},
	}}}
	require.NoError(t, reconciler.Apply(t.Context(), second))
	require.ElementsMatch(t, []string{
		"192.0.2.9/24",
		"2001:db8::9/64",
		"fe80::10/64",
		"2001:db8:1::7/64",
		"203.0.113.7/24",
	}, addressStrings(backend.addresses["kni0"]))
	require.Equal(t, []string{"198.51.100.7/25"}, backend.deletedAddresses)
	require.Equal(t, []string{
		"kni0/accept_ra=2",
		"kni0/addr_gen_mode=0",
		"kni0/accept_ra=2",
		"kni0/addr_gen_mode=0",
	}, sysctl.writes)
}

func TestReconcilerDeletesOwnedAddressesWhenBaseLeavesDesiredState(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addresses["kni0"] = []vnetlink.Addr{mustAddr("192.0.2.9/24")}
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
	owned := netip.MustParsePrefix("198.51.100.7/25")

	require.NoError(t, reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name:      "kni0",
		Addresses: []netip.Prefix{owned},
	}}}))
	require.NoError(t, reconciler.Apply(t.Context(), netplan.State{}))

	require.Contains(t, backend.links, "kni0")
	require.Equal(t, []string{"192.0.2.9/24"}, addressStrings(backend.addresses["kni0"]))
	require.Equal(t, []string{"198.51.100.7/25"}, backend.deletedAddresses)
}

func TestReconcilerRemovesOnlyForeignIPv6LinkLocalWhenDisabled(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	slaacAddress := mustAddr("2001:db8::9/64")
	slaacAddress.Flags = unix.IFA_F_TEMPORARY
	backend.addresses["kni0"] = []vnetlink.Addr{
		mustAddr("192.0.2.9/24"),
		slaacAddress,
		mustAddr("fe80::10/64"),
	}
	backend.addressesOnUp["kni0"] = []vnetlink.Addr{mustAddr("fe80::11/64")}
	sysctl := &fakeSysctl{}
	reconciler := netreconcile.NewReconciler(backend, sysctl)

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name:      "kni0",
		AcceptRA:  boolPointer(false),
		Addresses: []netip.Prefix{netip.MustParsePrefix("198.51.100.7/25")},
	}}})
	require.NoError(t, err)
	require.ElementsMatch(t, []string{
		"192.0.2.9/24",
		"2001:db8::9/64",
		"198.51.100.7/25",
	}, addressStrings(backend.addresses["kni0"]))
	require.ElementsMatch(t, []string{"fe80::10/64", "fe80::11/64"}, backend.deletedAddresses)
	require.Equal(t, []string{"kni0/accept_ra=0", "kni0/addr_gen_mode=1"}, sysctl.writes)
}

func TestReconcilerTracksSuccessfulAddressesAfterPartialApply(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addresses["kni0"] = []vnetlink.Addr{mustAddr("192.0.2.9/24")}
	backend.addrReplaceErr["kni0/2001:db8::7/64"] = errors.New("replace failed")
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name: "kni0",
		Addresses: []netip.Prefix{
			netip.MustParsePrefix("198.51.100.7/25"),
			netip.MustParsePrefix("2001:db8::7/64"),
		},
	}}})
	require.ErrorContains(t, err, `replace address "2001:db8::7/64"`)
	require.ElementsMatch(t, []string{"192.0.2.9/24", "198.51.100.7/25"}, addressStrings(backend.addresses["kni0"]))

	delete(backend.addrReplaceErr, "kni0/2001:db8::7/64")
	require.NoError(t, reconciler.Apply(t.Context(), netplan.State{
		Links: []netplan.Link{{Name: "kni0"}},
	}))
	require.Equal(t, []string{"192.0.2.9/24"}, addressStrings(backend.addresses["kni0"]))
	require.Equal(t, []string{"198.51.100.7/25"}, backend.deletedAddresses)
}

func TestReconcilerDoesNotDeleteAddressFromReplacedLink(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
	address := netip.MustParsePrefix("198.51.100.7/25")
	require.NoError(t, reconciler.Apply(t.Context(), netplan.State{
		Links: []netplan.Link{{Name: "kni0", Addresses: []netip.Prefix{address}}},
	}))

	backend.linkByNameCalls = map[string]int{}
	backend.beforeLinkByName = func(name string, call int) {
		if name == "kni0" && call == 5 {
			backend.addLink(dummy("kni0", 11, "foreign-owner"))
		}
	}
	err := reconciler.Apply(t.Context(), netplan.State{
		Links: []netplan.Link{{Name: "kni0"}},
	})

	require.ErrorContains(t, err, "identity changed during reconciliation")
	require.True(t, netreconcile.IsRetryable(err))
	require.Empty(t, backend.deletedAddresses)
	require.Equal(t, []string{"198.51.100.7/25"}, addressStrings(backend.addresses["kni0"]))
}

func TestReconcilerSerializesConcurrentApply(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
	state := netplan.State{Links: []netplan.Link{{
		Name:      "kni0",
		Addresses: []netip.Prefix{netip.MustParsePrefix("198.51.100.7/25")},
	}}}

	var group errgroup.Group
	for range 2 {
		group.Go(func() error {
			return reconciler.Apply(t.Context(), state)
		})
	}
	require.NoError(t, group.Wait())
	require.Equal(t, []string{"198.51.100.7/25"}, addressStrings(backend.addresses["kni0"]))
}

func TestReconcilerStopsWaitingForConcurrentApplyAfterCancellation(t *testing.T) {
	backend := newFakeBackend()
	entered := make(chan struct{})
	release := make(chan struct{})
	backend.beforeLinkList = func(int) {
		close(entered)
		<-release
	}
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- reconciler.Apply(t.Context(), netplan.State{})
	}()
	<-entered

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := reconciler.Apply(ctx, netplan.State{})

	require.ErrorIs(t, err, context.Canceled)
	close(release)
	require.NoError(t, <-firstResult)
}

func TestReconcilerDeletesOnlyStaleOwnedVLANs(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ownedAlias))
	backend.addLink(vlan("owned.100", 20, 10, 100, ownedAlias))
	backend.addLink(vlan("foreign.200", 21, 10, 200, "foreign-owner"))
	backend.addLink(dummy("owned-dummy", 22, ownedAlias))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	require.NoError(t, reconciler.Apply(t.Context(), netplan.State{}))
	require.Equal(t, []string{"owned.100"}, backend.deleted)
	require.Contains(t, backend.links, "kni0")
	require.Contains(t, backend.links, "foreign.200")
	require.Contains(t, backend.links, "owned-dummy")
}

func TestReconcilerDoesNotDeleteReplacedStaleVLAN(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(vlan("stale.100", 20, 10, 100, ownedAlias))
	backend.beforeLinkByName = func(name string, call int) {
		if name == "stale.100" && call == 1 {
			backend.addLink(vlan("stale.100", 21, 10, 100, "foreign-owner"))
		}
	}
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{})

	require.ErrorContains(t, err, "identity changed during reconciliation")
	require.True(t, netreconcile.IsRetryable(err))
	require.Empty(t, backend.deleted)
	require.Equal(t, 21, backend.links["stale.100"].Attrs().Index)
}

func TestReconcilerDoesNotDeleteStaleVLANAfterCancellationDuringValidation(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(vlan("stale.100", 20, 10, 100, ownedAlias))
	ctx, cancel := context.WithCancel(t.Context())
	backend.beforeLinkByName = func(name string, call int) {
		if name == "stale.100" && call == 1 {
			cancel()
		}
	}
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(ctx, netplan.State{})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, backend.deleted)
	require.Contains(t, backend.links, "stale.100")
}

func TestReconcilerDoesNotDeleteAddressAfterCancellationDuringValidation(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	stale := mustAddr("192.0.2.1/24")
	backend.addresses["kni0"] = []vnetlink.Addr{stale}
	ctx, cancel := context.WithCancel(t.Context())
	backend.beforeLinkByName = func(name string, call int) {
		if name == "kni0" && call == 6 {
			cancel()
		}
	}
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	require.NoError(t, reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name:      "kni0",
		Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")},
	}}}))

	backend.linkByNameCalls = map[string]int{}
	backend.beforeLinkByName = func(name string, call int) {
		if name == "kni0" && call == 6 {
			cancel()
		}
	}
	err := reconciler.Apply(ctx, netplan.State{Links: []netplan.Link{{Name: "kni0"}}})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, backend.deletedAddresses)
	require.Equal(t, []string{"192.0.2.1/24"}, addressStrings(backend.addresses["kni0"]))
}

func TestReconcilerDeletesExplicitlyOwnedStaleVLANWithResidualAddress(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(vlan("stale.100", 20, 10, 100, ownedAlias))
	backend.addresses["stale.100"] = []vnetlink.Addr{mustAddr("192.0.2.9/24")}
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	require.NoError(t, reconciler.Apply(t.Context(), netplan.State{}))
	require.Equal(t, []string{"stale.100"}, backend.deleted)
	require.NotContains(t, backend.links, "stale.100")
}

func TestReconcilerDeletesExplicitlyOwnedStaleVLANWithoutAddressDump(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(vlan("stale.100", 20, 10, 100, ownedAlias))
	backend.addrListErr["stale.100"] = errors.New("address dump unavailable")
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	require.NoError(t, reconciler.Apply(t.Context(), netplan.State{}))
	require.Equal(t, []string{"stale.100"}, backend.deleted)
	require.NotContains(t, backend.links, "stale.100")
}

func TestReconcilerSkipsPerAddressCleanupBeforeDeletingOwnedVLAN(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})
	address := netip.MustParsePrefix("192.0.2.9/24")

	require.NoError(t, reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{
			Name:      "tenant.100",
			Parent:    "kni0",
			VLANID:    100,
			Addresses: []netip.Prefix{address},
		},
	}}))
	backend.addrListErr["tenant.100"] = errors.New("address dump unavailable")

	require.NoError(t, reconciler.Apply(t.Context(), netplan.State{
		Links: []netplan.Link{{Name: "kni0"}},
	}))
	require.Equal(t, []string{"tenant.100"}, backend.deleted)
	require.NotContains(t, backend.links, "tenant.100")
}

func TestReconcilerAppliesIPv6Sysctls(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addLink(vlan("tenant.100", 20, 10, 100, ownedAlias))
	sysctl := &fakeSysctl{}
	reconciler := netreconcile.NewReconciler(backend, sysctl)

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0", AcceptRA: boolPointer(true), LinkLocal: []string{"ipv6"}},
		{
			Name:      "tenant.100",
			Parent:    "kni0",
			VLANID:    100,
			AcceptRA:  boolPointer(false),
			LinkLocal: []string{},
		},
	}})
	require.NoError(t, err)
	require.Equal(t, []string{
		"kni0/accept_ra=2",
		"kni0/addr_gen_mode=0",
		"tenant.100/accept_ra=0",
		"tenant.100/addr_gen_mode=1",
	}, sysctl.writes)
}

func TestReconcilerAppliesIPv6PolicyBeforeBringingLinkUp(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	sysctl := &fakeSysctl{
		beforeSet: func() {
			require.Empty(t, backend.up)
		},
	}
	reconciler := netreconcile.NewReconciler(backend, sysctl)

	require.NoError(t, reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name:     "kni0",
		AcceptRA: boolPointer(false),
	}}}))
	require.Equal(t, []string{"kni0"}, backend.up)
}

func TestReconcilerDoesNotWriteSysctlToReplacementLink(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	sysctl := &fakeSysctl{
		beforeSet: func() {
			backend.addLink(dummy("kni0", 11, "foreign-owner"))
		},
	}
	reconciler := netreconcile.NewReconciler(backend, sysctl)

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name:     "kni0",
		AcceptRA: boolPointer(false),
	}}})

	require.ErrorContains(t, err, `link "kni0" identity changed during reconciliation`)
	require.True(t, netreconcile.IsRetryable(err))
	require.Empty(t, sysctl.writes)
	require.Empty(t, backend.up)
}

func TestReconcilerMarksCancellationDuringSysctlValidationNonRetryable(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	ctx, cancel := context.WithCancel(t.Context())
	sysctl := &fakeSysctl{beforeSet: cancel}
	reconciler := netreconcile.NewReconciler(backend, sysctl)

	err := reconciler.Apply(ctx, netplan.State{Links: []netplan.Link{{Name: "kni0"}}})

	require.ErrorIs(t, err, context.Canceled)
	require.False(t, netreconcile.IsRetryable(err))
	require.Empty(t, sysctl.writes)
	require.Empty(t, backend.up)
}

func TestReconcilerGivesWrappedCancellationPriorityOverRetryableError(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	sysctl := &fakeSysctl{setError: errors.Join(
		context.Canceled,
		&netreconcile.ApplyError{
			Operation: "injected validation",
			Retryable: true,
			Err:       errors.New("transient failure"),
		},
	)}
	reconciler := netreconcile.NewReconciler(backend, sysctl)

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{Name: "kni0"}}})

	require.ErrorIs(t, err, context.Canceled)
	require.False(t, netreconcile.IsRetryable(err))
	require.Empty(t, sysctl.writes)
}

func TestReconcilerRejectsIPv4LinkLocalBeforeMutation(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name:      "kni0",
		MTU:       9000,
		LinkLocal: []string{"ipv4"},
	}}})
	require.ErrorContains(t, err, "IPv4 link-local addressing is unsupported")
	require.Zero(t, backend.links["kni0"].Attrs().MTU)
	require.Empty(t, backend.up)
}

func TestReconcilerRejectsProcfsEscapingInterfaceName(t *testing.T) {
	backend := newFakeBackend()
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{Name: "../conf/all"}}})
	require.ErrorContains(t, err, "contains a character rejected by Linux")
	require.Empty(t, backend.listCalls)
}

func TestReconcilerRejectsKernelInvalidAndReservedInterfaceNames(t *testing.T) {
	for _, name := range []string{"tenant:100", "tenant 100", "all", "default"} {
		t.Run(name, func(t *testing.T) {
			backend := newFakeBackend()
			reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

			err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{Name: name}}})

			require.ErrorContains(t, err, "interface name")
			require.False(t, netreconcile.IsRetryable(err))
			require.Empty(t, backend.listCalls)
		})
	}
}

func TestReconcilerStopsBetweenOperationsWhenContextIsCanceled(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	ctx, cancel := context.WithCancel(t.Context())
	sysctl := &fakeSysctl{cancel: cancel}
	reconciler := netreconcile.NewReconciler(backend, sysctl)

	err := reconciler.Apply(ctx, netplan.State{Links: []netplan.Link{{
		Name:     "kni0",
		AcceptRA: boolPointer(false),
	}}})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, []string{"kni0/accept_ra=0"}, sysctl.writes)
	require.Equal(t, []string{"links", "addresses:kni0"}, backend.listCalls)
	require.Empty(t, backend.replacedAddresses)
	require.Empty(t, backend.deletedAddresses)
}

func TestReconcilerDoesNotCleanUpAfterIncompleteList(t *testing.T) {
	t.Run("link list", func(t *testing.T) {
		backend := newFakeBackend()
		backend.addLink(vlan("stale.100", 20, 10, 100, ownedAlias))
		backend.linkListErr = errors.New("dump interrupted")
		reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

		err := reconciler.Apply(t.Context(), netplan.State{})
		require.ErrorContains(t, err, "list links")
		require.Empty(t, backend.deleted)
		require.Contains(t, backend.links, "stale.100")
	})

	t.Run("address list", func(t *testing.T) {
		backend := newFakeBackend()
		backend.addLink(dummy("kni0", 10, ""))
		backend.addLink(dummy("kni1", 11, ""))
		backend.addLink(vlan("stale.100", 20, 10, 100, ownedAlias))
		backend.addresses["kni0"] = []vnetlink.Addr{mustAddr("fe80::10/64")}
		backend.addrListErr["kni1"] = errors.New("dump interrupted")
		reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

		err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
			{Name: "kni0"},
			{Name: "kni1"},
		}})
		require.ErrorContains(t, err, `list addresses on link "kni1"`)
		require.Empty(t, backend.deleted)
		require.Empty(t, backend.deletedAddresses)
		require.Equal(t, []string{"fe80::10/64"}, addressStrings(backend.addresses["kni0"]))
		require.Contains(t, backend.links, "stale.100")
	})
}

type fakeBackend struct {
	links             map[string]vnetlink.Link
	addresses         map[string][]vnetlink.Addr
	addressesOnUp     map[string][]vnetlink.Addr
	addrListErr       map[string]error
	addrReplaceErr    map[string]error
	aliasErr          map[string]error
	linkListErr       error
	nextIndex         int
	added             []string
	deleted           []string
	aliasChanges      []string
	up                []string
	replacedAddresses []string
	deletedAddresses  []string
	listCalls         []string
	linkByNameCalls   map[string]int
	beforeLinkByName  func(string, int)
	addrListCalls     map[string]int
	beforeAddrList    func(string, int)
	linkListCalls     int
	beforeLinkList    func(int)
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		links:           map[string]vnetlink.Link{},
		addresses:       map[string][]vnetlink.Addr{},
		addressesOnUp:   map[string][]vnetlink.Addr{},
		addrListErr:     map[string]error{},
		addrReplaceErr:  map[string]error{},
		aliasErr:        map[string]error{},
		linkByNameCalls: map[string]int{},
		addrListCalls:   map[string]int{},
		nextIndex:       100,
	}
}

func (m *fakeBackend) addLink(link vnetlink.Link) {
	m.links[link.Attrs().Name] = link
}

func (m *fakeBackend) LinkList() ([]vnetlink.Link, error) {
	m.linkListCalls++
	if m.beforeLinkList != nil {
		m.beforeLinkList(m.linkListCalls)
	}
	m.listCalls = append(m.listCalls, "links")
	if m.linkListErr != nil {
		return nil, m.linkListErr
	}
	links := make([]vnetlink.Link, 0, len(m.links))
	for _, link := range m.links {
		links = append(links, link)
	}
	sort.Slice(links, func(first, second int) bool {
		return links[first].Attrs().Name < links[second].Attrs().Name
	})
	return links, nil
}

func (m *fakeBackend) LinkByName(name string) (vnetlink.Link, error) {
	m.linkByNameCalls[name]++
	if m.beforeLinkByName != nil {
		m.beforeLinkByName(name, m.linkByNameCalls[name])
	}
	link, ok := m.links[name]
	if !ok {
		return nil, fmt.Errorf("link %q not found", name)
	}
	return link, nil
}

func (m *fakeBackend) LinkAdd(link vnetlink.Link) error {
	if _, exists := m.links[link.Attrs().Name]; exists {
		return errors.New("link already exists")
	}
	if vlanLink, ok := link.(*vnetlink.Vlan); ok {
		parent := m.linkByIndex(vlanLink.ParentIndex)
		if parent == nil {
			return errors.New("VLAN parent is missing")
		}
		if link.Attrs().MTU == 0 {
			link.Attrs().MTU = parent.Attrs().MTU
		}
		if parent.Attrs().MTU < link.Attrs().MTU {
			return errors.New("VLAN MTU exceeds parent MTU")
		}
		for _, current := range m.links {
			currentVLAN, isVLAN := current.(*vnetlink.Vlan)
			if isVLAN && currentVLAN.ParentIndex == vlanLink.ParentIndex &&
				currentVLAN.VlanId == vlanLink.VlanId &&
				currentVLAN.VlanProtocol == vlanLink.VlanProtocol {
				return errors.New("VLAN identity already exists")
			}
		}
	}
	link.Attrs().Index = m.nextIndex
	m.nextIndex++
	m.links[link.Attrs().Name] = link
	m.added = append(m.added, link.Attrs().Name)
	return nil
}

func (m *fakeBackend) LinkDel(link vnetlink.Link) error {
	delete(m.links, link.Attrs().Name)
	delete(m.addresses, link.Attrs().Name)
	m.deleted = append(m.deleted, link.Attrs().Name)
	return nil
}

func (m *fakeBackend) LinkSetAlias(link vnetlink.Link, alias string) error {
	if err := m.aliasErr[link.Attrs().Name]; err != nil {
		return err
	}
	link.Attrs().Alias = alias
	m.aliasChanges = append(m.aliasChanges, link.Attrs().Name+"="+alias)
	return nil
}

func (m *fakeBackend) LinkSetMTU(link vnetlink.Link, mtu int) error {
	if _, isVLAN := link.(*vnetlink.Vlan); !isVLAN {
		for _, candidate := range m.links {
			vlanLink, ok := candidate.(*vnetlink.Vlan)
			if ok && vlanLink.ParentIndex == link.Attrs().Index && candidate.Attrs().MTU > mtu {
				return errors.New("parent MTU is smaller than VLAN MTU")
			}
		}
	}
	link.Attrs().MTU = mtu
	return nil
}

func (m *fakeBackend) linkByIndex(index int) vnetlink.Link {
	for _, link := range m.links {
		if link.Attrs().Index == index {
			return link
		}
	}
	return nil
}

func (m *fakeBackend) LinkSetUp(link vnetlink.Link) error {
	link.Attrs().Flags |= net.FlagUp
	name := link.Attrs().Name
	m.addresses[name] = append(m.addresses[name], m.addressesOnUp[name]...)
	delete(m.addressesOnUp, name)
	m.up = append(m.up, name)
	return nil
}

func (m *fakeBackend) AddrList(link vnetlink.Link, _ int) ([]vnetlink.Addr, error) {
	name := link.Attrs().Name
	m.addrListCalls[name]++
	if m.beforeAddrList != nil {
		m.beforeAddrList(name, m.addrListCalls[name])
	}
	m.listCalls = append(m.listCalls, "addresses:"+name)
	if err := m.addrListErr[name]; err != nil {
		return nil, err
	}
	return append([]vnetlink.Addr(nil), m.addresses[name]...), nil
}

func (m *fakeBackend) AddrReplace(link vnetlink.Link, address *vnetlink.Addr) error {
	name := link.Attrs().Name
	wanted := addressString(*address)
	if err := m.addrReplaceErr[name+"/"+wanted]; err != nil {
		return err
	}
	current := m.addresses[name][:0]
	for _, existing := range m.addresses[name] {
		if addressString(existing) != wanted {
			current = append(current, existing)
		}
	}
	m.addresses[name] = append(current, *address)
	m.replacedAddresses = append(m.replacedAddresses, wanted)
	return nil
}

func (m *fakeBackend) AddrDel(link vnetlink.Link, address *vnetlink.Addr) error {
	name := link.Attrs().Name
	deleted := addressString(*address)
	current := m.addresses[name][:0]
	for _, existing := range m.addresses[name] {
		if addressString(existing) != deleted {
			current = append(current, existing)
		}
	}
	m.addresses[name] = current
	m.deletedAddresses = append(m.deletedAddresses, deleted)
	return nil
}

type fakeSysctl struct {
	writes    []string
	cancel    context.CancelFunc
	beforeSet func()
	setError  error
}

func (m *fakeSysctl) SetIPv6(
	_ context.Context,
	name string,
	setting string,
	value string,
	validate func() error,
) error {
	if m.beforeSet != nil {
		m.beforeSet()
	}
	if err := validate(); err != nil {
		return err
	}
	if m.setError != nil {
		return m.setError
	}
	m.writes = append(m.writes, name+"/"+setting+"="+value)
	if m.cancel != nil {
		m.cancel()
	}
	return nil
}

func dummy(name string, index int, alias string) *vnetlink.Dummy {
	return &vnetlink.Dummy{LinkAttrs: vnetlink.LinkAttrs{Name: name, Index: index, Alias: alias}}
}

func vlan(name string, index, parentIndex, vlanID int, alias string) *vnetlink.Vlan {
	return &vnetlink.Vlan{
		LinkAttrs: vnetlink.LinkAttrs{
			Name:        name,
			Index:       index,
			ParentIndex: parentIndex,
			Alias:       alias,
		},
		VlanId:       vlanID,
		VlanProtocol: vnetlink.VLAN_PROTOCOL_8021Q,
	}
}

func mustAddr(value string) vnetlink.Addr {
	address, err := vnetlink.ParseAddr(value)
	if err != nil {
		panic(err)
	}
	return *address
}

func addressStrings(addresses []vnetlink.Addr) []string {
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		result = append(result, addressString(address))
	}
	return result
}

func boolPointer(value bool) *bool {
	return &value
}

func addressString(address vnetlink.Addr) string {
	if address.IPNet == nil {
		return "<nil>"
	}
	ones, _ := address.IPNet.Mask.Size()
	return strings.TrimSpace(address.IPNet.IP.String() + "/" + fmt.Sprint(ones))
}

var _ netreconcile.Backend = (*fakeBackend)(nil)
var _ netreconcile.Sysctl = (*fakeSysctl)(nil)
