package rib

import (
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

type RIB struct {
	mu        sync.RWMutex
	routes    MapTrie[netip.Prefix, netip.Addr, RoutesList]
	changedAt *atomic.Int64
	log       *zap.SugaredLogger
}

func NewRIB(log *zap.SugaredLogger) *RIB {
	changedAt := atomic.Int64{}
	changedAt.Store(time.Now().UnixNano())
	return &RIB{
		routes:    NewMapTrie[netip.Prefix, netip.Addr, RoutesList](1024),
		changedAt: &changedAt,
		log:       log,
	}
}

func (m *RIB) AddUnicastRoute(prefix netip.Prefix, nexthopAddr netip.Addr) error {
	m.log.Debugf("adding unicast route %q via %q", prefix, nexthopAddr)

	route := Route{
		Prefix:    prefix,
		NextHop:   nexthopAddr,
		SourceID:  RouteSourceStatic,
		UpdatedAt: time.Now(),
	}

	m.mu.Lock()
	m.routes.InsertOrUpdate(
		route.Prefix,
		func() RoutesList {
			return RoutesList{
				Routes: []Route{route},
			}
		},
		func(m RoutesList) RoutesList {
			m.Insert(route)
			return m
		},
	)
	m.mu.Unlock()
	m.changedAt.Store(time.Now().UnixNano())

	m.log.Infow("added unicast route",
		zap.Stringer("prefix", prefix),
		zap.Stringer("nexthop_addr", nexthopAddr),
	)

	return nil
}

func (m *RIB) DumpRoutes() map[netip.Prefix]RoutesList {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Since `RoutesList` is passed by value, there's no need to create a
	// separate copy of it. However, since the `Routes` member within
	// the struct is a reference like type (slice), we need to replace it.
	dump := m.routes.Dump()
	for key := range dump {
		dump[key] = RoutesList{
			// replace with a copy of the routes slice to avoid sharing data
			Routes: slices.Clone(dump[key].Routes),
		}
	}
	return dump
}

func (m *RIB) LongestMatch(addr netip.Addr) (netip.Prefix, RoutesList, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	prefix, list, ok := m.routes.Lookup(addr)
	// replace with a copy of the routes slice to avoid sharing data
	list.Routes = slices.Clone(list.Routes)
	return prefix, list, ok
}

func (m *RIB) Update(routes ...Route) {
	m.mu.Lock()
	for _, route := range routes {
		if route.ToRemove {
			m.routes.UpdateOrDelete(
				route.Prefix,
				func(m RoutesList) (RoutesList, bool) {
					return m, len(m.Routes) == 0
				},
			)
		} else {
			m.routes.InsertOrUpdate(
				route.Prefix,
				func() RoutesList {
					return RoutesList{
						Routes: []Route{route},
					}
				},
				func(m RoutesList) RoutesList {
					m.Insert(route)
					return m
				},
			)
		}
	}
	m.mu.Unlock()
	m.changedAt.Store(time.Now().UnixNano())
}

func (m *RIB) UpdatedAt() time.Time {
	return time.Unix(0, m.changedAt.Load())
}
