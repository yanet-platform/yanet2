package rib

import (
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/common/go/maptrie"
)

type RIB struct {
	mu               sync.RWMutex
	routes           maptrie.MapTrie[netip.Prefix, netip.Addr, RoutesList]
	stats            *RIBStats
	currentSessionId *atomic.Uint64 // Monotonically increasing ID for BIRD import sessions
	// sessionTerminator points to a flag signaling the active FeedRIB stream to terminate;
	// swapped on NewSession to invalidate the previous stream.
	sessionTerminator *atomic.Pointer[atomic.Bool]
	log               *zap.Logger
}

// Option configures the NewRIB constructor.
type Option func(*ribOptions)

type ribOptions struct {
	Log *zap.Logger
}

func newRIBOptions() *ribOptions {
	return &ribOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the RIB.
func WithLog(log *zap.Logger) Option {
	return func(o *ribOptions) {
		o.Log = log
	}
}

func NewRIB(options ...Option) *RIB {
	opts := newRIBOptions()
	for _, o := range options {
		o(opts)
	}

	sessionTerminator := &atomic.Pointer[atomic.Bool]{}
	sessionTerminator.Store(&atomic.Bool{})

	return &RIB{
		routes:            maptrie.NewMapTrie[netip.Prefix, netip.Addr, RoutesList](1024),
		stats:             NewRIBStats(),
		currentSessionId:  &atomic.Uint64{},
		sessionTerminator: sessionTerminator,
		log:               opts.Log,
	}
}

func (m *RIB) AddUnicastRoute(prefix netip.Prefix, nexthopAddr netip.Addr, sourceID RouteSourceID) error {
	route := Route{
		Prefix:    prefix,
		NextHop:   nexthopAddr,
		Peer:      netip.IPv6Unspecified(),
		SourceID:  sourceID,
		UpdatedAt: time.Now(),
	}

	m.mu.Lock()
	m.routes.InsertOrUpdate(
		route.Prefix,
		func() RoutesList {
			m.stats.OnPrefixAdded()
			m.stats.OnRouteAdded(1)
			return RoutesList{
				Routes: []Route{route},
			}
		},
		func(rl RoutesList) RoutesList {
			if rl.Insert(route) {
				m.stats.OnRouteAdded(1)
			}
			return rl
		},
	)
	m.mu.Unlock()
	m.stats.OnChanged()

	m.log.Info("RIB: added unicast route",
		zap.Stringer("prefix", prefix),
		zap.Stringer("nexthop", nexthopAddr),
	)

	return nil
}

func (m *RIB) RemoveUnicastRoute(prefix netip.Prefix, nexthopAddr netip.Addr, sourceID RouteSourceID) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	candidate := Route{
		Prefix:   prefix,
		NextHop:  nexthopAddr,
		Peer:     netip.IPv6Unspecified(),
		SourceID: sourceID,
	}

	found := 0
	m.routes.UpdateOrDelete(
		prefix,
		func(routesList RoutesList) (RoutesList, bool) {
			newRoutes := make([]Route, 0, len(routesList.Routes))
			for _, r := range routesList.Routes {
				if r.IsSameIdentity(candidate) {
					found++
					continue // skip means remove
				}
				newRoutes = append(newRoutes, r)
			}
			routesList.Routes = newRoutes
			// Delete the prefix entry if no routes remain.
			isEmpty := len(routesList.Routes) == 0
			if isEmpty {
				m.stats.OnPrefixRemoved()
			}
			return routesList, isEmpty
		},
	)

	if found > 0 {
		m.stats.OnRouteRemoved(found)
		m.stats.OnChanged()
		m.log.Info("RIB: removed unicast route",
			zap.Stringer("prefix", prefix),
			zap.Stringer("nexthop", nexthopAddr),
			zap.Uint8("source", uint8(sourceID)),
			zap.Int("count", found),
		)
	} else {
		m.log.Warn("RIB: route not found for removal",
			zap.Stringer("prefix", prefix),
			zap.Stringer("nexthop", nexthopAddr),
			zap.Uint8("source", uint8(sourceID)),
		)
	}

	return nil
}

func (m *RIB) DumpRoutes() maptrie.MapTrie[netip.Prefix, netip.Addr, RoutesList] {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Since `RoutesList` is passed by value, there's no need to create a
	// separate copy of it. However, since the `Routes` member within
	// the struct is a reference like type (slice), we need to replace it.
	dump := m.routes.Clone()
	for idx := range dump {
		for key := range dump[idx] {
			dump[idx][key] = RoutesList{
				// replace with a copy of the routes slice to avoid sharing data
				Routes: slices.Clone(dump[idx][key].Routes),
			}
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
	m.stats.OnChanged()
}

// update applies the given routes to the RIB, inserting each one or removing
// it when it is marked as withdrawn. The caller must hold the write lock.
//
// The routes must not share storage with anything the RIB already holds: a
// removal rewrites the path list of the affected entry, and a caller passing
// that same list would have it change underneath the walk.
func (m *RIB) update(routes ...Route) {
	for _, route := range routes {
		if route.ToRemove {
			m.routes.UpdateOrDelete(
				route.Prefix,
				func(rl RoutesList) (RoutesList, bool) {
					if rl.Remove(route) {
						m.stats.OnRouteRemoved(1)
					}
					isEmpty := len(rl.Routes) == 0
					if isEmpty {
						m.stats.OnPrefixRemoved()
					}
					return rl, isEmpty
				},
			)
		} else {
			m.routes.InsertOrUpdate(
				route.Prefix,
				func() RoutesList {
					m.stats.OnPrefixAdded()
					m.stats.OnRouteAdded(1)
					return RoutesList{
						Routes: []Route{route},
					}
				},
				func(rl RoutesList) RoutesList {
					if rl.Insert(route) {
						m.stats.OnRouteAdded(1)
					}
					return rl
				},
			)
		}
	}
}

// Stats returns an O(1) snapshot of RIB counters.
func (m *RIB) Stats() RIBStatsSnapshot {
	return m.stats.Snapshot()
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
		m.log.Info("RIB: cleanup task cancelled before timeout",
			zap.Uint64("sessionID", sessionID),
		)
		return
	case <-timeout:
		m.log.Info("RIB: cleanup task timeout reached, starting cleanup",
			zap.Uint64("sessionID", sessionID),
		)
	}

	removedCount := 0
	m.mu.Lock()
	defer func() {
		m.mu.Unlock()
		if removedCount > 0 {
			m.stats.OnChanged()
		}
	}()

	isStale := func(route Route) bool {
		return route.SourceID == RouteSourceBird && route.SessionID <= sessionID
	}

	// Take the prefixes first so the pass walks a snapshot rather than the
	// structure it rewrites.
	prefixes := make([]netip.Prefix, 0, m.routes.Len())
	for bits := range m.routes {
		for prefix := range m.routes[bits] {
			prefixes = append(prefixes, prefix)
		}
	}

	for _, prefix := range prefixes {
		select {
		case <-quit:
			m.log.Info("RIB: cleanup task interrupted during cleanup",
				zap.Uint64("sessionID", sessionID),
				zap.Int("removedCount", removedCount),
			)
			return
		default:
		}

		m.routes.UpdateOrDelete(prefix, func(rl RoutesList) (RoutesList, bool) {
			stale := 0
			for _, route := range rl.Routes {
				if isStale(route) {
					stale++
				}
			}
			if stale == 0 {
				return rl, false
			}

			removedCount += stale
			m.stats.OnRouteRemoved(stale)

			if stale == len(rl.Routes) {
				m.stats.OnPrefixRemoved()
				return rl, true
			}

			// Give the survivors an array of their own, so that the path list
			// of an entry is never rewritten in place.
			kept := make([]Route, 0, len(rl.Routes)-stale)
			for _, route := range rl.Routes {
				if !isStale(route) {
					kept = append(kept, route)
				}
			}
			return RoutesList{Routes: kept}, false
		})
	}

	m.log.Info("RIB: cleanup task completed",
		zap.Uint64("sessionID", sessionID),
		zap.Int("removedCount", removedCount),
	)
}
