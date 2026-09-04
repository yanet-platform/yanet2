package route

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// Update tracks application of one committed route snapshot.
type Update struct {
	done       chan struct{}
	once       sync.Once
	err        error
	onComplete func(error)
}

func newUpdate(onComplete func(error)) *Update {
	return &Update{done: make(chan struct{}), onComplete: onComplete}
}

// Complete records the first kernel-application result for this snapshot.
func (m *Update) Complete(err error) {
	if m == nil {
		return
	}
	m.once.Do(func() {
		onComplete := m.onComplete
		m.onComplete = nil
		if onComplete != nil {
			onComplete(err)
		}
		m.err = err
		close(m.done)
	})
}

// Wait blocks until this snapshot is applied or ctx is cancelled.
func (m *Update) Wait(ctx context.Context) error {
	if m == nil {
		return errors.New("wait for route update: update is nil")
	}
	select {
	case <-m.done:
		return m.err
	default:
	}
	select {
	case <-m.done:
		return m.err
	case <-ctx.Done():
		select {
		case <-m.done:
			return m.err
		default:
			return ctx.Err()
		}
	}
}

// Route describes one static route and its host-network output.
type Route struct {
	Prefix    netip.Prefix
	Nexthop   netip.Addr
	Interface string
}

// Store holds the complete desired static-route snapshot.
type Store struct {
	mu          sync.RWMutex
	routes      []Route
	initialized bool
	update      *Update
	applied     storeSnapshot
	generation  uint64
	settled     uint64
	wake        chan struct{}
}

type storeSnapshot struct {
	Routes      []Route
	Initialized bool
	Update      *Update
	Generation  uint64
}

// NewStore creates an empty route store with a coalescing wake channel.
func NewStore() *Store {
	return &Store{wake: make(chan struct{}, 1)}
}

// Replace validates and atomically installs a complete route snapshot.
//
// The input is copied before validation, so later caller changes cannot alter
// either the validated candidate or the committed state.
func (m *Store) Replace(routes []Route) error {
	update, err := m.ReplaceTracked(routes)
	if err != nil {
		return err
	}
	update.Complete(nil)
	return nil
}

// ReplaceTracked atomically installs a snapshot and returns its application
// tracker.
func (m *Store) ReplaceTracked(routes []Route) (*Update, error) {
	candidate := append([]Route(nil), routes...)
	if err := validateRoutes(candidate); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.generation++
	generation := m.generation
	var update *Update
	update = newUpdate(func(err error) {
		m.completeUpdate(update, candidate, generation, err)
	})

	m.routes = candidate
	m.initialized = true
	m.update = update
	m.notify()
	m.mu.Unlock()
	return update, nil
}

// Initialized reports whether the store has received its first complete
// snapshot. An empty replacement is initialized and means "delete all".
func (m *Store) Initialized() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.initialized
}

// Snapshot returns an independent copy of the current route snapshot.
func (m *Store) Snapshot() []Route {
	snapshot, _, _ := m.SnapshotUpdate()
	return snapshot
}

// SnapshotState returns routes and initialization from one locked observation.
func (m *Store) SnapshotState() ([]Route, bool) {
	snapshot, initialized, _ := m.SnapshotUpdate()
	return snapshot, initialized
}

// SnapshotUpdate returns route state and its application tracker from one
// locked observation.
func (m *Store) SnapshotUpdate() ([]Route, bool, *Update) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snapshot := make([]Route, len(m.routes))
	copy(snapshot, m.routes)
	return snapshot, m.initialized, m.update
}

// Wake returns the buffered channel signalled after each successful replace.
func (m *Store) Wake() <-chan struct{} {
	return m.wake
}

// Notify asks the reconciler to refresh external state without changing the
// current route snapshot.
func (m *Store) Notify() {
	m.notify()
}

func (m *Store) notify() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Store) completeUpdate(
	update *Update,
	routes []Route,
	generation uint64,
	applyErr error,
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if generation < m.settled {
		// Any stale apply or rollback may have changed the kernel after a newer
		// result was settled. Reconcile the current snapshot again.
		m.notify()
		return
	}
	m.settled = generation
	if applyErr == nil {
		if generation >= m.applied.Generation {
			m.applied = storeSnapshot{
				Routes:      append([]Route(nil), routes...),
				Initialized: true,
				Update:      update,
				Generation:  generation,
			}
		}
		if m.update != update {
			m.notify()
		}
		return
	}
	if m.update != update {
		m.notify()
		return
	}
	m.routes = append([]Route(nil), m.applied.Routes...)
	m.initialized = m.applied.Initialized
	m.update = m.applied.Update
	m.notify()
}

func validateRoutes(routes []Route) error {
	seen := map[Route]int{}
	multipathSizes := map[netip.Prefix]int{}
	for idx, route := range routes {
		if err := validateRoute(route); err != nil {
			return fmt.Errorf("route %d: %w", idx, err)
		}
		if previous, duplicate := seen[route]; duplicate {
			return fmt.Errorf("route %d duplicates route %d", idx, previous)
		}
		seen[route] = idx

		// RTA_MULTIPATH stores its total length in a uint16. Validate before a
		// reconcile can delete the route that an oversized replacement targets.
		gatewayAttributeSize := align4(unix.SizeofRtAttr + route.Nexthop.BitLen()/8)
		nexthopSize := align4(unix.SizeofRtNexthop + gatewayAttributeSize)
		multipathSizes[route.Prefix] += nexthopSize
		if multipathSizes[route.Prefix] > int(^uint16(0))-unix.SizeofRtAttr {
			return fmt.Errorf(
				"routes for prefix %q exceed the Linux multipath attribute limit",
				route.Prefix,
			)
		}
	}
	return nil
}

func align4(size int) int {
	return (size + 3) &^ 3
}

func validateRoute(route Route) error {
	if !route.Prefix.IsValid() {
		return fmt.Errorf("prefix %q is invalid", route.Prefix)
	}
	if route.Prefix.Addr().Zone() != "" {
		return fmt.Errorf("prefix %q has a zone", route.Prefix)
	}
	if route.Prefix.Addr().Is4In6() {
		return fmt.Errorf("prefix %q is an IPv4-mapped IPv6 prefix", route.Prefix)
	}
	if route.Prefix != route.Prefix.Masked() {
		return fmt.Errorf("prefix %q is not masked", route.Prefix)
	}
	if !route.Nexthop.IsValid() {
		return fmt.Errorf("nexthop %q is invalid", route.Nexthop)
	}
	if route.Nexthop.Zone() != "" {
		return fmt.Errorf("nexthop %q has a zone", route.Nexthop)
	}
	if route.Nexthop.Is4In6() {
		return fmt.Errorf("nexthop %q is an IPv4-mapped IPv6 address", route.Nexthop)
	}
	if route.Interface == "" {
		return errors.New("interface is empty")
	}
	if len(route.Interface) >= unix.IFNAMSIZ {
		return fmt.Errorf("interface %q exceeds Linux IFNAMSIZ", route.Interface)
	}
	if strings.ContainsRune(route.Interface, '\x00') {
		return fmt.Errorf("interface %q contains a NUL byte", route.Interface)
	}
	if route.Prefix.Addr().BitLen() != route.Nexthop.BitLen() {
		return fmt.Errorf(
			"prefix %q and nexthop %q use different address families",
			route.Prefix,
			route.Nexthop,
		)
	}
	return nil
}
