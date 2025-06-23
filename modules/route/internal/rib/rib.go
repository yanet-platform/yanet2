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
	mu               sync.RWMutex
	routes           MapTrie[netip.Prefix, netip.Addr, RoutesList]
	changedAt        *atomic.Int64
	currentSessionId *atomic.Uint64 // Monotonically increasing ID for BIRD import sessions
	// sessionTerminator points to a flag signaling the active FeedRIB stream to terminate;
	// swapped on NewSession to invalidate the previous stream.
	sessionTerminator *atomic.Pointer[atomic.Bool]
	log               *zap.SugaredLogger
}

func NewRIB(log *zap.SugaredLogger) *RIB {
	changedAt := atomic.Int64{}
	changedAt.Store(time.Now().UnixNano())
	sessionTerminator := &atomic.Pointer[atomic.Bool]{}
	sessionTerminator.Store(&atomic.Bool{})
	return &RIB{
		routes:            NewMapTrie[netip.Prefix, netip.Addr, RoutesList](1024),
		changedAt:         &changedAt,
		currentSessionId:  &atomic.Uint64{},
		sessionTerminator: sessionTerminator,
		log:               log,
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
	m.update(routes...)
	m.mu.Unlock()
	m.changedAt.Store(time.Now().UnixNano())
}
func (m *RIB) update(routes ...Route) {
	for _, route := range routes {
		if route.ToRemove {
			m.routes.UpdateOrDelete(
				route.Prefix,
				func(m RoutesList) (RoutesList, bool) {
					m.Remove(route)
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
}

func (m *RIB) UpdatedAt() time.Time {
	return time.Unix(0, m.changedAt.Load())
}

// NewSession generates a unique ID for a new BIRD import stream and provides its termination flag.
// Crucially, it also signals the *previous* stream (if any) to terminate by setting its flag.
// This ensures only one import stream actively updates a RIB for a given source.
func (m *RIB) NewSession() (uint64, *atomic.Bool) {
	id := m.currentSessionId.Add(1)
	newSessionTerminator := &atomic.Bool{}
	// Atomically replace the RIB's sessionTerminator with the new one, getting the old.
	oldSessionTerminator := m.sessionTerminator.Swap(newSessionTerminator)
	// Signal the previous stream, identified by oldSessionTerminator, to stop.
	oldSessionTerminator.Store(true)
	return id, newSessionTerminator
}

// CleanupTask removes stale BIRD routes (those with sessionID <= provided sessionID) after a TTL.
// It's launched when a BIRD import stream ends, targeting routes from that now-defunct session.
// The 'quit' channel allows for early termination, e.g., on service shutdown.
func (m *RIB) CleanupTask(sessionID uint64, quit chan bool, ttl time.Duration) {
	timeout := time.After(ttl)
	select {
	case <-quit:
		return
	case <-timeout:
	}

	changed := false
	m.mu.Lock()
	defer func() {
		m.mu.Unlock()
		if changed {
			m.changedAt.Store(time.Now().UnixNano())
		}
	}()

	for _, routeList := range m.routes.Dump() {
		select {
		case <-quit:
			return
		default:
		}

		for _, route := range routeList.Routes {
			if route.SourceID == RouteSourceBird && route.SessionID <= sessionID {
				changed = true
				route.ToRemove = true
			}
		}
		m.update(routeList.Routes...)
	}
}
