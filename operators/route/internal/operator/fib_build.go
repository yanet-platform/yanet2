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
	// FilteredRoutes counts routes excluded by the per-source best-group filter.
	FilteredRoutes int
}

// filterFIBEntries returns the subset of entries whose nexthops include at
// least one device present in the given set.
//
// When the device set is empty every entry is returned unchanged.
func filterFIBEntries(entries []FIBEntry, devices map[string]struct{}) []FIBEntry {
	if len(devices) == 0 {
		return entries
	}

	result := make([]FIBEntry, 0, len(entries))
	for _, entry := range entries {
		kept := make([]neigh.HardwareRoute, 0, len(entry.Nexthops))
		for idx := range entry.Nexthops {
			if _, ok := devices[entry.Nexthops[idx].Device]; ok {
				kept = append(kept, entry.Nexthops[idx])
			}
		}
		if len(kept) == 0 {
			continue
		}
		result = append(result, FIBEntry{
			Prefix:   entry.Prefix,
			Nexthops: kept,
		})
	}
	return result
}

// BuildFIB resolves a RIB dump against the supplied neighbour view and
// produces a deduplicated FIB.
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

			bestRoutes := routesList.BestPerSource()
			stats.FilteredRoutes += len(routesList.Routes) - len(bestRoutes)

			nexthops := make([]neigh.HardwareRoute, 0, len(bestRoutes))
			for _, r := range bestRoutes {
				entry, ok := neighbours.Lookup(r.NextHop.Unmap())
				if !ok {
					stats.NeighbourNotFound++
					continue
				}

				routeHardware := entry.HardwareRoute
				nexthops = append(nexthops, routeHardware)
			}

			if len(nexthops) == 0 {
				continue
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
