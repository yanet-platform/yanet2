package operator

import (
	"net/netip"
	"slices"

	"github.com/yanet-platform/yanet2/common/go/maptrie"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
	"github.com/yanet-platform/yanet2/operators/route/neigh"
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
	TotalPrefixes   int
	TotalRoutes     int
	SkippedPrefixes int
	// NeighbourNotFound counts route records whose nexthops are absent from
	// the full neighbour snapshot; their device ownership is unknown.
	NeighbourNotFound int
	// DeviceExcludedRoutes counts resolved route records on devices outside
	// the gateway's non-empty device restriction.
	DeviceExcludedRoutes int
	HardwareRoutes       int
	PrefixesAdded        int
	// FilteredRoutes counts eligible routes dropped because a better route
	// of the same source exists.
	FilteredRoutes int
}

// BuildFIB resolves a RIB dump against the complete neighbour snapshot and
// produces a deduplicated FIB for the gateway's devices.
//
// Missing neighbours and resolved paths on excluded devices are counted
// separately before best-per-source selection. Only locally eligible routes
// compete, allowing fallback to a reachable lower-priority path.
func BuildFIB(
	ribDump maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList],
	neighbours neigh.NexthopCacheView,
	devices neigh.DeviceScope,
) (FIB, FIBBuildStats) {
	var stats FIBBuildStats

	entries := make([]FIBEntry, 0)

	// The wire applies entries in list order, each overwriting the addresses
	// it covers, so emitting least-specific first reproduces longest-prefix-match.
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
				entry, ok := neighbours.Lookup(r.NextHop.Unmap())
				if !ok {
					stats.NeighbourNotFound++
					continue
				}
				if !devices.Allows(entry.HardwareRoute.Device) {
					stats.DeviceExcludedRoutes++
					continue
				}

				local = append(local, r)
			}

			if len(local) == 0 {
				continue
			}

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
