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
	AmbiguousNextHops int
	HardwareRoutes    int
	PrefixesAdded     int
	// FilteredRoutes counts eligible routes dropped because a better route
	// of the same source exists.
	FilteredRoutes int
}

// BuildFIB resolves a RIB dump against the supplied neighbour view and
// produces a deduplicated FIB.
//
// Neighbours are filtered by gateway ownership before equal next hops are
// merged. The best routes per source are chosen
// among the resolvable routes, so a gateway can fall back to a reachable path.
func BuildFIB(
	ribDump maptrie.MapTrie[netip.Prefix, netip.Addr, rib.RoutesList],
	neighbours neigh.TableSnapshot,
	devices []string,
	options ...FIBBuildOption,
) (FIB, FIBBuildStats) {
	configuration := fibBuildOptions{}
	for _, option := range options {
		option(&configuration)
	}
	var stats FIBBuildStats
	view := neighbours.ViewByDevices(devices)
	scopeView := view
	if configuration.ScopeSource != "" {
		scopeView, _ = neighbours.SourceView(configuration.ScopeSource)
	}
	type scopedKey struct {
		Address netip.Addr
		Ifindex uint32
	}
	bindings := map[scopedKey]string{}
	conflicts := map[scopedKey]bool{}
	scopeEntries, _ := scopeView.Entries()
	for entry := range scopeEntries {
		if entry.Ifindex == 0 {
			continue
		}
		key := scopedKey{entry.NextHop.Unmap(), entry.Ifindex}
		if device, found := bindings[key]; found && device != entry.HardwareRoute.Device {
			conflicts[key] = true
		}
		bindings[key] = entry.HardwareRoute.Device
	}
	resolved := map[netip.Addr]neigh.NeighbourEntry{}
	ambiguous := map[netip.Addr]bool{}
	neighbourEntries, _ := view.Entries()
	for entry := range neighbourEntries {
		if _, duplicate := resolved[entry.NextHop]; duplicate {
			ambiguous[entry.NextHop] = true
		}
		resolved[entry.NextHop] = entry
	}
	for address := range ambiguous {
		delete(resolved, address)
	}
	resolve := func(route rib.Route) (neigh.NeighbourEntry, bool) {
		if route.Ifindex != 0 {
			key := scopedKey{route.NextHop.Unmap(), route.Ifindex}
			device, found := bindings[key]
			if !found || conflicts[key] {
				return neigh.NeighbourEntry{}, false
			}
			return view.Lookup(neigh.NewKey(route.NextHop, device))
		}
		entry, found := resolved[route.NextHop.Unmap()]
		return entry, found
	}

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
			for _, route := range routesList.Routes {
				if _, ok := resolve(route); !ok {
					stats.NeighbourNotFound++
					if route.Ifindex == 0 && ambiguous[route.NextHop.Unmap()] {
						stats.AmbiguousNextHops++
					}
					continue
				}

				local = append(local, route)
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
			for _, route := range bestRoutes {
				entry, _ := resolve(route)
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

type fibBuildOptions struct{ ScopeSource string }

// FIBBuildOption selects the namespace provenance used for explicit route scope.
type FIBBuildOption func(*fibBuildOptions)

// WithFIBScopeSource retains observed scope beneath static neighbour overrides.
func WithFIBScopeSource(source string) FIBBuildOption {
	return func(options *fibBuildOptions) { options.ScopeSource = source }
}
