package operator_test

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/xnetip"
	"github.com/yanet-platform/yanet2/common/go/maptrie"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/rcucache"
	"github.com/yanet-platform/yanet2/common/go/xcfg"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
	op "github.com/yanet-platform/yanet2/operators/route/internal/operator"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
	"github.com/yanet-platform/yanet2/operators/route/neigh"
)

// staticPath returns a configured alternative for the given exact prefix.
func staticPath(prefix, nexthop string) op.StaticRouteConfig {
	return op.StaticRouteConfig{Prefix: prefix, NexthopAddr: nexthop}
}

// ribPath returns a path whose embedded prefix may retain host bits.
func ribPath(prefix, nexthop string, source rib.RouteSourceID) rib.Route {
	return rib.Route{
		Prefix: netip.MustParsePrefix(prefix), NextHop: netip.MustParseAddr(nexthop),
		SourceID: source,
	}
}

// routeSnapshot groups paths under canonical trie keys and native neighbour keys.
func routeSnapshot(routes []rib.Route, devices map[string]string) op.RouteSnapshot {
	dump := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](0)
	for _, route := range routes {
		prefix := route.Prefix.Masked()
		list := dump[prefix.Bits()][prefix]
		list.Routes = append(list.Routes, route)
		dump[prefix.Bits()][prefix] = list
	}
	cache := map[netip.Addr]neigh.NeighbourEntry{}
	for address, device := range devices {
		cache[netip.MustParseAddr(address).Unmap()] = neigh.NeighbourEntry{
			HardwareRoute: testHardwareRoute(address, device),
		}
	}
	return op.RouteSnapshot{
		RIBs: map[string]maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList]{
			"route0": dump,
		},
		Neighbours: rcucache.NewCache(cache).View(),
	}
}

// testHardwareRoute distinguishes fixture nexthops by their low address bytes.
func testHardwareRoute(address, device string) neigh.HardwareRoute {
	bytes := netip.MustParseAddr(address).Unmap().As16()
	return neigh.HardwareRoute{
		SourceMAC:      [6]byte{2, 1, 0, 0, 0, 1},
		DestinationMAC: [6]byte{2, 0, bytes[12], bytes[13], bytes[14], bytes[15]},
		Device:         device,
	}
}

// resolvedPath describes one expected prefix and its resolved hardware route.
func resolvedPath(prefix, address, device string) map[string][]neigh.HardwareRoute {
	return map[string][]neigh.HardwareRoute{
		prefix: {testHardwareRoute(address, device)},
	}
}

// requireFIB compares exact wire ranges and hardware nexthops without relying
// on entry ordering, including when assertions are collected during polling.
func requireFIB(
	collector require.TestingT,
	request *routepb.UpdateFIBRequest,
	module string,
	expected map[string][]neigh.HardwareRoute,
) {
	require.NotNil(collector, request)
	require.Equal(collector, module, request.GetModuleName())
	require.Len(collector, request.GetEntries(), len(expected))
	actual := map[string][]neigh.HardwareRoute{}
	for _, entry := range request.GetEntries() {
		start, end, err := entry.GetRange().ToRange()
		require.NoError(collector, err)
		key := start.String() + "-" + end.String()
		require.NotContains(collector, actual, key, "duplicate forwarding range")
		actual[key] = []neigh.HardwareRoute{}
		for _, nexthop := range entry.GetNexthops() {
			actual[key] = append(actual[key], neigh.HardwareRoute{
				SourceMAC:      nexthop.GetSrcMac().EUI48(),
				DestinationMAC: nexthop.GetDstMac().EUI48(), Device: nexthop.GetDevice(),
			})
		}
	}
	for prefix, nexthops := range expected {
		canonical := netip.MustParsePrefix(prefix).Masked()
		network, ok := xnetip.NetworkFromPrefix(canonical)
		require.True(collector, ok)
		key := canonical.Addr().String() + "-" + network.LastAddr().String()
		require.Contains(collector, actual, key)
		require.ElementsMatch(collector, nexthops, actual[key])
	}
}

// Test_GatewayActuator_PreflightGatesWholeUpdate verifies that each module must
// satisfy forwarding requirements before any table or function write.
func Test_GatewayActuator_PreflightGatesWholeUpdate(t *testing.T) {
	resolved := routeSnapshot(
		[]rib.Route{ribPath("10.10.0.0/24", "192.0.2.1", rib.RouteSourceBird)},
		map[string]string{"192.0.2.1": "eth0"},
	)
	emptyLists := maptrie.NewMapTrie[netip.Prefix, netip.Addr, rib.RoutesList](0)
	emptyLists[24][netip.MustParsePrefix("10.20.0.0/24")] = rib.RoutesList{}
	mixed := routeSnapshot(
		[]rib.Route{ribPath("10.10.0.0/24", "192.0.2.1", rib.RouteSourceBird)},
		map[string]string{"192.0.2.1": "eth0"},
	)
	mixed.RIBs["empty"] = emptyLists
	static := ribPath("10.30.0.0/24", "192.0.2.1", rib.RouteSourceStatic)
	staticOnly := routeSnapshot([]rib.Route{static}, map[string]string{"192.0.2.1": "eth0"})
	for _, tc := range []struct {
		name       string
		snapshot   op.RouteSnapshot
		message    string
		module     string
		configured []op.StaticRouteConfig
		expectBird bool
		expected   map[string][]neigh.HardwareRoute
	}{
		{
			name: "empty neighbours with routes", message: "empty merged neighbour view", module: "route0",
			snapshot: op.RouteSnapshot{RIBs: resolved.RIBs},
		},
		{
			name: "absent RIB map", message: "no RIBs", module: "route0",
			snapshot: op.RouteSnapshot{Neighbours: resolved.Neighbours},
		},
		{
			name: "prefixes without route records", message: "no route records", module: "empty",
			snapshot: op.RouteSnapshot{
				RIBs:       map[string]maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList]{"empty": emptyLists},
				Neighbours: resolved.Neighbours,
			},
		},
		{
			name: "unresolved routes with unrelated neighbours", message: "empty local FIB", module: "route0",
			snapshot: routeSnapshot(
				[]rib.Route{ribPath("10.10.0.0/24", "192.0.2.2", rib.RouteSourceBird)},
				map[string]string{"192.0.2.1": "eth0"},
			),
		},
		{
			name: "only foreign-device paths", message: "empty local FIB", module: "route0",
			snapshot: routeSnapshot(
				[]rib.Route{ribPath("10.10.0.0/24", "192.0.2.1", rib.RouteSourceBird)},
				map[string]string{"192.0.2.1": "eth1"},
			),
		},
		{
			name: "valid module alongside empty dump", message: "no route records", module: "empty",
			snapshot: mixed, configured: []op.StaticRouteConfig{staticPath("10.30.0.0/24", "192.0.2.3")},
		},
		{
			name: "valid module alongside empty local FIB", message: "empty local FIB", module: "unreachable-module",
			snapshot: op.RouteSnapshot{
				RIBs: map[string]maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList]{
					"route0": resolved.RIBs["route0"],
					"unreachable-module": routeSnapshot([]rib.Route{
						ribPath("10.20.0.0/24", "192.0.2.2", rib.RouteSourceBird),
					}, nil).RIBs["route0"],
				},
				Neighbours: resolved.Neighbours,
			},
		},
		{
			name:     "static-only input publishes when BIRD is not expected",
			snapshot: staticOnly, expected: resolvedPath("10.30.0.0/24", "192.0.2.1", "eth0"),
		},
		{
			name:     "resolved static routes cannot replace expected BIRD input",
			snapshot: staticOnly, expectBird: true, message: "no BIRD route records", module: "route0",
			configured: []op.StaticRouteConfig{staticPath("10.40.0.0/24", "192.0.2.4")},
		},
		{
			name:       "resolved static routes cannot mask unresolved BIRD paths",
			expectBird: true, message: "no local BIRD forwarding", module: "route0",
			snapshot: routeSnapshot([]rib.Route{
				static, ribPath("10.10.0.0/24", "192.0.2.2", rib.RouteSourceBird),
			}, map[string]string{"192.0.2.1": "eth0"}),
		},
		{
			name:       "resolved static routes cannot mask device-excluded BIRD paths",
			expectBird: true, message: "no local BIRD forwarding", module: "route0",
			snapshot: routeSnapshot([]rib.Route{
				static, ribPath("10.10.0.0/24", "192.0.2.2", rib.RouteSourceBird),
			}, map[string]string{"192.0.2.1": "eth0", "192.0.2.2": "eth1"}),
		},
		{
			name:       "valid BIRD module cannot publish alongside a static-only module",
			expectBird: true, message: "no BIRD route records", module: "static-only",
			snapshot: op.RouteSnapshot{
				RIBs: map[string]maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList]{
					"route0": resolved.RIBs["route0"], "static-only": staticOnly.RIBs["route0"],
				},
				Neighbours: resolved.Neighbours,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway, endpoint := startFakeGateway(t)
			actuator, err := op.NewGatewayActuator(
				operator.GatewayConfig{Name: "gw0", Endpoint: xcfg.MustNonEmptyString(endpoint)},
				op.WithGatewayActuatorFunction(op.DefaultConfig().Function),
				op.WithGatewayActuatorDevices([]string{"eth0"}),
				op.WithGatewayActuatorStaticRoutes(tc.configured),
				op.WithGatewayActuatorExpectBird(tc.expectBird),
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, actuator.Close()) })
			err = actuator.Apply(requestContext(t), tc.snapshot)
			writes, functionUpdates := gateway.receivedWrites()
			if tc.expected != nil {
				require.NoError(t, err)
				require.Len(t, writes, 1)
				requireFIB(t, writes[0], "route0", tc.expected)
				require.Equal(t, 1, functionUpdates)
				return
			}
			require.ErrorContains(t, err, tc.message)
			require.ErrorContains(t, err, "gw0")
			require.ErrorContains(t, err, tc.module)
			for _, path := range tc.configured {
				require.ErrorContains(t, err, path.Prefix)
			}
			require.Empty(t, writes)
			require.Zero(t, functionUpdates)
		})
	}
}

// Test_GatewayActuator_StaticRequirements verifies that only locally resolvable
// configured static alternatives satisfy each exact-prefix requirement.
func Test_GatewayActuator_StaticRequirements(t *testing.T) {
	configured := []op.StaticRouteConfig{staticPath("10.10.0.0/24", "192.0.2.1")}
	static := ribPath("10.10.0.0/24", "192.0.2.1", rib.RouteSourceStatic)
	lowerPriorityStatic := static
	lowerPriorityStatic.Pref = 50
	higherPriorityStatic := ribPath("10.10.0.0/24", "192.0.2.2", rib.RouteSourceStatic)
	higherPriorityStatic.Pref = 100
	fallback := ribPath("10.20.0.0/24", "192.0.2.254", rib.RouteSourceBird)
	for _, tc := range []struct {
		name       string
		configured []op.StaticRouteConfig
		routes     []rib.Route
		neighbours map[string]string
		devices    []string
		module     string
		pathModule string
		missing    []string
		expected   map[string][]neigh.HardwareRoute
		expectBird bool
	}{
		{
			name:       "expected BIRD forwarding publishes alongside resolved configured static paths",
			configured: configured, routes: []rib.Route{static, fallback}, expectBird: true,
			neighbours: map[string]string{"192.0.2.1": "eth0", "192.0.2.254": "eth0"},
			expected: map[string][]neigh.HardwareRoute{
				"10.10.0.0/24": {testHardwareRoute("192.0.2.1", "eth0")},
				"10.20.0.0/24": {testHardwareRoute("192.0.2.254", "eth0")},
			},
		},
		{
			name:       "unresolved configured path still publishes a nonempty fallback",
			configured: configured, routes: []rib.Route{static, fallback}, missing: []string{"10.10.0.0/24"}, expectBird: true,
			neighbours: map[string]string{"192.0.2.254": "eth0"},
			expected:   resolvedPath("10.20.0.0/24", "192.0.2.254", "eth0"),
		},
		{
			name:       "static-only mode publishes one resolved configured alternative without BIRD",
			configured: append([]op.StaticRouteConfig{staticPath("10.10.0.0/24", "192.0.2.2")}, configured...),
			routes:     []rib.Route{static, ribPath("10.10.0.0/24", "192.0.2.2", rib.RouteSourceStatic)},
			neighbours: map[string]string{"192.0.2.1": "eth0"},
			expected:   resolvedPath("10.10.0.0/24", "192.0.2.1", "eth0"),
		},
		{
			name:       "foreign device does not satisfy local resolution",
			configured: configured, routes: []rib.Route{static, fallback},
			neighbours: map[string]string{"192.0.2.1": "eth1", "192.0.2.254": "eth0"}, devices: []string{"eth0"},
			missing:  []string{"10.10.0.0/24"},
			expected: resolvedPath("10.20.0.0/24", "192.0.2.254", "eth0"),
		},
		{
			name:       "a resolved configured path satisfies readiness even when not selected",
			configured: configured, routes: []rib.Route{higherPriorityStatic, lowerPriorityStatic},
			devices: []string{"eth0"}, neighbours: map[string]string{"192.0.2.1": "eth0", "192.0.2.2": "eth0"},
			expected: resolvedPath("10.10.0.0/24", "192.0.2.2", "eth0"),
		},
		{
			name: "all missing prefixes are deterministic while available prefixes publish",
			configured: []op.StaticRouteConfig{
				staticPath("10.30.0.0/24", "192.0.2.3"),
				staticPath("10.30.0.7/24", "192.0.2.4"),
				staticPath("10.20.0.7/24", "192.0.2.2"),
				staticPath("10.20.0.0/24", "192.0.2.2"), configured[0],
			},
			routes:     []rib.Route{static, ribPath("10.20.0.0/24", "192.0.2.2", rib.RouteSourceStatic)},
			neighbours: map[string]string{"192.0.2.1": "eth0"},
			expected:   resolvedPath("10.10.0.0/24", "192.0.2.1", "eth0"),
			missing:    []string{"10.20.0.0/24", "10.30.0.0/24"},
		},
		{
			name:       "runtime deletion fails despite an existing neighbour",
			configured: configured, routes: []rib.Route{fallback},
			neighbours: map[string]string{"192.0.2.1": "eth0", "192.0.2.254": "eth0"},
			missing:    []string{"10.10.0.0/24"},
			expected:   resolvedPath("10.20.0.0/24", "192.0.2.254", "eth0"),
		},
		{
			name:       "BGP path cannot substitute even with the configured nexthop",
			configured: configured, routes: []rib.Route{ribPath("10.10.0.0/24", "192.0.2.1", rib.RouteSourceBird)},
			neighbours: map[string]string{"192.0.2.1": "eth0"},
			expected:   resolvedPath("10.10.0.0/24", "192.0.2.1", "eth0"),
			missing:    []string{"10.10.0.0/24"},
		},
		{
			name:       "unconfigured static fallback publishes but cannot satisfy the requirement",
			configured: configured, routes: []rib.Route{static, ribPath("10.10.0.0/24", "192.0.2.2", rib.RouteSourceStatic)},
			neighbours: map[string]string{"192.0.2.2": "eth0"},
			expected:   resolvedPath("10.10.0.0/24", "192.0.2.2", "eth0"),
			missing:    []string{"10.10.0.0/24"},
		},
		{
			name:       "one resolved BIRD path permits omitting another unresolved BIRD path",
			expectBird: true,
			routes:     []rib.Route{ribPath("10.10.0.0/24", "192.0.2.1", rib.RouteSourceBird), fallback},
			neighbours: map[string]string{"192.0.2.254": "eth0"},
			expected:   resolvedPath("10.20.0.0/24", "192.0.2.254", "eth0"),
		},
		{
			name: "canonical prefixes group host-bit alternatives and normalize mapped nexthops",
			configured: []op.StaticRouteConfig{
				staticPath("10.10.0.7/24", "::ffff:192.0.2.1"),
				staticPath("10.10.0.8/24", "192.0.2.2"),
			},
			routes:     []rib.Route{ribPath("10.10.0.9/24", "192.0.2.1", rib.RouteSourceStatic)},
			neighbours: map[string]string{"::ffff:192.0.2.1": "eth0"},
			expected:   resolvedPath("10.10.0.0/24", "192.0.2.1", "eth0"),
		},
		{
			name:       "mapped runtime nexthop matches native configuration",
			configured: configured, routes: []rib.Route{ribPath("10.10.0.0/24", "::ffff:192.0.2.1", rib.RouteSourceStatic)},
			neighbours: map[string]string{"192.0.2.1": "eth0"},
			expected:   resolvedPath("10.10.0.0/24", "192.0.2.1", "eth0"),
		},
		{
			name:       "IPv6 host-bit prefixes are canonicalized",
			configured: []op.StaticRouteConfig{staticPath("2001:db8:1::7/64", "2001:db8::1")},
			routes:     []rib.Route{ribPath("2001:db8:1::8/64", "2001:db8::1", rib.RouteSourceStatic)},
			neighbours: map[string]string{"2001:db8::1": "eth0"},
			expected:   resolvedPath("2001:db8:1::/64", "2001:db8::1", "eth0"),
		},
		{
			name:       "a covering prefix is not an exact-prefix requirement match",
			configured: configured, routes: []rib.Route{ribPath("10.10.0.0/16", "192.0.2.1", rib.RouteSourceStatic)},
			neighbours: map[string]string{"192.0.2.1": "eth0"},
			expected:   resolvedPath("10.10.0.0/16", "192.0.2.1", "eth0"),
			missing:    []string{"10.10.0.0/24"},
		},
		{
			name:       "missing managed module is validated even when another module has the path",
			configured: configured, routes: []rib.Route{static}, pathModule: "other",
			neighbours: map[string]string{"192.0.2.1": "eth0"},
			expected:   resolvedPath("10.10.0.0/24", "192.0.2.1", "eth0"),
			missing:    []string{"10.10.0.0/24"},
		},
		{
			name:       "configured nondefault module resolves without a default module RIB",
			configured: configured, routes: []rib.Route{static}, module: "route-custom",
			neighbours: map[string]string{"192.0.2.1": "eth0"},
			expected:   resolvedPath("10.10.0.0/24", "192.0.2.1", "eth0"),
		},
		{
			name:       "a matching path only in the default module cannot satisfy a nondefault module",
			configured: configured, routes: []rib.Route{static}, module: "route-custom", pathModule: "route0",
			neighbours: map[string]string{"192.0.2.1": "eth0"},
			expected:   resolvedPath("10.10.0.0/24", "192.0.2.1", "eth0"),
			missing:    []string{"10.10.0.0/24"},
		},
		{
			name: "all distinct required prefixes resolve including IPv6 default and multicast",
			configured: []op.StaticRouteConfig{
				configured[0], staticPath("2001:db8:1::/64", "2001:db8::1"),
				staticPath("::/0", "2001:db8::2"), staticPath("ff02::1/128", "2001:db8::3"),
			},
			routes: []rib.Route{
				static, ribPath("2001:db8:1::/64", "2001:db8::1", rib.RouteSourceStatic),
				ribPath("::/0", "2001:db8::2", rib.RouteSourceStatic),
				ribPath("ff02::1/128", "2001:db8::3", rib.RouteSourceStatic),
			},
			neighbours: map[string]string{
				"192.0.2.1": "eth0", "2001:db8::1": "eth1",
				"2001:db8::2": "eth2", "2001:db8::3": "eth3",
			},
			expected: map[string][]neigh.HardwareRoute{
				"10.10.0.0/24":    {testHardwareRoute("192.0.2.1", "eth0")},
				"2001:db8:1::/64": {testHardwareRoute("2001:db8::1", "eth1")},
				"::/0":            {testHardwareRoute("2001:db8::2", "eth2")},
				"ff02::1/128":     {testHardwareRoute("2001:db8::3", "eth3")},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway, endpoint := startFakeGateway(t)
			function := op.DefaultConfig().Function
			module := "route0"
			if tc.module != "" {
				module = tc.module
			}
			function.Module = xcfg.MustNonEmptyString(module)
			actuator, err := op.NewGatewayActuator(
				operator.GatewayConfig{Name: "gw0", Endpoint: xcfg.MustNonEmptyString(endpoint)},
				op.WithGatewayActuatorFunction(function),
				op.WithGatewayActuatorStaticRoutes(tc.configured),
				op.WithGatewayActuatorDevices(tc.devices),
				op.WithGatewayActuatorExpectBird(tc.expectBird),
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, actuator.Close()) })
			snapshot := routeSnapshot(tc.routes, tc.neighbours)
			pathModule := module
			if tc.pathModule != "" {
				pathModule = tc.pathModule
			}
			if pathModule != "route0" {
				snapshot.RIBs[pathModule] = snapshot.RIBs["route0"]
				delete(snapshot.RIBs, "route0")
			}
			err = actuator.Apply(requestContext(t), snapshot)
			if len(tc.missing) == 0 {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "gw0")
				require.ErrorContains(t, err, module)
				previous := -1
				for _, prefix := range tc.missing {
					require.ErrorContains(t, err, prefix)
					require.Equal(t, 1, strings.Count(err.Error(), prefix), "canonical alternatives must be deduplicated")
					position := strings.Index(err.Error(), prefix)
					require.Greater(t, position, previous)
					previous = position
				}
				for prefix := range tc.expected {
					if !slices.Contains(tc.missing, prefix) {
						require.NotContains(t, err.Error(), prefix, "resolved prefixes must not appear in diagnostics")
					}
				}
				for _, path := range tc.configured {
					prefix := netip.MustParsePrefix(path.Prefix)
					if prefix != prefix.Masked() {
						require.NotContains(t, err.Error(), prefix.String(), "diagnostics must use canonical prefixes")
					}
				}
			}
			writes, functionUpdates := gateway.receivedWrites()
			require.Len(t, writes, 1)
			requireFIB(t, writes[0], pathModule, tc.expected)
			require.Equal(t, 1, functionUpdates)
		})
	}
}

// Test_GatewayActuator_DeviceScopeOwnership verifies that mutating the caller's
// device list cannot change forwarding eligibility or static-path readiness.
func Test_GatewayActuator_DeviceScopeOwnership(t *testing.T) {
	gateway, endpoint := startFakeGateway(t)
	devices := []string{"eth0"}
	actuator, err := op.NewGatewayActuator(
		operator.GatewayConfig{Name: "gw0", Endpoint: xcfg.MustNonEmptyString(endpoint)},
		op.WithGatewayActuatorFunction(op.DefaultConfig().Function),
		op.WithGatewayActuatorDevices(devices),
		op.WithGatewayActuatorStaticRoutes([]op.StaticRouteConfig{staticPath("10.10.0.0/24", "192.0.2.1")}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, actuator.Close()) })
	devices[0] = "eth1"
	snapshot := routeSnapshot(
		[]rib.Route{
			ribPath("10.10.0.0/24", "192.0.2.1", rib.RouteSourceStatic),
			ribPath("10.10.0.0/24", "192.0.2.2", rib.RouteSourceStatic),
		},
		map[string]string{"192.0.2.1": "eth0", "192.0.2.2": "eth1"},
	)
	require.NoError(t, actuator.Apply(requestContext(t), snapshot))
	writes, _ := gateway.receivedWrites()
	require.Len(t, writes, 1)
	requireFIB(t, writes[0], "route0", resolvedPath("10.10.0.0/24", "192.0.2.1", "eth0"))
}

// Test_GatewayActuator_RejectedFIB_ClassificationLifecycle verifies that build
// gauges refresh even when programming or preflight rejects the update.
func Test_GatewayActuator_RejectedFIB_ClassificationLifecycle(t *testing.T) {
	gateway, endpoint := startFakeGateway(t)
	gateway.failWrites(status.Error(codes.Unavailable, "FIB rejected"), nil)
	metrics := op.NewMetrics(&op.RIBStore{}, neigh.NewNeighTable())
	actuator, err := op.NewGatewayActuator(
		operator.GatewayConfig{Name: "gw0", Endpoint: xcfg.MustNonEmptyString(endpoint)},
		op.WithGatewayActuatorFunction(op.DefaultConfig().Function),
		op.WithGatewayActuatorDevices([]string{"eth0"}),
		op.WithGatewayActuatorOnFIBBuilt(metrics.Gateway("gw0").OnFIBBuilt),
		op.WithGatewayActuatorExpectBird(true),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, actuator.Close()) })
	routes := []rib.Route{
		ribPath("10.10.0.0/24", "192.0.2.1", rib.RouteSourceBird),
		ribPath("10.10.0.0/24", "192.0.2.2", rib.RouteSourceBird),
		ribPath("10.10.0.0/24", "192.0.2.3", rib.RouteSourceBird),
		ribPath("10.10.0.0/24", "192.0.2.4", rib.RouteSourceBird),
	}
	for idx := range routes {
		routes[idx].GlobalID = uint32(idx + 1)
	}
	for idx, tc := range []struct {
		name       string
		routes     []rib.Route
		neighbours map[string]string
		unresolved float64
		excluded   float64
		expected   map[string][]neigh.HardwareRoute
	}{
		{
			name: "missing and foreign paths are classified separately", routes: routes,
			neighbours: map[string]string{"192.0.2.3": "eth1", "192.0.2.4": "eth0"},
			unresolved: 2, excluded: 1, expected: resolvedPath("10.10.0.0/24", "192.0.2.4", "eth0"),
		},
		{
			name: "neighbour-only changes replace classifications after a rejected write", routes: routes,
			neighbours: map[string]string{"192.0.2.1": "eth0", "192.0.2.2": "eth1", "192.0.2.3": "eth1", "192.0.2.4": "eth1"},
			excluded:   3, expected: resolvedPath("10.10.0.0/24", "192.0.2.1", "eth0"),
		},
		{
			name:   "empty neighbours refresh classifications without a write",
			routes: routes, unresolved: 4,
		},
		{
			name: "static decoy refreshes classifications without a write",
			routes: []rib.Route{
				ribPath("10.10.0.0/24", "192.0.2.1", rib.RouteSourceBird),
				ribPath("10.20.0.0/24", "192.0.2.4", rib.RouteSourceStatic),
			},
			neighbours: map[string]string{"192.0.2.4": "eth0"}, unresolved: 1,
		},
		{
			name:       "explicit empty module dump clears unresolved classifications",
			neighbours: map[string]string{"192.0.2.1": "eth1", "192.0.2.2": "eth1", "192.0.2.3": "eth0"},
		},
	} {
		t.Logf("step %d: %s", idx+1, tc.name)
		previousWrites, previousFunctionUpdates := gateway.receivedWrites()
		err := actuator.Apply(requestContext(t), routeSnapshot(tc.routes, tc.neighbours))
		require.Error(t, err)
		if tc.expected != nil {
			require.ErrorContains(t, err, "FIB rejected")
			require.Equal(t, codes.Unavailable, status.Code(err))
		}
		requireFIBClassifications(t, metrics.Collect(), "gw0", "route0", tc.unresolved, tc.excluded)
		writes, functionUpdates := gateway.receivedWrites()
		if tc.expected == nil {
			require.Len(t, writes, len(previousWrites))
			require.Equal(t, previousFunctionUpdates, functionUpdates)
		} else {
			require.Len(t, writes, len(previousWrites)+1)
			requireFIB(t, gateway.lastFIBRequest(), "route0", tc.expected)
		}
	}
}

// Test_GatewayActuator_StaticAndRPCFailures verifies that validation and both
// programming errors survive while every module and the function are attempted.
func Test_GatewayActuator_StaticAndRPCFailures(t *testing.T) {
	gateway, endpoint := startFakeGateway(t)
	gateway.failWrites(status.Error(codes.Unavailable, "FIB rejected"), status.Error(codes.Internal, "function rejected"))
	actuator, err := op.NewGatewayActuator(
		operator.GatewayConfig{Name: "gw0", Endpoint: xcfg.MustNonEmptyString(endpoint)},
		op.WithGatewayActuatorFunction(op.DefaultConfig().Function),
		op.WithGatewayActuatorStaticRoutes([]op.StaticRouteConfig{staticPath("10.10.0.0/24", "192.0.2.1")}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, actuator.Close()) })
	snapshot := routeSnapshot(
		[]rib.Route{ribPath("10.20.0.0/24", "192.0.2.2", rib.RouteSourceBird)},
		map[string]string{"192.0.2.2": "eth0", "192.0.2.3": "eth1"},
	)
	snapshot.RIBs["other"] = routeSnapshot(
		[]rib.Route{ribPath("10.30.0.0/24", "192.0.2.3", rib.RouteSourceBird)}, nil,
	).RIBs["route0"]
	err = actuator.Apply(requestContext(t), snapshot)
	require.ErrorContains(t, err, "10.10.0.0/24")
	require.ErrorContains(t, err, "FIB rejected")
	require.ErrorContains(t, err, "function rejected")
	writes, functionUpdates := gateway.receivedWrites()
	require.Len(t, writes, 2)
	modules := []string{writes[0].GetModuleName(), writes[1].GetModuleName()}
	require.ElementsMatch(t, []string{"route0", "other"}, modules)
	expected := map[string]map[string][]neigh.HardwareRoute{
		"route0": resolvedPath("10.20.0.0/24", "192.0.2.2", "eth0"),
		"other":  resolvedPath("10.30.0.0/24", "192.0.2.3", "eth1"),
	}
	for _, request := range writes {
		requireFIB(t, request, request.GetModuleName(), expected[request.GetModuleName()])
	}
	require.Equal(t, 1, functionUpdates)
}

// Test_GatewayActuator_ResolvedStatic_RPCRecovery verifies that local resolution
// cannot mask either programming failure and success follows RPC recovery only.
func Test_GatewayActuator_ResolvedStatic_RPCRecovery(t *testing.T) {
	for _, tc := range []struct {
		name          string
		fibError      error
		functionError error
		message       string
	}{
		{
			name:     "FIB RPC rejects resolved static state until recovery",
			fibError: status.Error(codes.Unavailable, "FIB rejected"), message: "FIB rejected",
		},
		{
			name:          "function RPC rejects resolved static state until recovery",
			functionError: status.Error(codes.Internal, "function rejected"), message: "function rejected",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway, endpoint := startFakeGateway(t)
			gateway.failWrites(tc.fibError, tc.functionError)
			actuator, err := op.NewGatewayActuator(
				operator.GatewayConfig{Name: "gw0", Endpoint: xcfg.MustNonEmptyString(endpoint)},
				op.WithGatewayActuatorFunction(op.DefaultConfig().Function),
				op.WithGatewayActuatorStaticRoutes([]op.StaticRouteConfig{staticPath("10.10.0.0/24", "192.0.2.1")}),
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, actuator.Close()) })
			snapshot := routeSnapshot(
				[]rib.Route{ribPath("10.10.0.0/24", "192.0.2.1", rib.RouteSourceStatic)},
				map[string]string{"192.0.2.1": "eth0"},
			)
			for range 2 {
				err = actuator.Apply(requestContext(t), snapshot)
				require.ErrorContains(t, err, tc.message)
				require.NotContains(t, err.Error(), "unresolved configured static prefixes")
			}
			writes, functionUpdates := gateway.receivedWrites()
			require.Len(t, writes, 2)
			if tc.functionError != nil {
				require.Equal(t, 2, functionUpdates)
			} else {
				require.Equal(t, 1, functionUpdates)
			}
			for _, request := range writes {
				requireFIB(t, request, "route0", resolvedPath("10.10.0.0/24", "192.0.2.1", "eth0"))
			}
			gateway.failWrites(nil, nil)
			require.NoError(t, actuator.Apply(requestContext(t), snapshot))
			writes, functionUpdates = gateway.receivedWrites()
			require.Len(t, writes, 3)
			requireFIB(t, writes[2], "route0", resolvedPath("10.10.0.0/24", "192.0.2.1", "eth0"))
			if tc.functionError != nil {
				require.Equal(t, 3, functionUpdates)
			} else {
				require.Equal(t, 1, functionUpdates)
			}
		})
	}
}

// Test_GatewayActuator_InvalidStaticConfiguration verifies that malformed
// configured paths are rejected before an actuator can publish state.
func Test_GatewayActuator_InvalidStaticConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name  string
		route op.StaticRouteConfig
		field string
		value string
	}{
		{name: "invalid prefix", route: staticPath("not-a-prefix", "192.0.2.1"), field: "prefix", value: "not-a-prefix"},
		{name: "invalid nexthop", route: staticPath("10.10.0.0/24", "not-an-address"), field: "nexthop", value: "not-an-address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, endpoint := startFakeGateway(t)
			actuator, err := op.NewGatewayActuator(
				operator.GatewayConfig{Name: "gw0", Endpoint: xcfg.MustNonEmptyString(endpoint)},
				op.WithGatewayActuatorFunction(op.DefaultConfig().Function),
				op.WithGatewayActuatorStaticRoutes([]op.StaticRouteConfig{tc.route}),
			)
			if actuator != nil {
				t.Cleanup(func() { require.NoError(t, actuator.Close()) })
			}
			require.ErrorContains(t, err, "failed to parse static route "+tc.field)
			require.ErrorContains(t, err, tc.value)
			require.Nil(t, actuator)
		})
	}
}
