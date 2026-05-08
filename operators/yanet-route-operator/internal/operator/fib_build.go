package operator

import (
	"net/netip"
	"slices"

	"github.com/yanet-platform/yanet2/operators/yanet-route-operator/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/operators/yanet-route-operator/internal/rib"
	"github.com/yanet-platform/yanet2/common/go/maptrie"
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

			nexthops := make([]neigh.HardwareRoute, 0, len(routesList.Routes))
			for _, r := range routesList.Routes {
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
