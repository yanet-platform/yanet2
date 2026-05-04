package operator

import (
	"net/netip"

	"github.com/yanet-platform/yanet2/agents/yanet-route-operator/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/agents/yanet-route-operator/internal/rib"
	"github.com/yanet-platform/yanet2/common/go/bitset"
	"github.com/yanet-platform/yanet2/common/go/maptrie"
)

// FIBNexthop describes a hardware-level nexthop (resolved neighbour).
type FIBNexthop struct {
	// SourceMAC is the local interface MAC.
	SourceMAC [6]byte
	// DestinationMAC is the next-hop MAC.
	DestinationMAC [6]byte
	// Device is the egress interface name.
	Device string
}

// FIBEntry describes a single FIB prefix and its ECMP nexthops.
type FIBEntry struct {
	// Prefix is the destination network.
	Prefix netip.Prefix
	// Nexthops are the resolved hardware routes for the prefix. The slice
	// is deduplicated.
	Nexthops []FIBNexthop
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
// produces a deduplicated FIB. The function is pure: no shared memory
// is touched and no errors are returned because every individual route
// resolution failure is best-effort (recorded in stats).
func BuildFIB(
	ribDump maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList],
	neighbours neigh.NexthopCacheView,
) (FIB, FIBBuildStats) {
	var stats FIBBuildStats

	// Track hardware-route uniqueness so the resulting FIB nexthops are
	// deduplicated per prefix using TinyBitset semantics, mirroring the
	// behaviour of the legacy backend.
	hardwareIndex := map[neigh.HardwareRoute]uint32{}
	hardwareSlice := []neigh.HardwareRoute{}
	entries := make([]FIBEntry, 0)

	for prefixLen := range ribDump {
		for prefix, routesList := range ribDump[prefixLen] {
			stats.TotalPrefixes++
			if len(routesList.Routes) == 0 {
				stats.SkippedPrefixes++
				continue
			}

			stats.TotalRoutes += len(routesList.Routes)

			key := bitset.TinyBitset{}
			for _, route := range routesList.Routes {
				entry, ok := neighbours.Lookup(route.NextHop.Unmap())
				if !ok {
					stats.NeighbourNotFound++
					continue
				}
				idx, ok := hardwareIndex[entry.HardwareRoute]
				if !ok {
					idx = uint32(len(hardwareSlice))
					hardwareIndex[entry.HardwareRoute] = idx
					hardwareSlice = append(hardwareSlice, entry.HardwareRoute)
					stats.HardwareRoutes++
				}
				key.Insert(idx)
			}

			if key.Count() == 0 {
				continue
			}

			indices := key.AsSlice()
			nexthops := make([]FIBNexthop, 0, len(indices))
			for _, idx := range indices {
				hr := hardwareSlice[idx]
				nexthops = append(nexthops, FIBNexthop{
					SourceMAC:      hr.SourceMAC,
					DestinationMAC: hr.DestinationMAC,
					Device:         hr.Device,
				})
			}

			entries = append(entries, FIBEntry{
				Prefix:   prefix,
				Nexthops: nexthops,
			})
			stats.PrefixesAdded++
		}
	}

	return FIB{Entries: entries}, stats
}
