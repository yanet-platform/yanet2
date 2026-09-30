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
	mu     sync.RWMutex
	routes maptrie.MapTrie[netip.Prefix, netip.Addr, RoutesList]
	stats  *RIBStats
	// sessionID is the id of the active BIRD import stream: the greatest id
	// NewSession has handed out, or zero before the first stream.
	sessionID atomic.Uint64
	log       *zap.Logger
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

	return &RIB{
		routes: maptrie.NewMapTrie[netip.Prefix, netip.Addr, RoutesList](1024),
		stats:  NewRIBStats(),
		log:    opts.Log,
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

// Update stamps each route with sessionID and applies it to the RIB,
// reporting whether the write was accepted.
//
// Only the active session's id is accepted, never zero; a rejected write
// leaves the RIB unmutated. The check and the mutation share one locked
// section, so a supersession landing right after the check still passed is
// harmless: the successor's own write must take the same lock, so this write
// always linearizes before any route the successor applies — which then
// either overwrites the same identity or is removed by a later cleanup pass.
func (m *RIB) Update(sessionID uint64, routes ...Route) bool {
	m.mu.Lock()
	if !m.IsActive(sessionID) {
		m.mu.Unlock()
		return false
	}
	m.update(sessionID, routes...)
	m.mu.Unlock()

	m.stats.OnChanged()
	return true
}

// update applies the given routes to the RIB, inserting each one or removing
// it when it is marked as withdrawn. The caller must hold the write lock.
//
// Each route is stamped with sessionID before it is stored or matched for
// removal, so the RIB's own bookkeeping — not whatever the caller happened
// to set — is the source of truth for which session a stored route belongs
// to. The routes must not share storage with anything the RIB already
// holds: a removal rewrites the path list of the affected entry, and a
// caller passing that same list would have it change underneath the walk.
func (m *RIB) update(sessionID uint64, routes ...Route) {
	for _, route := range routes {
		route.SessionID = sessionID
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

// NewSession starts a BIRD import stream and returns its id, superseding the
// previous stream.
//
// Allocating the id and making it the active one is a single atomic
// increment, so the active id is always the greatest id handed out, however
// the calls interleave, and starting a stream never waits behind a cleanup or
// dump pass holding the write lock. A superseded stream is never signaled
// directly; it learns its fate only when Update or IsActive later reports
// that id as no longer active.
func (m *RIB) NewSession() uint64 {
	return m.sessionID.Add(1)
}

// IsActive reports whether sessionID currently names the active BIRD
// import stream, never true for id zero.
//
// It is advisory only, a lock-free early exit for a cheap liveness check
// such as a flush event: whether a route write actually lands is decided
// solely by Update's own fence, under the write lock, which this method
// never takes.
func (m *RIB) IsActive(sessionID uint64) bool {
	return sessionID != 0 && m.sessionID.Load() == sessionID
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
