package netlink_test

import (
	"bytes"
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

// Test_Reconciler_LateKNI verifies that an absent base link is never created
// and a later pass brings it up once it appears.
func Test_Reconciler_LateKNI(t *testing.T) {
	backend := newFakeBackend()
	sysctl := &fakeSysctl{}
	reconciler := netreconcile.NewReconciler(backend, sysctl)
	state := netplan.State{Links: []netplan.Link{{Name: "kni0"}}}

	err := reconciler.Apply(t.Context(), state)
	require.ErrorContains(t, err, `find base link "kni0"`)
	require.ErrorContains(t, err, "base KNI link is not available yet")
	require.Empty(t, backend.added)
	require.Empty(t, backend.deleted)

	backend.addLink(dummy("kni0", 10, ""))
	require.NoError(t, reconciler.Apply(t.Context(), state))
	require.True(t, backend.links["kni0"].Attrs().Flags&net.FlagUp != 0)
}

// Test_Reconciler_ZeroValue verifies that an uninitialized reconciler returns
// an error instead of attempting kernel operations.
func Test_Reconciler_ZeroValue(t *testing.T) {
	var reconciler netreconcile.Reconciler

	err := reconciler.Apply(t.Context(), netplan.State{})

	require.ErrorContains(t, err, "nil apply slot")
}

// Test_Reconciler_CreatesAndMarksVLAN verifies that a new VLAN has the requested
// parent, tag, MTU, and ownership marker, with both links brought up.
func Test_Reconciler_CreatesAndMarksVLAN(t *testing.T) {
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

// Test_Reconciler_CreatesZeroMTUVLANAtDesiredParentMTU verifies that an omitted
// child MTU inherits the requested parent size rather than its old size.
func Test_Reconciler_CreatesZeroMTUVLANAtDesiredParentMTU(t *testing.T) {
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

// Test_Reconciler_LowersChildMTUBeforeParent verifies that shrinking a VLAN and
// its parent converges without violating the kernel's child MTU constraint.
func Test_Reconciler_LowersChildMTUBeforeParent(t *testing.T) {
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

// Test_Reconciler_RejectsOversizedChildMTU verifies that an incompatible desired
// child size fails before inspecting or changing kernel links.
func Test_Reconciler_RejectsOversizedChildMTU(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0", MTU: 1500},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100, MTU: 2000},
	}})

	require.ErrorContains(t, err, "VLAN MTU 2000 exceeds parent")
	require.Empty(t, backend.listCalls)
	require.Empty(t, backend.added)
	require.Zero(t, backend.links["kni0"].Attrs().MTU)
}

// Test_Reconciler_RejectsNegativeMTU verifies that a negative size fails before
// the first kernel link listing.
func Test_Reconciler_RejectsNegativeMTU(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name: "kni0",
		MTU:  -1,
	}}})

	require.ErrorContains(t, err, "MTU must be within")
	require.Empty(t, backend.listCalls)
}

// Test_Reconciler_RejectsOversizedMTU verifies that sizes exceeding the signed
// kernel limit fail before the first link listing.
func Test_Reconciler_RejectsOversizedMTU(t *testing.T) {
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
	require.Empty(t, backend.listCalls)
}

// Test_Reconciler_RejectsPreservedChildMTU verifies that an omitted child size
// cannot permit shrinking its parent below the child's current MTU.
func Test_Reconciler_RejectsPreservedChildMTU(t *testing.T) {
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
	require.Equal(t, 9000, backend.links["kni0"].Attrs().MTU)
	require.Empty(t, backend.up)
}

// Test_Reconciler_RejectsParentMTUBelowUnmanagedChild verifies that foreign
// children constrain parent resizing without being modified themselves.
func Test_Reconciler_RejectsParentMTUBelowUnmanagedChild(t *testing.T) {
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
	require.Equal(t, 9000, backend.links["kni0"].Attrs().MTU)
	require.Empty(t, backend.up)
}

// Test_Reconciler_ValidatesMarkedNonVLANMTU verifies that an ownership alias on
// a non-VLAN child cannot bypass the parent size constraint.
func Test_Reconciler_ValidatesMarkedNonVLANMTU(t *testing.T) {
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
	require.Equal(t, 9000, backend.links["kni0"].Attrs().MTU)
}

// Test_Reconciler_RechecksChildMTUs verifies that a foreign child's concurrent
// MTU increase prevents shrinking the parent using stale validation.
func Test_Reconciler_RechecksChildMTUs(t *testing.T) {
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
	require.Equal(t, 9000, backend.links["kni0"].Attrs().MTU)
}

// Test_Reconciler_RejectsMissingDesiredParent verifies that an existing kernel
// parent cannot satisfy an incomplete desired VLAN topology.
func Test_Reconciler_RejectsMissingDesiredParent(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(vlan("parent.100", 10, 9, 100, ownedAlias))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name:   "tenant.200",
		Parent: "parent.100",
		VLANID: 200,
	}}})

	require.ErrorContains(t, err, `parent "parent.100" is missing from desired state`)
	require.Empty(t, backend.listCalls)
	require.Empty(t, backend.added)
	require.Empty(t, backend.deleted)
}

// Test_Reconciler_RejectsDuplicateVLANIdentity verifies that different names
// cannot claim the same parent and tag before any kernel inspection.
func Test_Reconciler_RejectsDuplicateVLANIdentity(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "first.100", Parent: "kni0", VLANID: 100},
		{Name: "second.100", Parent: "kni0", VLANID: 100},
	}})

	require.ErrorContains(t, err, `VLAN parent "kni0" ID 100 is already used`)
	require.Empty(t, backend.listCalls)
	require.Empty(t, backend.added)
}

// Test_Reconciler_RequiresOwnershipHandoff verifies that a matching unmarked
// VLAN is neither adopted nor deleted during reconciliation.
func Test_Reconciler_RequiresOwnershipHandoff(t *testing.T) {
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
	require.Empty(t, backend.links["tenant.100"].Attrs().Alias)
	require.Empty(t, backend.deleted)
}

// Test_Reconciler_RejectsUnownedVLANBeforeConfiguration verifies that a missing
// ownership marker prevents changes to links, addresses, and IPv6 policy.
func Test_Reconciler_RejectsUnownedVLANBeforeConfiguration(t *testing.T) {
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

// Test_Reconciler_RejectsForeignOwnedVLAN verifies that matching topology does
// not authorize changing a VLAN marked as belonging to another manager.
func Test_Reconciler_RejectsForeignOwnedVLAN(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	backend.addLink(vlan("tenant.100", 20, 10, 100, "foreign-owner"))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100},
	}})
	require.ErrorContains(t, err, `link alias "foreign-owner" belongs to another owner`)
	require.Equal(t, "foreign-owner", backend.links["tenant.100"].Attrs().Alias)
	require.Empty(t, backend.deleted)
	require.Empty(t, backend.up)
}

// Test_Reconciler_RejectsMismatchedExistingVLAN verifies that foreign link type,
// parent, or tag conflicts fail without link, address, or sysctl mutations.
func Test_Reconciler_RejectsMismatchedExistingVLAN(t *testing.T) {
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
			require.Empty(t, backend.added)
			require.Empty(t, backend.deleted)
			require.Empty(t, backend.links["tenant.100"].Attrs().Alias)
			require.Empty(t, backend.up)
			require.Empty(t, backend.replacedAddresses)
			require.Empty(t, backend.deletedAddresses)
			require.Empty(t, sysctl.writes)
		})
	}
}

// Test_Reconciler_RejectsWrongVLANProtocol verifies that an existing 802.1ad
// link cannot satisfy an 802.1q request and remains untouched.
func Test_Reconciler_RejectsWrongVLANProtocol(t *testing.T) {
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
	require.Empty(t, backend.added)
	require.Empty(t, backend.deleted)
}

// Test_Reconciler_RejectsVLANAsBaseKNI verifies that a VLAN using a base link's
// name is rejected before changing its MTU or bringing it up.
func Test_Reconciler_RejectsVLANAsBaseKNI(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(vlan("kni0", 10, 9, 100, ""))
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{
		Name: "kni0",
		MTU:  9000,
	}}})
	require.ErrorContains(t, err, "base KNI link is unexpectedly a VLAN")
	require.Zero(t, backend.links["kni0"].Attrs().MTU)
	require.Empty(t, backend.up)
}

// Test_Reconciler_RecreatesMismatchedOwnedVLAN verifies that an owned topology
// mismatch is replaced with the requested parent, tag, marker, and address.
func Test_Reconciler_RecreatesMismatchedOwnedVLAN(t *testing.T) {
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

// Test_Reconciler_RenamesOwnedVLAN verifies that a stale owned name releases
// its parent and tag so a replacement can claim the same VLAN identity.
func Test_Reconciler_RenamesOwnedVLAN(t *testing.T) {
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

// Test_Reconciler_PreservesMTUDuringRecreation verifies that an omitted desired
// MTU retains the old child's size when its topology must be replaced.
func Test_Reconciler_PreservesMTUDuringRecreation(t *testing.T) {
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

// Test_Reconciler_RejectsRecreatedChildMTU verifies that an inherited child MTU
// exceeding the desired parent prevents both deletion and recreation.
func Test_Reconciler_RejectsRecreatedChildMTU(t *testing.T) {
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
	require.Empty(t, backend.deleted)
	require.Empty(t, backend.added)
}

// Test_Reconciler_RecreatesVLANWithResidualAddress verifies that an old address
// does not prevent replacing an explicitly owned VLAN with wrong topology.
func Test_Reconciler_RecreatesVLANWithResidualAddress(t *testing.T) {
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

// Test_Reconciler_PreservesVLANAfterIncompleteDump verifies that a failed
// address dump on another managed link prevents destructive reconciliation.
func Test_Reconciler_PreservesVLANAfterIncompleteDump(t *testing.T) {
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
	require.Equal(t, ownedAlias, backend.links["tenant.100"].Attrs().Alias)
	require.Empty(t, backend.up)
}

// Test_Reconciler_PreservesConcurrentVLANReplacement verifies that a new
// foreign link at the same name is not deleted using an old ownership check.
func Test_Reconciler_PreservesConcurrentVLANReplacement(t *testing.T) {
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
	require.Empty(t, backend.deleted)
	require.Equal(t, 21, backend.links["tenant.100"].Attrs().Index)
}

// Test_Reconciler_StopsAfterParentReplacement verifies that a changed parent
// identity prevents child MTU, link-state, and sysctl mutations.
func Test_Reconciler_StopsAfterParentReplacement(t *testing.T) {
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
	require.Zero(t, backend.links["tenant.100"].Attrs().MTU)
	require.Empty(t, backend.up)
	require.Empty(t, sysctl.writes)
}

// Test_Reconciler_PreservesConcurrentAddress verifies that an address arriving
// during revalidation survives replacement of the owned VLAN.
func Test_Reconciler_PreservesConcurrentAddress(t *testing.T) {
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

// Test_Reconciler_ValidatesNewlyCreatedVLAN verifies that a foreign replacement
// appearing after creation is not relabeled as owned.
func Test_Reconciler_ValidatesNewlyCreatedVLAN(t *testing.T) {
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
	require.Equal(t, "foreign-owner", backend.links["tenant.100"].Attrs().Alias)
}

// Test_Reconciler_PreservesForeignAddresses verifies that subsequent snapshots
// remove only previously claimed addresses and leave unmanaged links intact.
func Test_Reconciler_PreservesForeignAddresses(t *testing.T) {
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

// Test_Reconciler_RemovesRetiredBaseAddresses verifies that retiring a base
// link removes its claimed addresses but preserves the link and foreign IPs.
func Test_Reconciler_RemovesRetiredBaseAddresses(t *testing.T) {
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

// Test_Reconciler_DisablesIPv6LinkLocal verifies that disabling generation
// removes link-local IPs present before or after link-up, but keeps global IPs.
func Test_Reconciler_DisablesIPv6LinkLocal(t *testing.T) {
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

// Test_Reconciler_TracksPartialAddressSuccess verifies that addresses installed
// before a later failure remain owned and can be removed on the next pass.
func Test_Reconciler_TracksPartialAddressSuccess(t *testing.T) {
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

// Test_Reconciler_PreservesReplacedLinkAddresses verifies that a changed link
// identity invalidates address deletion authorized by an earlier snapshot.
func Test_Reconciler_PreservesReplacedLinkAddresses(t *testing.T) {
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
	require.Empty(t, backend.deletedAddresses)
	require.Equal(t, []string{"198.51.100.7/25"}, addressStrings(backend.addresses["kni0"]))
}

// Test_Reconciler_SerializesConcurrentApply verifies that concurrent identical
// requests both succeed and converge on a single configured address.
func Test_Reconciler_SerializesConcurrentApply(t *testing.T) {
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

// Test_Reconciler_CancelsConcurrentWait verifies that a canceled caller returns
// without waiting for the active reconciliation to finish.
func Test_Reconciler_CancelsConcurrentWait(t *testing.T) {
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

// Test_Reconciler_DeletesOnlyStaleOwnedVLANs verifies that an empty desired
// state preserves foreign VLANs and marked non-VLAN links.
func Test_Reconciler_DeletesOnlyStaleOwnedVLANs(t *testing.T) {
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

// Test_Reconciler_PreservesReplacedStaleVLAN verifies that a foreign link
// replacing a stale owned VLAN cannot be deleted by the old cleanup plan.
func Test_Reconciler_PreservesReplacedStaleVLAN(t *testing.T) {
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
	require.Empty(t, backend.deleted)
	require.Equal(t, 21, backend.links["stale.100"].Attrs().Index)
}

// Test_Reconciler_CancelsStaleVLANDeletion verifies that cancellation during
// identity validation preserves the link and returns the cancellation error.
func Test_Reconciler_CancelsStaleVLANDeletion(t *testing.T) {
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

// Test_Reconciler_CancelsAddressDeletion verifies that cancellation during
// link revalidation preserves an address scheduled for removal.
func Test_Reconciler_CancelsAddressDeletion(t *testing.T) {
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

// Test_Reconciler_DeletesStaleVLANWithResidualAddress verifies that residual
// addresses do not prevent deletion of an explicitly owned obsolete link.
func Test_Reconciler_DeletesStaleVLANWithResidualAddress(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(vlan("stale.100", 20, 10, 100, ownedAlias))
	backend.addresses["stale.100"] = []vnetlink.Addr{mustAddr("192.0.2.9/24")}
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	require.NoError(t, reconciler.Apply(t.Context(), netplan.State{}))
	require.Equal(t, []string{"stale.100"}, backend.deleted)
	require.NotContains(t, backend.links, "stale.100")
}

// Test_Reconciler_DeletesStaleVLANWithoutAddressDump verifies that a failing
// address dump does not block deletion of an explicitly owned obsolete link.
func Test_Reconciler_DeletesStaleVLANWithoutAddressDump(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(vlan("stale.100", 20, 10, 100, ownedAlias))
	backend.addrListErr["stale.100"] = errors.New("address dump unavailable")
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	require.NoError(t, reconciler.Apply(t.Context(), netplan.State{}))
	require.Equal(t, []string{"stale.100"}, backend.deleted)
	require.NotContains(t, backend.links, "stale.100")
}

// Test_Reconciler_SkipsDeletedVLANAddressCleanup verifies that withdrawing a
// previously configured VLAN succeeds even when its address dump is unavailable.
func Test_Reconciler_SkipsDeletedVLANAddressCleanup(t *testing.T) {
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

// Test_Reconciler_AppliesIPv6Sysctls verifies that each link's RA and link-local
// choices produce the corresponding kernel policy values independently.
func Test_Reconciler_AppliesIPv6Sysctls(t *testing.T) {
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

// Test_Reconciler_AppliesIPv6PolicyBeforeLinkUp verifies that IPv6 policy is
// installed while the link is still down, before automatic address generation.
func Test_Reconciler_AppliesIPv6PolicyBeforeLinkUp(t *testing.T) {
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

// Test_Reconciler_PreservesReplacementLinkSysctls verifies that a link replaced
// during sysctl validation receives neither the policy write nor link-up.
func Test_Reconciler_PreservesReplacementLinkSysctls(t *testing.T) {
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
	require.Empty(t, sysctl.writes)
	require.Empty(t, backend.up)
}

// Test_Reconciler_PropagatesCancellationDuringSysctlValidation verifies that
// cancellation is preserved and prevents the pending sysctl and link writes.
func Test_Reconciler_PropagatesCancellationDuringSysctlValidation(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	ctx, cancel := context.WithCancel(t.Context())
	sysctl := &fakeSysctl{beforeSet: cancel}
	reconciler := netreconcile.NewReconciler(backend, sysctl)

	err := reconciler.Apply(ctx, netplan.State{Links: []netplan.Link{{Name: "kni0"}}})

	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, sysctl.writes)
	require.Empty(t, backend.up)
}

// Test_Reconciler_PreservesJoinedSysctlErrors verifies that wrapping retains
// both cancellation and the other cause of a failed sysctl write.
func Test_Reconciler_PreservesJoinedSysctlErrors(t *testing.T) {
	backend := newFakeBackend()
	backend.addLink(dummy("kni0", 10, ""))
	validationErr := errors.New("injected validation failure")
	sysctl := &fakeSysctl{setError: errors.Join(
		context.Canceled,
		validationErr,
	)}
	reconciler := netreconcile.NewReconciler(backend, sysctl)

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{Name: "kni0"}}})

	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, validationErr)
	require.Empty(t, sysctl.writes)
}

// Test_Reconciler_RejectsIPv4LinkLocal verifies that unsupported automatic IPv4
// addressing fails before MTU or link-state changes.
func Test_Reconciler_RejectsIPv4LinkLocal(t *testing.T) {
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

// Test_Reconciler_RejectsProcfsEscape verifies that path traversal in a link
// name fails before any kernel link listing.
func Test_Reconciler_RejectsProcfsEscape(t *testing.T) {
	backend := newFakeBackend()
	reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

	err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{Name: "../conf/all"}}})
	require.ErrorContains(t, err, "contains a character rejected by Linux")
	require.Empty(t, backend.listCalls)
}

// Test_Reconciler_RejectsInvalidInterfaceNames verifies that Linux-invalid and
// reserved sysctl names fail before any kernel link listing.
func Test_Reconciler_RejectsInvalidInterfaceNames(t *testing.T) {
	for _, name := range []string{"tenant:100", "tenant 100", "all", "default"} {
		t.Run(name, func(t *testing.T) {
			backend := newFakeBackend()
			reconciler := netreconcile.NewReconciler(backend, &fakeSysctl{})

			err := reconciler.Apply(t.Context(), netplan.State{Links: []netplan.Link{{Name: name}}})

			require.ErrorContains(t, err, "interface name")
			require.Empty(t, backend.listCalls)
		})
	}
}

// Test_Reconciler_CancelsBetweenOperations verifies that cancellation after
// the first sysctl write prevents further policy writes and address mutations.
func Test_Reconciler_CancelsBetweenOperations(t *testing.T) {
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

// Test_Reconciler_SkipsCleanupAfterIncompleteList verifies that interrupted
// link or address dumps cannot authorize stale VLAN or address deletion.
func Test_Reconciler_SkipsCleanupAfterIncompleteList(t *testing.T) {
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
	linkAddErr        map[string]error
	mtuErr            map[string]error
	linkListErr       error
	nextIndex         int
	added             []string
	deleted           []string
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
		linkAddErr:      map[string]error{},
		mtuErr:          map[string]error{},
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
	if err := m.linkAddErr[link.Attrs().Name]; err != nil {
		return err
	}
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
	for _, child := range m.links {
		if child.Attrs().ParentIndex == link.Attrs().Index {
			if err := m.LinkDel(child); err != nil {
				return err
			}
		}
	}
	delete(m.links, link.Attrs().Name)
	delete(m.addresses, link.Attrs().Name)
	m.deleted = append(m.deleted, link.Attrs().Name)
	return nil
}

func (m *fakeBackend) LinkSetMTU(link vnetlink.Link, mtu int) error {
	if err := m.mtuErr[link.Attrs().Name]; err != nil {
		return err
	}
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
	if child, ok := link.(*vnetlink.Vlan); ok {
		parent := m.linkByIndex(child.ParentIndex)
		if parent == nil || (parent.Attrs().Flags&net.FlagUp == 0 &&
			(child.LooseBinding == nil || !*child.LooseBinding)) {
			return unix.ENETDOWN
		}
	}
	link.Attrs().Flags |= net.FlagUp
	name := link.Attrs().Name
	m.addresses[name] = append(m.addresses[name], m.addressesOnUp[name]...)
	delete(m.addressesOnUp, name)
	m.up = append(m.up, name)
	return nil
}

func (m *fakeBackend) AddrList(link vnetlink.Link, family int) ([]vnetlink.Addr, error) {
	name := link.Attrs().Name
	m.addrListCalls[name]++
	if m.beforeAddrList != nil {
		m.beforeAddrList(name, m.addrListCalls[name])
	}
	m.listCalls = append(m.listCalls, "addresses:"+name)
	if err := m.addrListErr[name]; err != nil {
		return nil, err
	}
	var addresses []vnetlink.Addr
	for _, address := range m.addresses[name] {
		if family == vnetlink.FAMILY_ALL ||
			(family == vnetlink.FAMILY_V4 && address.IP.To4() != nil) ||
			(family == vnetlink.FAMILY_V6 && address.IP.To4() == nil) {
			addresses = append(addresses, address)
		}
	}
	return addresses, nil
}

func (m *fakeBackend) AddrReplace(link vnetlink.Link, address *vnetlink.Addr) error {
	name := link.Attrs().Name
	wanted := addressString(*address)
	if err := m.addrReplaceErr[name+"/"+wanted]; err != nil {
		return err
	}
	replacement := *address
	for _, existing := range m.addresses[name] {
		if existing.IP.To4() == nil && existing.IP.Equal(address.IP) {
			m.replacedAddresses = append(m.replacedAddresses, wanted)
			return nil
		}
		if addressString(existing) == wanted {
			replacement.Flags = existing.Flags
			break
		}
		if sameIPv4Subnet(existing, replacement) {
			replacement.Flags |= unix.IFA_F_SECONDARY
		}
	}
	current := m.addresses[name][:0]
	for _, existing := range m.addresses[name] {
		if addressString(existing) != wanted {
			current = append(current, existing)
		}
	}
	m.addresses[name] = append(current, replacement)
	m.replacedAddresses = append(m.replacedAddresses, wanted)
	return nil
}

func (m *fakeBackend) AddrDel(link vnetlink.Link, address *vnetlink.Addr) error {
	name := link.Attrs().Name
	deleted := addressString(*address)
	current := m.addresses[name][:0]
	for _, existing := range m.addresses[name] {
		secondary := address.Flags&unix.IFA_F_SECONDARY == 0 &&
			existing.Flags&unix.IFA_F_SECONDARY != 0 && sameIPv4Subnet(*address, existing)
		if addressString(existing) != deleted && !secondary {
			current = append(current, existing)
		}
	}
	m.addresses[name] = current
	m.deletedAddresses = append(m.deletedAddresses, deleted)
	return nil
}

// sameIPv4Subnet identifies addresses sharing the kernel's IPv4 primary group.
func sameIPv4Subnet(first, second vnetlink.Addr) bool {
	return first.IPNet != nil && second.IPNet != nil &&
		first.IP.To4() != nil && second.IP.To4() != nil &&
		bytes.Equal(first.Mask, second.Mask) && first.IPNet.Contains(second.IP)
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
