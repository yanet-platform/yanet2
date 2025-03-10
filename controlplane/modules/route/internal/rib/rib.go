package rib

import (
	"fmt"
	"net"
	"net/netip"
	"sync"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/discovery/neigh"
)

type RIB struct {
	mu         sync.RWMutex
	routes     MapTrie[netip.Prefix, netip.Addr, RoutesList]
	neighbours *neigh.NexthopCache
	log        *zap.SugaredLogger
}

func NewRIB(neighbours *neigh.NexthopCache, log *zap.SugaredLogger) *RIB {
	return &RIB{
		routes:     NewMapTrie[netip.Prefix, netip.Addr, RoutesList](1024),
		neighbours: neighbours,
		log:        log,
	}
}

func (m *RIB) AddUnicastRoute(prefix netip.Prefix, nexthopAddr netip.Addr) error {
	m.log.Debugf("adding unicast route %q via %q", prefix, nexthopAddr)

	// Obtain neighbor entry with resolved hardware addresses
	neighbours := m.neighbours.View()
	entry, ok := neighbours.Lookup(nexthopAddr)
	if !ok {
		return fmt.Errorf("neighbour with %q nexthop IP address not found", nexthopAddr)
	}

	m.log.Debugw("found neighbour with resolved hardware addresses",
		zap.Stringer("nexthop_addr", nexthopAddr),
		zap.Stringer("nexthop_hardware_addr", entry.LinkAddr),
		zap.Stringer("hardware_addr", entry.HardwareAddr),
	)

	route := Route{
		Prefix: prefix,
	}

	// Now we can directly use the hardware addresses from the entry
	copy(route.SourceMAC[:], entry.HardwareAddr)
	copy(route.DestinationMAC[:], entry.LinkAddr)

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
		zap.Stringer("hardware_addr", entry.HardwareAddr),
		zap.Stringer("nexthop_hardware_addr", entry.LinkAddr),
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
