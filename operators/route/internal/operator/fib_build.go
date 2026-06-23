package operator

import (
	"net/netip"
	"slices"

	"github.com/yanet-platform/yanet2/common/go/maptrie"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
)

// FIBEntry describes a single FIB prefix and its ECMP nexthops.
type FIBEntry struct {
	// Prefix is the destination network.
	Prefix netip.Prefix
	// Nexthops are the resolved hardware routes for the prefix. The slice
	// is deduplicated.
	Nexthops []neigh.HardwareRoute
}

// FIB is the complete forwarding table for one module config.
type FIB struct {
	// Name is the module config name this FIB belongs to.
	Name string
	// Entries is the list of FIB entries.
	Entries []FIBEntry
}

// FIBBuildStats summarises a BuildFIB pass for observability.
type FIBBuildStats struct {
	TotalPrefixes     int
	TotalRoutes       int
	SkippedPrefixes   int
	NeighbourNotFound int
	HardwareRoutes    int
	PrefixesAdded     int
	// FilteredRoutes counts eligible routes dropped because a better route
	// of the same source exists.
	FilteredRoutes int
}

// BuildFIB resolves a RIB dump against the supplied neighbour view and
// produces a deduplicated FIB.
//
// A route is eligible only when its nexthop resolves in the neighbour view;
// pass a device-filtered view to restrict a gateway to its own egress
// devices. The best routes per source are chosen among the eligible routes,
// so a gateway can fall back to a lower-priority route it can actually reach
// rather than a globally best one it cannot.
func BuildFIB(
	ribDump maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList],
	neighbours neigh.NexthopCacheView,
) (FIB, FIBBuildStats) {
	var stats FIBBuildStats

	entries := make([]FIBEntry, 0)

	for prefixLen := range ribDump {
		for prefix, routesList := range ribDump[prefixLen] {
			stats.TotalPrefixes++
			if len(routesList.Routes) == 0 {
				stats.SkippedPrefixes++
				continue
			}

			stats.TotalRoutes += len(routesList.Routes)

			local := make([]rib.Route, 0, len(routesList.Routes))
			for _, r := range routesList.Routes {
				if _, ok := neighbours.Lookup(r.NextHop.Unmap()); !ok {
					stats.NeighbourNotFound++
					continue
				}

				local = append(local, r)
			}

			if len(local) == 0 {
				continue
			}

			// The best route of each source is chosen among the resolvable
			// routes only, so the gateway falls back to a reachable route
			// when its source's best one has no neighbour.
			localList := rib.RoutesList{Routes: local}
			bestRoutes := localList.BestPerSource()
			stats.FilteredRoutes += len(local) - len(bestRoutes)

			nexthops := make([]neigh.HardwareRoute, 0, len(bestRoutes))
			for _, r := range bestRoutes {
				entry, _ := neighbours.Lookup(r.NextHop.Unmap())
				nexthops = append(nexthops, entry.HardwareRoute)
			}

			slices.SortFunc(nexthops, neigh.HardwareRoute.Compare)
			nexthops = slices.Compact(nexthops)

			entries = append(entries, FIBEntry{
				Prefix:   prefix,
				Nexthops: nexthops,
			})
			stats.PrefixesAdded++
			stats.HardwareRoutes += len(nexthops)
		}
	}

	return FIB{Entries: entries}, stats
}
