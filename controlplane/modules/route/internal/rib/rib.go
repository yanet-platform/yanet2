package rib

import (
	"fmt"
	"net"
	"net/netip"
	"sync"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/discovery/link"
	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/discovery/neigh"
)

type RIB struct {
	mu         sync.RWMutex
	routes     MapTrie[netip.Prefix, netip.Addr, RoutesList]
	neighbours *neigh.NexthopCache
	links      *link.LinksCache
	log        *zap.SugaredLogger
}

func NewRIB(neighbours *neigh.NexthopCache, links *link.LinksCache, log *zap.SugaredLogger) *RIB {
	return &RIB{
		routes:     NewMapTrie[netip.Prefix, netip.Addr, RoutesList](1024),
		neighbours: neighbours,
		links:      links,
		log:        log,
	}
}

func (m *RIB) AddUnicastRoute(prefix netip.Prefix, nexthopAddr netip.Addr) error {
	m.log.Debugf("adding unicast route %q via %q", prefix, nexthopAddr)

	// Obtain destination MAC address using neighbours table.
	neighbours := m.neighbours.View()
	neigh, ok := neighbours.Lookup(nexthopAddr)
	if !ok {
		return fmt.Errorf("neighbour with %q nexthop IP address not found", nexthopAddr)
	}
	if len(neigh.HardwareAddr) != 6 {
		return fmt.Errorf("unsupported MAC address %q: must be EUI-48", neigh.HardwareAddr)
	}

	m.log.Debugw("found neighbour",
		zap.Int("link_index", neigh.LinkIndex),
		zap.Stringer("nexthop_addr", nexthopAddr),
		zap.Stringer("nexthop_hardware_addr", neigh.HardwareAddr),
	)

	// ... and source MAC using links cache.
	links := m.links.View()
	link, ok := links.Lookup(neigh.LinkIndex)
	if !ok {
		return fmt.Errorf("link with %q index not found", neigh.LinkIndex)
	}
	if len(link.HardwareAddr) != 6 {
		return fmt.Errorf("unsupported MAC address %q: must be EUI-48", link.HardwareAddr)
	}

	m.log.Debugw("found local interface for neighbour",
		zap.Int("link_index", link.Index),
		zap.Stringer("hardware_addr", link.HardwareAddr),
	)

	route := Route{
		Prefix: prefix,
	}

	// Safe, because we've checked for MAC address format earlier.
	copy(route.SourceMAC[:], link.HardwareAddr)
	copy(route.DestinationMAC[:], neigh.HardwareAddr)

	m.mu.Lock()
	m.routes.InsertOrUpdate(
		prefix,
		func() RoutesList {
			return RoutesList{
				Routes: []Route{route},
			}
		},
		func(m RoutesList) RoutesList {
			// WIP(sakateka): FIXME: deduplicate routes
			m.Routes = append(m.Routes, route)
			return m
		},
	)
	m.mu.Unlock()

	m.log.Infow("added unicast route",
		zap.Stringer("prefix", prefix),
		zap.Stringer("nexthop_addr", nexthopAddr),
		zap.Stringer("hardware_addr", link.HardwareAddr),
		zap.Stringer("nexthop_hardware_addr", neigh.HardwareAddr),
	)

	return nil
}

func (m *RIB) DumpRoutes() map[netip.Prefix]RoutesList {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.routes.Dump()
}

type Route struct {
	netip.Prefix
	NextHop netip.Addr
	// Temporary placeholder
	HardwareRoute
}

// HardwareRoute is a hashable pair of MAC addresses.
type HardwareRoute struct {
	SourceMAC      [6]byte
	DestinationMAC [6]byte
}

func (m HardwareRoute) String() string {
	return fmt.Sprintf("%s -> %s", net.HardwareAddr(m.SourceMAC[:]), net.HardwareAddr(m.DestinationMAC[:]))
}

type RoutesList struct {
	Routes []Route
}
