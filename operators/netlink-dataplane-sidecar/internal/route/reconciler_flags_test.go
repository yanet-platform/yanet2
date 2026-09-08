package route_test

import (
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	vnetlink "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/route"
)

// Test_Reconciler_SingletonRuntimeFlagsDoNotMutateRoutes verifies that carrier
// state in a singleton dump does not turn an unchanged snapshot into a change.
func Test_Reconciler_SingletonRuntimeFlagsDoNotMutateRoutes(t *testing.T) {
	for _, test := range []struct {
		name     string
		linkDown bool
	}{
		{name: "healthy singleton remains unchanged"},
		{name: "carrier-down singleton remains unchanged", linkDown: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeRouteBackend()
			backend.links["kni1"] = testLink("kni1", 20)
			dumped := testKernelRoute("198.51.100.0/24", testTable, testProtocol, testPriority)
			dumped.LinkIndex = 20
			dumped.Gw = net.ParseIP("192.0.3.1").To4()
			if test.linkDown {
				dumped.Flags = unix.RTNH_F_LINKDOWN
			}
			backend.allRoutes = []vnetlink.Route{dumped}
			desired := []route.Route{testRoute("198.51.100.0/24", "192.0.3.1", "kni1")}
			err := newTestReconciler(t, backend).Apply(t.Context(), desired, flagTestNetplan())

			require.NoError(t, err)
			require.Empty(t, backend.addAttempts)
			require.Empty(t, backend.deleteAttempts)
		})
	}
}

// Test_Reconciler_RollbackPreservesConfiguredAttributes verifies that removing
// kernel status for restoration preserves user flags, metrics, and nexthops.
func Test_Reconciler_RollbackPreservesConfiguredAttributes(t *testing.T) {
	for _, encoding := range []string{"singleton", "ECMP"} {
		t.Run(encoding, func(t *testing.T) {
			restored := testKernelRoute("198.51.100.0/24", testTable, testProtocol, testPriority)
			restored.Flags = unix.RTM_F_NOTIFY
			restored.Tos = 16
			restored.Src = net.ParseIP("192.0.2.99").To4()
			restored.Realm = 42
			restored.MTU = 1400
			restored.MTULock = true
			restored.Hoplimit = 37
			restored.RtoMin = 250
			restored.RtoMinLock = true
			if encoding == "singleton" {
				restored.LinkIndex = 10
				restored.Gw = net.ParseIP("192.0.2.1").To4()
				restored.Flags |= int(vnetlink.FLAG_ONLINK)
			} else {
				restored.MultiPath = []*vnetlink.NexthopInfo{
					{LinkIndex: 10, Gw: net.ParseIP("192.0.2.1").To4(), Hops: 1, Flags: int(vnetlink.FLAG_ONLINK)},
					{LinkIndex: 20, Gw: net.ParseIP("192.0.3.1").To4(), Hops: 3, Flags: int(vnetlink.FLAG_ONLINK)},
				}
			}
			nexthopStatus := unix.RTNH_F_DEAD | unix.RTNH_F_OFFLOAD |
				unix.RTNH_F_LINKDOWN | unix.RTNH_F_UNRESOLVED | unix.RTNH_F_TRAP
			dumped := cloneKernelRoute(restored)
			dumped.Flags |= unix.RTM_F_CLONED | unix.RTM_F_OFFLOAD |
				unix.RTM_F_TRAP | unix.RTM_F_OFFLOAD_FAILED | nexthopStatus
			for _, nexthop := range dumped.MultiPath {
				nexthop.Flags |= nexthopStatus
			}
			backend := newFakeRouteBackend()
			backend.links["kni0"] = testLink("kni0", 10)
			backend.links["kni1"] = testLink("kni1", 20)
			backend.allRoutes = []vnetlink.Route{dumped}
			backend.addErrorAt = 0
			backend.addError = unix.ENETUNREACH

			err := newTestReconciler(t, backend).Apply(
				t.Context(),
				[]route.Route{testRoute("198.51.100.0/24", "192.0.2.254", "kni0")},
				flagTestNetplan(),
			)

			require.ErrorIs(t, err, unix.ENETUNREACH)
			require.Equal(t, []vnetlink.Route{dumped}, backend.deleteAttempts)
			require.Equal(t, []vnetlink.Route{restored}, backend.added)
		})
	}
}

// Test_Reconciler_KernelSingletonRuntimeFlagsDoNotMutateRoutes verifies that an
// actual carrier-down singleton dump remains a no-op on the next apply.
func Test_Reconciler_KernelSingletonRuntimeFlagsDoNotMutateRoutes(t *testing.T) {
	handle := newIsolatedRouteKernel(t)
	desired := []route.Route{testRoute("198.51.100.0/24", "192.0.3.1", "kni1")}
	require.NoError(t, newTestReconciler(t, handle).Apply(t.Context(), desired, flagTestNetplan()))
	before := ownedFlagTestRoutes(t, handle)
	require.Len(t, before, 1)
	require.Empty(t, before[0].MultiPath)
	require.NotZero(t, before[0].Flags&unix.RTNH_F_LINKDOWN)

	backend := &recordingRouteBackend{Backend: handle}
	err := newTestReconciler(t, backend).Apply(t.Context(), desired, flagTestNetplan())

	require.NoError(t, err)
	require.Equal(t, [2]int{}, [2]int{len(backend.AddAttempts), len(backend.DeleteAttempts)},
		"an unchanged kernel singleton must require neither an add nor a delete",
	)
}

// Test_Reconciler_KernelRollbackRestoresIPv4ECMP verifies that an actual kernel
// rejection of an off-link gateway preserves the previous partly usable ECMP.
func Test_Reconciler_KernelRollbackRestoresIPv4ECMP(t *testing.T) {
	handle := newIsolatedRouteKernel(t)
	require.NoError(t, newTestReconciler(t, handle).Apply(t.Context(), []route.Route{
		testRoute("198.51.100.0/24", "192.0.2.1", "kni0"),
		testRoute("198.51.100.0/24", "192.0.3.1", "kni1"),
	}, flagTestNetplan()))
	before := ownedFlagTestRoutes(t, handle)
	require.Len(t, before, 1)
	require.Len(t, before[0].MultiPath, 2)
	require.Zero(t, before[0].MultiPath[0].Flags&unix.RTNH_F_LINKDOWN)
	require.NotZero(t, before[0].MultiPath[1].Flags&unix.RTNH_F_LINKDOWN)

	desired := []route.Route{testRoute("198.51.100.0/24", "203.0.113.1", "kni0")}
	err := newTestReconciler(t, handle).Apply(t.Context(), desired, flagTestNetplan())
	after := ownedFlagTestRoutes(t, handle)

	require.Error(t, err, "the replacement gateway has no connected route in this isolated namespace")
	require.Equal(t, before, after, "a failed replacement must restore the complete old kernel ECMP route")
}

// recordingRouteBackend observes real-kernel mutations without changing them.
type recordingRouteBackend struct {
	route.Backend
	AddAttempts    []vnetlink.Route
	DeleteAttempts []vnetlink.Route
}

// RouteAdd records the request before forwarding it to the kernel.
func (m *recordingRouteBackend) RouteAdd(kernelRoute *vnetlink.Route) error {
	m.AddAttempts = append(m.AddAttempts, cloneKernelRoute(*kernelRoute))
	return m.Backend.RouteAdd(kernelRoute)
}

// RouteDel records the complete deletion request before delegating it.
func (m *recordingRouteBackend) RouteDel(kernelRoute *vnetlink.Route) error {
	m.DeleteAttempts = append(m.DeleteAttempts, cloneKernelRoute(*kernelRoute))
	return m.Backend.RouteDel(kernelRoute)
}

// flagTestNetplan declares both fixture interfaces as currently managed links.
func flagTestNetplan() netplan.State {
	return netplan.State{Links: []netplan.Link{{Name: "kni0"}, {Name: "kni1"}}}
}

// ownedFlagTestRoutes captures only the fixture's exact IPv4 ownership tuple.
func ownedFlagTestRoutes(t *testing.T, backend route.Backend) []vnetlink.Route {
	t.Helper()
	routes, err := backend.RouteListFiltered(
		vnetlink.FAMILY_V4,
		&vnetlink.Route{Table: testTable, Protocol: testProtocol},
		vnetlink.RT_FILTER_TABLE|vnetlink.RT_FILTER_PROTOCOL,
	)
	require.NoError(t, err)
	owned := []vnetlink.Route{}
	for _, kernelRoute := range routes {
		if kernelRoute.Priority == testPriority {
			owned = append(owned, kernelRoute)
		}
	}
	return owned
}

// newIsolatedRouteKernel creates disposable veth links only in an explicitly
// enabled, loopback-only Docker network namespace and cleans them up afterwards.
func newIsolatedRouteKernel(t *testing.T) *vnetlink.Handle {
	t.Helper()
	if os.Getenv("YANET_ROUTE_KERNEL_TESTS") != "1" {
		t.Skip("set YANET_ROUTE_KERNEL_TESTS=1 only inside docker --network none --cap-add NET_ADMIN")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatalf("refusing kernel mutations outside the disposable Docker fixture: %v", err)
	}
	handle, err := vnetlink.NewHandle(unix.NETLINK_ROUTE)
	require.NoError(t, err)
	t.Cleanup(handle.Close)
	require.NoError(t, handle.SetSocketTimeout(2*time.Second))
	links, err := handle.LinkList()
	require.NoError(t, err)
	require.Len(t, links, 1, "refusing kernel mutations: the namespace must initially contain only loopback")
	require.Equal(t, "lo", links[0].Attrs().Name, "refusing kernel mutations in a non-isolated namespace")

	for idx, address := range []string{"192.0.2.2/24", "192.0.3.2/24"} {
		name := fmt.Sprintf("kni%d", idx)
		peerName := fmt.Sprintf("routepeer%d", idx)
		pair := &vnetlink.Veth{LinkAttrs: vnetlink.LinkAttrs{Name: name}, PeerName: peerName}
		require.NoError(t, handle.LinkAdd(pair))
		t.Cleanup(func() { require.NoError(t, handle.LinkDel(pair)) })
		link, err := handle.LinkByName(name)
		require.NoError(t, err)
		parsedAddress, err := vnetlink.ParseAddr(address)
		require.NoError(t, err)
		require.NoError(t, handle.AddrAdd(link, parsedAddress))
		require.NoError(t, handle.LinkSetUp(link))
		if idx == 0 {
			peer, err := handle.LinkByName(peerName)
			require.NoError(t, err)
			require.NoError(t, handle.LinkSetUp(peer))
		}
	}
	require.Eventually(
		t,
		func() bool {
			link, err := handle.LinkByName("kni0")
			return err == nil && link.Attrs().Flags&net.FlagRunning != 0
		},
		time.Second,
		10*time.Millisecond,
		"the first ECMP member must have carrier",
	)
	return handle
}

var _ route.Backend = (*recordingRouteBackend)(nil)
