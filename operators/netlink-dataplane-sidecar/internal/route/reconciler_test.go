package route_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/route"
)

const (
	testTable    = 100
	testProtocol = 242
	testPriority = 4242
)

// Test_Reconciler_RejectsUnmanagedInterfaceBeforeNetlinkAccess verifies that
// routes cannot escape the links owned by the supplied netplan state.
func Test_Reconciler_RejectsUnmanagedInterfaceBeforeNetlinkAccess(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["management0"] = testLink("management0", 90)
	reconciler := newTestReconciler(t, backend)

	err := reconciler.Apply(
		t.Context(),
		[]route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "management0")},
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
	)
	require.ErrorContains(t, err, `interface "management0" is not managed`)
	require.Empty(t, backend.linkCalls)
	require.Empty(t, backend.listCalls)
	require.Empty(t, backend.addAttempts)
	require.Empty(t, backend.deleted)
}

// Test_Reconciler_GroupsECMPAndResolvesInterfacesEachPass verifies that equal
// prefixes become deterministic multipath routes using fresh link indexes.
func Test_Reconciler_GroupsECMPAndResolvesInterfacesEachPass(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	backend.links["tenant.100"] = testLink("tenant.100", 20)
	reconciler := newTestReconciler(t, backend)
	routes := []route.Route{
		testRoute("192.0.2.0/24", "192.0.2.2", "tenant.100"),
		testRoute("192.0.2.0/24", "192.0.2.1", "tenant.100"),
		testRoute("192.0.2.0/24", "192.0.2.1", "kni0"),
	}
	state := netplan.State{Links: []netplan.Link{
		{Name: "kni0"},
		{Name: "tenant.100", Parent: "kni0", VLANID: 100},
	}}

	require.NoError(t, reconciler.Apply(t.Context(), routes, state))
	require.Len(t, backend.added, 1)
	require.Equal(t, testPriority, backend.added[0].Priority)
	require.Equal(t, []int{10, 20, 20}, multipathIndexes(backend.added[0]))
	require.Equal(t, []string{
		"192.0.2.1",
		"192.0.2.1",
		"192.0.2.2",
	}, multipathGateways(backend.added[0]))

	backend.allRoutes = nil
	backend.links["kni0"] = testLink("kni0", 110)
	backend.links["tenant.100"] = testLink("tenant.100", 120)
	require.NoError(t, reconciler.Apply(t.Context(), routes, state))
	require.Len(t, backend.added, 2)
	require.Equal(t, testPriority, backend.added[1].Priority)
	require.Equal(t, []int{110, 120, 120}, multipathIndexes(backend.added[1]))
	require.Empty(t, backend.deleted)
	require.Equal(t, []string{
		"kni0",
		"tenant.100",
		"kni0",
		"tenant.100",
		"kni0",
		"tenant.100",
		"kni0",
		"tenant.100",
	}, backend.linkCalls)

	backend.allRoutes = []vnetlink.Route{backend.added[1]}
	require.NoError(t, reconciler.Apply(t.Context(), routes, state))
	require.Len(t, backend.added, 2)
	require.Empty(t, backend.deleted)
	require.Len(t, backend.listCalls, 3, "each pass uses one complete table dump")
}

// Test_Reconciler_ForeignCollisionPreventsMutation verifies that an existing
// foreign destination blocks every desired update and stale cleanup.
func Test_Reconciler_ForeignCollisionPreventsMutation(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	foreign := testKernelRoute("192.0.2.0/24", testTable, 99, testPriority)
	current := testKernelRoute("192.0.1.0/24", testTable, testProtocol, testPriority)
	stale := testKernelRoute(
		"198.51.100.0/24",
		testTable,
		testProtocol,
		testPriority,
	)
	backend.allRoutes = []vnetlink.Route{current, stale, foreign}
	reconciler := newTestReconciler(t, backend)

	err := reconciler.Apply(
		t.Context(),
		[]route.Route{
			testRoute("192.0.1.0/24", "192.0.2.1", "kni0"),
			testRoute("192.0.2.0/24", "192.0.2.1", "kni0"),
		},
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
	)
	require.ErrorContains(t, err, "collides with foreign route")
	require.Len(t, backend.listCalls, 1)
	require.Empty(t, backend.addAttempts)
	require.Empty(t, backend.deleted)
}

// Test_Reconciler_ForeignOtherPriorityIsPreserved verifies that ownership and
// collision checks do not claim a same-protocol route at another priority.
func Test_Reconciler_ForeignOtherPriorityIsPreserved(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	foreign := testKernelRoute(
		"192.0.2.0/24",
		testTable,
		testProtocol,
		testPriority+1,
	)
	stale := testKernelRoute(
		"198.51.100.0/24",
		testTable,
		testProtocol,
		testPriority,
	)
	backend.allRoutes = []vnetlink.Route{foreign, stale}
	reconciler := newTestReconciler(t, backend)

	require.NoError(t, reconciler.Apply(
		t.Context(),
		[]route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")},
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
	))
	require.Len(t, backend.added, 1)
	require.Len(t, backend.deleted, 1)
	require.Equal(t, capturedPrefix(stale), capturedPrefix(backend.deleted[0]))
	require.Equal(t, testPriority, backend.deleted[0].Priority)
}

// Test_Reconciler_RouteAddRaceSkipsCleanup verifies that an exclusive-add
// collision after preflight returns the kernel error and preserves stale
// state.
func Test_Reconciler_RouteAddRaceSkipsCleanup(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	stale := testKernelRoute(
		"198.51.100.0/24",
		testTable,
		testProtocol,
		testPriority,
	)
	backend.allRoutes = []vnetlink.Route{stale}
	backend.addErrorAt = 0
	backend.addError = unix.EEXIST
	reconciler := newTestReconciler(t, backend)

	err := reconciler.Apply(
		t.Context(),
		[]route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")},
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
	)
	require.ErrorIs(t, err, unix.EEXIST)
	require.ErrorContains(t, err, "add destination")
	require.Len(t, backend.addAttempts, 1)
	require.Empty(t, backend.added)
	require.Empty(t, backend.deleted)
}

// Test_Reconciler_ReplacesOwnedRouteWithUnexpectedAttributes verifies that
// drift in source, metrics, flags, or next-hop encoding restores canonical routes.
func Test_Reconciler_ReplacesOwnedRouteWithUnexpectedAttributes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*vnetlink.Route)
	}{
		{
			name: "preferred source",
			mutate: func(kernelRoute *vnetlink.Route) {
				kernelRoute.Src = net.ParseIP("192.0.2.99").To4()
			},
		},
		{
			name: "MTU metric",
			mutate: func(kernelRoute *vnetlink.Route) {
				kernelRoute.MTU = 1400
			},
		},
		{
			name: "multipath flags",
			mutate: func(kernelRoute *vnetlink.Route) {
				kernelRoute.MultiPath[0].Flags = int(vnetlink.FLAG_ONLINK)
			},
		},
		{
			name: "route flags",
			mutate: func(kernelRoute *vnetlink.Route) {
				kernelRoute.Flags = unix.RTM_F_EQUALIZE
			},
		},
		{
			name: "singleton onlink flag",
			mutate: func(kernelRoute *vnetlink.Route) {
				kernelRoute.LinkIndex = kernelRoute.MultiPath[0].LinkIndex
				kernelRoute.Gw = kernelRoute.MultiPath[0].Gw
				kernelRoute.MultiPath = nil
				kernelRoute.Flags = int(vnetlink.FLAG_ONLINK)
			},
		},
		{
			name: "multipath via",
			mutate: func(kernelRoute *vnetlink.Route) {
				kernelRoute.MultiPath[0].Via = &vnetlink.Via{
					AddrFamily: vnetlink.FAMILY_V4,
					Addr:       net.ParseIP("192.0.2.254").To4(),
				}
			},
		},
		{
			name: "mixed outer and multipath nexthop",
			mutate: func(kernelRoute *vnetlink.Route) {
				kernelRoute.LinkIndex = 10
				kernelRoute.Gw = net.ParseIP("192.0.2.1").To4()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeRouteBackend()
			backend.links["kni0"] = testLink("kni0", 10)
			reconciler := newTestReconciler(t, backend)
			routes := []route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")}
			state := netplan.State{Links: []netplan.Link{{Name: "kni0"}}}
			require.NoError(t, reconciler.Apply(t.Context(), routes, state))

			canonical := backend.added[0]
			drifted := cloneKernelRoute(canonical)
			test.mutate(&drifted)
			backend.allRoutes = []vnetlink.Route{drifted}
			backend.added = nil
			backend.deleted = nil

			require.NoError(t, reconciler.Apply(t.Context(), routes, state))
			require.Equal(t, []vnetlink.Route{drifted}, backend.deleted)
			require.Equal(t, []vnetlink.Route{canonical}, backend.added)
		})
	}
}

// Test_Reconciler_EquivalentRouteRevalidatesEachInterfaceOnce verifies that an
// unchanged route requires only one lookup of its output interface per pass.
func Test_Reconciler_EquivalentRouteRevalidatesEachInterfaceOnce(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	reconciler := newTestReconciler(t, backend)
	routes := []route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")}
	state := netplan.State{Links: []netplan.Link{{Name: "kni0"}}}
	require.NoError(t, reconciler.Apply(t.Context(), routes, state))

	canonical := backend.added[0]
	backend.allRoutes = []vnetlink.Route{canonical}
	backend.linkByNameCalls = map[string]int{}
	backend.linkCalls = nil

	require.NoError(t, reconciler.Apply(t.Context(), routes, state))
	require.Equal(t, 1, backend.linkByNameCalls["kni0"])
	require.Equal(t, []string{"kni0"}, backend.linkCalls)
}

// Test_Reconciler_IgnoresKernelMaintainedRouteFlags verifies that runtime
// offload and reachability flags do not trigger route deletion or addition.
func Test_Reconciler_IgnoresKernelMaintainedRouteFlags(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	reconciler := newTestReconciler(t, backend)
	routes := []route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")}
	state := netplan.State{Links: []netplan.Link{{Name: "kni0"}}}
	require.NoError(t, reconciler.Apply(t.Context(), routes, state))

	kernelRoute := cloneKernelRoute(backend.added[0])
	kernelRoute.Flags = unix.RTM_F_CLONED |
		unix.RTM_F_OFFLOAD |
		unix.RTM_F_TRAP |
		unix.RTM_F_OFFLOAD_FAILED
	kernelNexthopFlags := unix.RTNH_F_DEAD |
		unix.RTNH_F_OFFLOAD |
		unix.RTNH_F_LINKDOWN |
		unix.RTNH_F_UNRESOLVED |
		unix.RTNH_F_TRAP
	kernelRoute.MultiPath[0].Flags = kernelNexthopFlags
	backend.allRoutes = []vnetlink.Route{kernelRoute}
	backend.added = nil
	backend.deleted = nil

	require.NoError(t, reconciler.Apply(t.Context(), routes, state))
	require.Empty(t, backend.added)
	require.Empty(t, backend.deleted)
}

// Test_Reconciler_RollsBackChangedRouteWhenReplacementAddFails verifies that
// failure to add the desired route restores the exact route deleted before it.
func Test_Reconciler_RollsBackChangedRouteWhenReplacementAddFails(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	current := testKernelRoute("192.0.2.0/24", testTable, testProtocol, testPriority)
	backend.allRoutes = []vnetlink.Route{current}
	backend.addErrorAt = 0
	backend.addError = unix.EIO
	reconciler := newTestReconciler(t, backend)

	err := reconciler.Apply(
		t.Context(),
		[]route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")},
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
	)

	require.ErrorIs(t, err, unix.EIO)
	require.Len(t, backend.deleted, 1)
	require.Len(t, backend.addAttempts, 2)
	require.Len(t, backend.added, 1)
	require.Equal(t, current, backend.added[0])
}

// Test_Reconciler_RollsBackRouteOnDifferentOldInterface verifies that a failed
// egress change restores the old next hop and interface rather than the new one.
func Test_Reconciler_RollsBackRouteOnDifferentOldInterface(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni-old"] = testLink("kni-old", 10)
	backend.links["kni-new"] = testLink("kni-new", 20)
	current := testKernelRoute("192.0.2.0/24", testTable, testProtocol, testPriority)
	current.MultiPath = []*vnetlink.NexthopInfo{{
		LinkIndex: 10,
		Gw:        net.ParseIP("192.0.2.9").To4(),
	}}
	backend.allRoutes = []vnetlink.Route{current}
	backend.addErrorAt = 0
	backend.addError = unix.EIO
	reconciler := newTestReconciler(t, backend)

	err := reconciler.Apply(
		t.Context(),
		[]route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni-new")},
		netplan.State{Links: []netplan.Link{{Name: "kni-old"}, {Name: "kni-new"}}},
	)

	require.ErrorIs(t, err, unix.EIO)
	require.Len(t, backend.deleted, 1)
	require.Len(t, backend.addAttempts, 2)
	require.Equal(t, current, backend.added[0])
}

// Test_Reconciler_RollsBackEarlierDeleteWhenLaterDeleteFails verifies that
// failure removing a second conflicting route restores the first removed variant.
func Test_Reconciler_RollsBackEarlierDeleteWhenLaterDeleteFails(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	first := testKernelRoute("192.0.2.0/24", testTable, testProtocol, testPriority)
	first.Tos = 8
	second := first
	second.Tos = 16
	backend.allRoutes = []vnetlink.Route{first, second}
	backend.deleteErrorAt = 1
	backend.deleteError = unix.ESRCH
	reconciler := newTestReconciler(t, backend)

	err := reconciler.Apply(
		t.Context(),
		[]route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")},
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
	)

	require.ErrorIs(t, err, unix.ESRCH)
	require.Len(t, backend.deleted, 1)
	require.Len(t, backend.added, 1)
	require.Equal(t, first, backend.added[0])
}

// Test_Reconciler_SamePrefixVariantsAreReplacedSafely verifies that every
// owned conflicting key is removed before the exclusive canonical add.
func Test_Reconciler_SamePrefixVariantsAreReplacedSafely(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	otherPriority := testKernelRoute(
		"192.0.2.0/24",
		testTable,
		testProtocol,
		testPriority+1,
	)
	otherTOS := testKernelRoute(
		"192.0.2.0/24",
		testTable,
		testProtocol,
		testPriority,
	)
	otherTOS.Tos = 16
	otherType := testKernelRoute(
		"192.0.2.0/24",
		testTable,
		testProtocol,
		testPriority,
	)
	otherType.Type = unix.RTN_BLACKHOLE
	backend.allRoutes = []vnetlink.Route{otherPriority, otherTOS, otherType}
	reconciler := newTestReconciler(t, backend)

	require.NoError(t, reconciler.Apply(
		t.Context(),
		[]route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")},
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
	))
	require.Len(t, backend.added, 1)
	require.Equal(t, testPriority, backend.added[0].Priority)
	require.Zero(t, backend.added[0].Tos)
	require.Equal(t, unix.RTN_UNICAST, backend.added[0].Type)
	require.Len(t, backend.deleted, 2)
	for _, deleted := range backend.deleted {
		require.Equal(t, testPriority, deleted.Priority)
		require.True(t, deleted.Tos != 0 || deleted.Type != unix.RTN_UNICAST)
	}
	require.Equal(t, []string{
		"link:kni0",
		"dump:all",
		"link:kni0",
		"delete",
		"delete",
		"link:kni0",
		"add",
	}, backend.operations)
}

// Test_Reconciler_EmptySnapshotDeletesOnlyExactOwner verifies that an empty
// desired state cannot delete routes outside the full ownership tuple.
func Test_Reconciler_EmptySnapshotDeletesOnlyExactOwner(t *testing.T) {
	backend := newFakeRouteBackend()
	owned := testKernelRoute(
		"192.0.2.0/24",
		testTable,
		testProtocol,
		testPriority,
	)
	otherPriority := testKernelRoute(
		"198.51.100.0/24",
		testTable,
		testProtocol,
		testPriority+1,
	)
	foreign := testKernelRoute("203.0.113.0/24", testTable, 99, testPriority)
	backend.allRoutes = []vnetlink.Route{owned, otherPriority, foreign}
	reconciler := newTestReconciler(t, backend)

	require.NoError(t, reconciler.Apply(t.Context(), nil, netplan.State{}))
	require.Empty(t, backend.addAttempts)
	require.Len(t, backend.deleted, 1)
	require.Equal(t, owned, backend.deleted[0])
}

// Test_Reconciler_DeletesOwnedDefaultRouteWithExplicitZeroPrefix verifies that
// an implicit default destination is made explicit in the kernel delete request.
func Test_Reconciler_DeletesOwnedDefaultRouteWithExplicitZeroPrefix(t *testing.T) {
	backend := newFakeRouteBackend()
	owned := testKernelRoute("0.0.0.0/0", testTable, testProtocol, testPriority)
	require.Nil(t, owned.Dst)
	backend.allRoutes = []vnetlink.Route{owned}
	reconciler := newTestReconciler(t, backend)

	require.NoError(t, reconciler.Apply(t.Context(), nil, netplan.State{}))
	require.Len(t, backend.deleted, 1)
	require.NotNil(t, backend.deleted[0].Dst)
	require.Equal(t, netip.MustParsePrefix("0.0.0.0/0"), capturedPrefix(backend.deleted[0]))
}

// Test_Reconciler_ChangesOwnedAndDeletesOnlyStaleOwned verifies that cleanup
// starts after desired exclusive-add and excludes every foreign route.
func Test_Reconciler_ChangesOwnedAndDeletesOnlyStaleOwned(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	current := testKernelRoute(
		"192.0.2.0/24",
		testTable,
		testProtocol,
		testPriority,
	)
	stale := testKernelRoute(
		"198.51.100.0/24",
		testTable,
		testProtocol,
		testPriority,
	)
	variant := current
	variant.Tos = 8
	foreign := testKernelRoute("203.0.113.0/24", testTable, 99, testPriority)
	backend.allRoutes = []vnetlink.Route{foreign, stale, variant, current}
	reconciler := newTestReconciler(t, backend)

	require.NoError(t, reconciler.Apply(
		t.Context(),
		[]route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")},
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
	))
	require.Len(t, backend.added, 1)
	require.Equal(t, netip.MustParsePrefix("192.0.2.0/24"), capturedPrefix(backend.added[0]))
	require.Equal(t, testTable, backend.added[0].Table)
	require.Equal(t, vnetlink.RouteProtocol(testProtocol), backend.added[0].Protocol)
	require.Equal(t, testPriority, backend.added[0].Priority)
	require.Len(t, backend.deleted, 3)
	require.Equal(t, netip.MustParsePrefix("192.0.2.0/24"), capturedPrefix(backend.deleted[0]))
	require.Equal(t, 8, backend.deleted[0].Tos)
	require.Zero(t, backend.deleted[1].Tos)
	require.Equal(t, netip.MustParsePrefix("198.51.100.0/24"), capturedPrefix(backend.deleted[2]))
	require.Equal(t, []string{
		"link:kni0",
		"dump:all",
		"link:kni0",
		"delete",
		"delete",
		"link:kni0",
		"add",
		"delete",
	}, backend.operations)
	require.Equal(t, []routeListCall{
		{
			family: vnetlink.FAMILY_ALL,
			filter: vnetlink.Route{Table: testTable},
			mask:   vnetlink.RT_FILTER_TABLE,
		},
	}, backend.listCalls)
}

// Test_Reconciler_DumpFailurePreventsMutation verifies that partial results
// from an interrupted or failed table dump are discarded before any mutation.
func Test_Reconciler_DumpFailurePreventsMutation(t *testing.T) {
	dumpError := errors.New("dump failed")
	tests := []struct {
		name        string
		wantedError error
	}{
		{
			name:        "interrupted table dump",
			wantedError: vnetlink.ErrDumpInterrupted,
		},
		{
			name:        "failed table dump",
			wantedError: dumpError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeRouteBackend()
			backend.links["kni0"] = testLink("kni0", 10)
			backend.allRoutes = []vnetlink.Route{
				testKernelRoute(
					"192.0.2.0/24",
					testTable,
					testProtocol,
					testPriority,
				),
				testKernelRoute(
					"198.51.100.0/24",
					testTable,
					testProtocol,
					testPriority,
				),
			}
			backend.allRoutesError = test.wantedError
			reconciler := newTestReconciler(t, backend)

			err := reconciler.Apply(
				t.Context(),
				[]route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")},
				netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
			)
			require.ErrorIs(t, err, test.wantedError)
			require.Len(t, backend.listCalls, 1)
			require.Empty(t, backend.addAttempts)
			require.Empty(t, backend.deleted)
		})
	}
}

// Test_Reconciler_LinkReplacementAfterDumpPreventsRouteMutation verifies that
// stale interface identity detected after route dumps blocks both add and delete.
func Test_Reconciler_LinkReplacementAfterDumpPreventsRouteMutation(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	current := testKernelRoute("192.0.2.0/24", testTable, testProtocol, testPriority)
	backend.allRoutes = []vnetlink.Route{current}
	backend.beforeLinkByName = func(name string, call int) {
		if name == "kni0" && call == 2 {
			backend.links[name] = testLink(name, 11)
		}
	}
	reconciler := newTestReconciler(t, backend)

	err := reconciler.Apply(
		t.Context(),
		[]route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")},
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
	)

	require.ErrorContains(t, err, "link identity changed during reconciliation")
	require.Empty(t, backend.added)
	require.Empty(t, backend.deleted)
}

// Test_Reconciler_LinkReplacementDuringDeletePreventsAddAndUnsafeRollback verifies
// that a changed output link blocks both the desired add and unsafe restoration.
func Test_Reconciler_LinkReplacementDuringDeletePreventsAddAndUnsafeRollback(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	current := testKernelRoute("192.0.2.0/24", testTable, testProtocol, testPriority)
	current.MultiPath = []*vnetlink.NexthopInfo{{
		LinkIndex: 10,
		Gw:        net.ParseIP("192.0.2.9").To4(),
	}}
	backend.allRoutes = []vnetlink.Route{current}
	replaced := false
	backend.afterMutation = func() {
		if !replaced {
			backend.links["kni0"] = testLink("kni0", 11)
			replaced = true
		}
	}
	reconciler := newTestReconciler(t, backend)

	err := reconciler.Apply(
		t.Context(),
		[]route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")},
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
	)

	require.ErrorContains(t, err, "link identity changed during reconciliation")
	require.ErrorContains(t, err, "cannot safely restore deleted route")
	require.Len(t, backend.deleted, 1)
	require.Empty(t, backend.addAttempts)
}

// Test_Reconciler_ChangedRouteDeleteFailureRollsBackEarlierChanges verifies
// that a later destination failure restores the complete pre-apply state.
func Test_Reconciler_ChangedRouteDeleteFailureRollsBackEarlierChanges(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	first := testKernelRoute(
		"192.0.2.0/24",
		testTable,
		testProtocol,
		testPriority,
	)
	second := testKernelRoute(
		"198.51.100.0/24",
		testTable,
		testProtocol,
		testPriority,
	)
	stale := testKernelRoute(
		"203.0.113.0/24",
		testTable,
		testProtocol,
		testPriority,
	)
	backend.allRoutes = []vnetlink.Route{first, second, stale}
	backend.deleteErrorAt = 1
	backend.deleteError = unix.ESRCH
	reconciler := newTestReconciler(t, backend)

	err := reconciler.Apply(
		t.Context(),
		[]route.Route{
			testRoute("192.0.2.0/24", "192.0.2.1", "kni0"),
			testRoute("198.51.100.0/24", "198.51.100.1", "kni0"),
		},
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
	)
	require.ErrorIs(t, err, unix.ESRCH)
	require.ErrorContains(t, err, "delete changed destination")
	require.Len(t, backend.added, 2)
	require.Equal(t, first, backend.added[1])
	require.Len(t, backend.deleteAttempts, 3)
	require.Len(t, backend.deleted, 2)
	require.Equal(t, backend.added[0], backend.deleted[1])
}

// Test_Reconciler_StaleDeleteFailureRollsBackWholePass verifies that cleanup
// failure restores earlier stale deletions and undoes the desired route change.
func Test_Reconciler_StaleDeleteFailureRollsBackWholePass(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	current := testKernelRoute("192.0.2.0/24", testTable, testProtocol, testPriority)
	firstStale := testKernelRoute("198.51.100.0/24", testTable, testProtocol, testPriority)
	secondStale := testKernelRoute("203.0.113.0/24", testTable, testProtocol, testPriority)
	backend.allRoutes = []vnetlink.Route{current, firstStale, secondStale}
	backend.deleteErrorAt = 2
	backend.deleteError = unix.EIO
	reconciler := newTestReconciler(t, backend)

	err := reconciler.Apply(
		t.Context(),
		[]route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")},
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
	)
	require.ErrorIs(t, err, unix.EIO)
	require.ErrorContains(t, err, "delete stale destination")
	require.Len(t, backend.added, 3)
	require.Equal(t, firstStale, backend.added[1])
	require.Equal(t, current, backend.added[2])
	require.Len(t, backend.deleteAttempts, 4)
	require.Len(t, backend.deleted, 3)
	require.Equal(t, backend.added[0], backend.deleted[2])
}

// Test_Reconciler_HandlesBothFamiliesAndDefaultPrefixes verifies that IPv4,
// IPv6, and zero-length destinations retain their family and gateway data.
func Test_Reconciler_HandlesBothFamiliesAndDefaultPrefixes(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	reconciler := newTestReconciler(t, backend)
	routes := []route.Route{
		testRoute("2001:db8::/64", "2001:db8::1", "kni0"),
		testRoute("::/0", "2001:db8::ffff", "kni0"),
		testRoute("192.0.2.0/24", "192.0.2.1", "kni0"),
		testRoute("0.0.0.0/0", "192.0.2.254", "kni0"),
	}

	require.NoError(t, reconciler.Apply(
		t.Context(),
		routes,
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
	))
	require.Len(t, backend.added, 4)
	families := map[netip.Prefix]int{}
	gateways := map[netip.Prefix]string{}
	for _, kernelRoute := range backend.added {
		prefix := capturedPrefix(kernelRoute)
		families[prefix] = kernelRoute.Family
		gateways[prefix] = kernelRoute.MultiPath[0].Gw.String()
		require.Equal(t, testPriority, kernelRoute.Priority)
	}
	require.Equal(t, vnetlink.FAMILY_V4, families[netip.MustParsePrefix("0.0.0.0/0")])
	require.Equal(t, vnetlink.FAMILY_V4, families[netip.MustParsePrefix("192.0.2.0/24")])
	require.Equal(t, vnetlink.FAMILY_V6, families[netip.MustParsePrefix("::/0")])
	require.Equal(t, vnetlink.FAMILY_V6, families[netip.MustParsePrefix("2001:db8::/64")])
	require.Equal(t, "192.0.2.254", gateways[netip.MustParsePrefix("0.0.0.0/0")])
	require.Equal(t, "2001:db8::ffff", gateways[netip.MustParsePrefix("::/0")])
}

// Test_Reconciler_ContextCancellationRollsBackBeforeCleanup verifies that a
// cancellation between desired routes removes changes from the interrupted pass.
func Test_Reconciler_ContextCancellationRollsBackBeforeCleanup(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	stale := testKernelRoute(
		"203.0.113.0/24",
		testTable,
		testProtocol,
		testPriority,
	)
	backend.allRoutes = []vnetlink.Route{stale}
	ctx, cancel := context.WithCancel(t.Context())
	backend.afterMutation = cancel
	reconciler := newTestReconciler(t, backend)

	err := reconciler.Apply(
		ctx,
		[]route.Route{
			testRoute("192.0.2.0/24", "192.0.2.1", "kni0"),
			testRoute("198.51.100.0/24", "198.51.100.1", "kni0"),
		},
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, backend.added, 1)
	require.Len(t, backend.deleted, 1)
	require.Equal(t, backend.added[0], backend.deleted[0])
}

// Test_Reconciler_CancellationDuringChangeRestoresOriginalRoute verifies that
// cancellation after deletion still permits restoring the original kernel route.
func Test_Reconciler_CancellationDuringChangeRestoresOriginalRoute(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	current := testKernelRoute(
		"192.0.2.0/24",
		testTable,
		testProtocol,
		testPriority,
	)
	backend.allRoutes = []vnetlink.Route{current}
	ctx, cancel := context.WithCancel(t.Context())
	backend.afterMutation = cancel
	reconciler := newTestReconciler(t, backend)

	err := reconciler.Apply(
		ctx,
		[]route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")},
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, backend.deleted, 1)
	require.Equal(t, []vnetlink.Route{current}, backend.added)
}

// Test_Reconciler_CancellationStopsRemainingChangedRouteDeletes verifies that
// cancellation after the first variant deletion restores it without deleting more.
func Test_Reconciler_CancellationStopsRemainingChangedRouteDeletes(t *testing.T) {
	backend := newFakeRouteBackend()
	backend.links["kni0"] = testLink("kni0", 10)
	first := testKernelRoute("192.0.2.0/24", testTable, testProtocol, testPriority)
	second := cloneKernelRoute(first)
	second.Realm = 42
	backend.allRoutes = []vnetlink.Route{first, second}
	ctx, cancel := context.WithCancel(t.Context())
	backend.afterMutation = cancel
	reconciler := newTestReconciler(t, backend)

	err := reconciler.Apply(
		ctx,
		[]route.Route{testRoute("192.0.2.0/24", "192.0.2.1", "kni0")},
		netplan.State{Links: []netplan.Link{{Name: "kni0"}}},
	)
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, backend.deleteAttempts, 1)
	require.Equal(t, []vnetlink.Route{first}, backend.added)
}

// Test_Reconciler_ConstructorValidatesOwnership verifies that invalid tables,
// protocols, and dependencies cannot create a route owner.
func Test_Reconciler_ConstructorValidatesOwnership(t *testing.T) {
	backend := newFakeRouteBackend()
	tests := []struct {
		name          string
		backend       route.Backend
		config        route.ReconcilerConfig
		errorContains string
	}{
		{
			name: "nil backend",
			config: route.ReconcilerConfig{
				Table:    testTable,
				Protocol: testProtocol,
				Priority: testPriority,
			},
			errorContains: "backend is nil",
		},
		{
			name:    "zero table",
			backend: backend,
			config: route.ReconcilerConfig{
				Protocol: testProtocol,
				Priority: testPriority,
			},
			errorContains: "table must be positive",
		},
		{
			name:    "zero protocol",
			backend: backend,
			config: route.ReconcilerConfig{
				Table:    testTable,
				Priority: testPriority,
			},
			errorContains: "reserved for a shared route origin",
		},
		{
			name:    "protocol above byte range",
			backend: backend,
			config: route.ReconcilerConfig{
				Table:    testTable,
				Protocol: 256,
				Priority: testPriority,
			},
			errorContains: "protocol must be within",
		},
		{
			name:    "zero priority",
			backend: backend,
			config: route.ReconcilerConfig{
				Table:    testTable,
				Protocol: testProtocol,
			},
			errorContains: "priority must be positive",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reconciler, err := route.NewReconciler(test.backend, test.config)
			require.ErrorContains(t, err, test.errorContains)
			require.Nil(t, reconciler)
		})
	}
}

// Test_Reconciler_ConstructorRejectsSharedProtocols verifies that route
// origins assigned to common kernel and routing daemons cannot become owners.
func Test_Reconciler_ConstructorRejectsSharedProtocols(t *testing.T) {
	tests := []struct {
		name     string
		protocol int
	}{
		{name: "unspecified", protocol: unix.RTPROT_UNSPEC},
		{name: "redirect", protocol: unix.RTPROT_REDIRECT},
		{name: "kernel", protocol: unix.RTPROT_KERNEL},
		{name: "boot", protocol: unix.RTPROT_BOOT},
		{name: "static", protocol: unix.RTPROT_STATIC},
		{name: "gated", protocol: unix.RTPROT_GATED},
		{name: "router advertisement", protocol: unix.RTPROT_RA},
		{name: "mrt", protocol: unix.RTPROT_MRT},
		{name: "zebra", protocol: unix.RTPROT_ZEBRA},
		{name: "bird", protocol: unix.RTPROT_BIRD},
		{name: "dnrouted", protocol: unix.RTPROT_DNROUTED},
		{name: "xorp", protocol: unix.RTPROT_XORP},
		{name: "ntk", protocol: unix.RTPROT_NTK},
		{name: "dhcp", protocol: unix.RTPROT_DHCP},
		{name: "mrouted", protocol: unix.RTPROT_MROUTED},
		{name: "keepalived", protocol: unix.RTPROT_KEEPALIVED},
		{name: "babel", protocol: unix.RTPROT_BABEL},
		{name: "ovn", protocol: unix.RTPROT_OVN},
		{name: "openr", protocol: unix.RTPROT_OPENR},
		{name: "bgp", protocol: unix.RTPROT_BGP},
		{name: "isis", protocol: unix.RTPROT_ISIS},
		{name: "ospf", protocol: unix.RTPROT_OSPF},
		{name: "rip", protocol: unix.RTPROT_RIP},
		{name: "eigrp", protocol: unix.RTPROT_EIGRP},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reconciler, err := route.NewReconciler(
				newFakeRouteBackend(),
				route.ReconcilerConfig{
					Table:    testTable,
					Protocol: test.protocol,
					Priority: testPriority,
				},
			)
			require.ErrorContains(t, err, "reserved for a shared route origin")
			require.Nil(t, reconciler)
		})
	}
}

// Test_Reconciler_ConstructorAllowsUnassignedProtocol verifies that a private
// protocol identifier can be combined with a dedicated positive priority.
func Test_Reconciler_ConstructorAllowsUnassignedProtocol(t *testing.T) {
	reconciler, err := route.NewReconciler(
		newFakeRouteBackend(),
		route.ReconcilerConfig{
			Table:    testTable,
			Protocol: 242,
			Priority: testPriority,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, reconciler)
}

// Test_Reconciler_ConstructorRejectsValuesThatOverflowNetlink verifies that
// out-of-range table and priority values cannot construct a kernel route owner.
func Test_Reconciler_ConstructorRejectsValuesThatOverflowNetlink(t *testing.T) {
	if ^uint(0) == uint(^uint32(0)) {
		t.Skip("int cannot represent values above uint32 on this platform")
	}
	overflowValue := route.MaxKernelRouteValue + 1
	overflow := int(overflowValue)

	reconciler, err := route.NewReconciler(newFakeRouteBackend(), route.ReconcilerConfig{
		Table:    overflow,
		Protocol: testProtocol,
		Priority: testPriority,
	})
	require.ErrorContains(t, err, "table must be within")
	require.Nil(t, reconciler)

	reconciler, err = route.NewReconciler(newFakeRouteBackend(), route.ReconcilerConfig{
		Table:    testTable,
		Protocol: testProtocol,
		Priority: overflow,
	})
	require.ErrorContains(t, err, "priority must be within")
	require.Nil(t, reconciler)
}

type routeListCall struct {
	family int
	filter vnetlink.Route
	mask   uint64
}

type fakeRouteBackend struct {
	links            map[string]vnetlink.Link
	linkErrors       map[string]error
	allRoutes        []vnetlink.Route
	allRoutesError   error
	addErrorAt       int
	addError         error
	deleteErrorAt    int
	deleteError      error
	afterMutation    func()
	beforeLinkByName func(string, int)
	linkByNameCalls  map[string]int
	linkCalls        []string
	listCalls        []routeListCall
	addAttempts      []vnetlink.Route
	added            []vnetlink.Route
	deleteAttempts   []vnetlink.Route
	deleted          []vnetlink.Route
	operations       []string
}

// newFakeRouteBackend returns an empty recorder with no mutation fault.
func newFakeRouteBackend() *fakeRouteBackend {
	return &fakeRouteBackend{
		links:           map[string]vnetlink.Link{},
		linkErrors:      map[string]error{},
		linkByNameCalls: map[string]int{},
		addErrorAt:      -1,
		deleteErrorAt:   -1,
	}
}

// LinkByName returns the current link fixture and records every fresh lookup.
func (m *fakeRouteBackend) LinkByName(name string) (vnetlink.Link, error) {
	m.linkByNameCalls[name]++
	if m.beforeLinkByName != nil {
		m.beforeLinkByName(name, m.linkByNameCalls[name])
	}
	m.linkCalls = append(m.linkCalls, name)
	m.operations = append(m.operations, "link:"+name)
	if err := m.linkErrors[name]; err != nil {
		return nil, err
	}
	link, found := m.links[name]
	if !found {
		return nil, fmt.Errorf("link %q not found", name)
	}
	return link, nil
}

func (m *fakeRouteBackend) LinkByIndex(index int) (vnetlink.Link, error) {
	for _, link := range m.links {
		if link != nil && link.Attrs() != nil && link.Attrs().Index == index {
			return link, nil
		}
	}
	return nil, fmt.Errorf("link index %d not found", index)
}

// RouteListFiltered returns the complete fixture selected by the filter mask.
func (m *fakeRouteBackend) RouteListFiltered(
	family int,
	filter *vnetlink.Route,
	mask uint64,
) ([]vnetlink.Route, error) {
	m.listCalls = append(m.listCalls, routeListCall{
		family: family,
		filter: cloneKernelRoute(*filter),
		mask:   mask,
	})
	switch mask {
	case vnetlink.RT_FILTER_TABLE:
		m.operations = append(m.operations, "dump:all")
		return cloneKernelRoutes(m.allRoutes), m.allRoutesError
	default:
		return nil, fmt.Errorf("unexpected route filter mask %d", mask)
	}
}

// RouteAdd records exclusive-add attempts and applies the configured failure.
func (m *fakeRouteBackend) RouteAdd(kernelRoute *vnetlink.Route) error {
	m.operations = append(m.operations, "add")
	m.addAttempts = append(m.addAttempts, cloneKernelRoute(*kernelRoute))
	if len(m.addAttempts)-1 == m.addErrorAt {
		return m.addError
	}
	m.added = append(m.added, cloneKernelRoute(*kernelRoute))
	if m.afterMutation != nil {
		m.afterMutation()
	}
	return nil
}

// RouteDel records exact deletion attempts and applies the configured failure.
func (m *fakeRouteBackend) RouteDel(kernelRoute *vnetlink.Route) error {
	m.operations = append(m.operations, "delete")
	m.deleteAttempts = append(m.deleteAttempts, cloneKernelRoute(*kernelRoute))
	if len(m.deleteAttempts)-1 == m.deleteErrorAt {
		return m.deleteError
	}
	m.deleted = append(m.deleted, cloneKernelRoute(*kernelRoute))
	if m.afterMutation != nil {
		m.afterMutation()
	}
	return nil
}

// newTestReconciler returns a reconciler with the package ownership fixture.
func newTestReconciler(t *testing.T, backend route.Backend) *route.Reconciler {
	t.Helper()
	reconciler, err := route.NewReconciler(backend, route.ReconcilerConfig{
		Table:    testTable,
		Protocol: testProtocol,
		Priority: testPriority,
	})
	require.NoError(t, err)
	return reconciler
}

// testLink returns a named dummy link with a usable kernel index.
func testLink(name string, index int) vnetlink.Link {
	return &vnetlink.Dummy{LinkAttrs: vnetlink.LinkAttrs{Name: name, Index: index}}
}

// testKernelRoute returns a canonical dumped route with the supplied owner tag.
func testKernelRoute(prefix string, table, protocol, priority int) vnetlink.Route {
	parsed := netip.MustParsePrefix(prefix)
	family := vnetlink.FAMILY_V6
	addressBits := 128
	if parsed.Addr().Is4() {
		family = vnetlink.FAMILY_V4
		addressBits = 32
	}
	var destination *net.IPNet
	if parsed.Bits() != 0 {
		destination = &net.IPNet{
			IP:   net.IP(parsed.Addr().AsSlice()),
			Mask: net.CIDRMask(parsed.Bits(), addressBits),
		}
	}
	return vnetlink.Route{
		Dst:      destination,
		Family:   family,
		Table:    table,
		Protocol: vnetlink.RouteProtocol(protocol),
		Priority: priority,
		Type:     unix.RTN_UNICAST,
	}
}

// capturedPrefix converts a recorded desired or dumped IP destination.
func capturedPrefix(kernelRoute vnetlink.Route) netip.Prefix {
	if kernelRoute.Dst == nil {
		if kernelRoute.Family == vnetlink.FAMILY_V4 {
			return netip.PrefixFrom(netip.IPv4Unspecified(), 0)
		}
		return netip.PrefixFrom(netip.IPv6Unspecified(), 0)
	}
	ones, bits := kernelRoute.Dst.Mask.Size()
	if bits == 32 {
		return netip.PrefixFrom(
			netip.AddrFrom4([4]byte(kernelRoute.Dst.IP.To4())),
			ones,
		).Masked()
	}
	return netip.PrefixFrom(
		netip.AddrFrom16([16]byte(kernelRoute.Dst.IP.To16())),
		ones,
	).Masked()
}

// multipathIndexes returns output indexes in the emitted nexthop order.
func multipathIndexes(kernelRoute vnetlink.Route) []int {
	indexes := make([]int, 0, len(kernelRoute.MultiPath))
	for _, nexthop := range kernelRoute.MultiPath {
		indexes = append(indexes, nexthop.LinkIndex)
	}
	return indexes
}

// multipathGateways returns gateways in the emitted nexthop order.
func multipathGateways(kernelRoute vnetlink.Route) []string {
	gateways := make([]string, 0, len(kernelRoute.MultiPath))
	for _, nexthop := range kernelRoute.MultiPath {
		gateways = append(gateways, nexthop.Gw.String())
	}
	return gateways
}

// cloneKernelRoutes isolates a fake dump from mutations by the reconciler.
func cloneKernelRoutes(routes []vnetlink.Route) []vnetlink.Route {
	clones := make([]vnetlink.Route, 0, len(routes))
	for _, kernelRoute := range routes {
		clones = append(clones, cloneKernelRoute(kernelRoute))
	}
	return clones
}

// cloneKernelRoute copies destination and multipath reference fields.
func cloneKernelRoute(kernelRoute vnetlink.Route) vnetlink.Route {
	clone := kernelRoute
	if kernelRoute.Dst != nil {
		clone.Dst = &net.IPNet{
			IP:   append(net.IP(nil), kernelRoute.Dst.IP...),
			Mask: append(net.IPMask(nil), kernelRoute.Dst.Mask...),
		}
	}
	if kernelRoute.MultiPath != nil {
		clone.MultiPath = make([]*vnetlink.NexthopInfo, 0, len(kernelRoute.MultiPath))
		for _, nexthop := range kernelRoute.MultiPath {
			copied := *nexthop
			copied.Gw = append(net.IP(nil), nexthop.Gw...)
			clone.MultiPath = append(clone.MultiPath, &copied)
		}
	}
	return clone
}

var _ route.Backend = (*fakeRouteBackend)(nil)
