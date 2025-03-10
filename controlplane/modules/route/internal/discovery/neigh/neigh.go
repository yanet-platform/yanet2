package neigh

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/vishvananda/netlink"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"

	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/discovery"
)

// NexthopCache is a cache of nexthops that is populated via neighbour
// discovery.
type NexthopCache = discovery.Cache[netip.Addr, NeighbourEntry]

// NexthopCacheView is a read-only view of the nexthop cache.
type NexthopCacheView = discovery.CacheView[netip.Addr, NeighbourEntry]

// Option is a function that configures the neighbour monitor.
type Option func(*options)

// WithUpdateInterval configures the neighbour monitor with an force-update
// interval.
func WithUpdateInterval(interval time.Duration) Option {
	return func(o *options) {
		o.UpdateInterval = interval
	}
}

// WithLog configures the neighbour monitor with a logger.
func WithLog(log *zap.SugaredLogger) Option {
	return func(o *options) {
		o.Log = log
	}
}

type options struct {
	UpdateInterval time.Duration
	Log            *zap.SugaredLogger
}

func newOptions() *options {
	return &options{
		UpdateInterval: 5 * time.Minute,
		Log:            zap.NewNop().Sugar(),
	}
}

// NeighMonitor is a monitor of neighbour events.
//
// It populates the nexthop cache with discovered neighbours both reactively
// and periodically.
type NeighMonitor struct {
	nexthopCache   *NexthopCache
	updateInterval time.Duration
	log            *zap.SugaredLogger
}

// NewNeighMonitor creates a new neighbour monitor.
func NewNeighMonitor(neighbours *NexthopCache, options ...Option) *NeighMonitor {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	m := &NeighMonitor{
		nexthopCache:   neighbours,
		updateInterval: opts.UpdateInterval,
		log:            opts.Log,
	}

	// Bootstrap neighbours synchronously here.
	m.updateNeighbours()
	return m
}

// Cache returns the nexthop cache.
func (m *NeighMonitor) Cache() *NexthopCache {
	return m.nexthopCache
}

// Run runs the neighbour monitor until the specified context is canceled.
func (m *NeighMonitor) Run(ctx context.Context) error {
	m.log.Debugf("starting neighbour monitor")
	defer m.log.Debugf("stopped neighbour monitor")

	wg, ctx := errgroup.WithContext(ctx)
	wg.Go(func() error {
		return m.runNeighSubscription(ctx)
	})
	wg.Go(func() error {
		return m.runNeighPeriodicUpdate(ctx)
	})

	return wg.Wait()
}

func (m *NeighMonitor) runNeighSubscription(ctx context.Context) error {
	txRx := make(chan netlink.NeighUpdate, 1)
	opts := netlink.NeighSubscribeOptions{}
	if err := netlink.NeighSubscribeWithOptions(txRx, ctx.Done(), opts); err != nil {
		return fmt.Errorf("failed to subscribe to neighbor updates: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case update := <-txRx:
			if err := m.processNeighUpdate(update); err != nil {
				m.log.Warnw("failed to process neighbour update", zap.Error(err))
			}
		}
	}
}

func (m *NeighMonitor) runNeighPeriodicUpdate(ctx context.Context) error {
	timer := time.NewTicker(m.updateInterval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if err := m.updateNeighbours(); err != nil {
				m.log.Warnw("failed to update neighbours", zap.Error(err))
			}
		}
	}
}

func (m *NeighMonitor) processNeighUpdate(update netlink.NeighUpdate) error {
	m.log.Debugw("processing neighbour update",
		zap.Int("link_index", update.LinkIndex),
		zap.Stringer("state", NeighbourState(update.State)),
		zap.Stringer("nexthop_addr", update.IP),
		zap.Stringer("nexthop_hardware_addr", update.HardwareAddr),
	)

	switch update.Type {
	case unix.RTM_NEWNEIGH:
		return m.updateNeighbours()
	case unix.RTM_DELNEIGH:
		// We don't process neighbour deletion events to avoid flaps.
		//
		// Instead, the entire neighbors table is overwritten on a timer event.
	default:
		m.log.Warnf("received unexpected neighbour update type: %d", update.Type)
	}

	return nil
}

func (m *NeighMonitor) updateNeighbours() error {
	neighs, err := netlink.NeighList(0, 0)
	if err != nil {
		return fmt.Errorf("failed to list neighbours: %w", err)
	}

	links, err := netlink.LinkList()
	if err != nil {
		return fmt.Errorf("failed to list links: %w", err)
	}

	// Create a map of link indexes to hardware addresses for quick lookup
	linkIndexToHardwareAddr := map[int]net.HardwareAddr{}
	for _, link := range links {
		attrs := link.Attrs()

		// TODO: should be filter out loopback links?

		hardwareAddr := attrs.HardwareAddr
		if hardwareAddr == nil {
			hardwareAddr = make(net.HardwareAddr, 6)
		}

		linkIndexToHardwareAddr[attrs.Index] = hardwareAddr
	}

	// Create the new cache map with resolved hardware addresses
	nexthopCache := make(map[netip.Addr]NeighbourEntry)
	for _, neigh := range neighs {
		nexthopAddr, ok := netip.AddrFromSlice(neigh.IP)
		if !ok {
			m.log.Warnf("failed to parse neighbour IP address: %q", neigh.IP)
			continue
		}

		// Skip entries with invalid MAC.
		if len(neigh.HardwareAddr) != 6 {
			m.log.Warnf("skipping entry with unsupported MAC address %q: must be EUI-48", neigh.HardwareAddr)
			continue
		}

		// Get the hardware address of the interface.
		hardwareAddr, ok := linkIndexToHardwareAddr[neigh.LinkIndex]
		if !ok {
			m.log.Warnf("no hardware address for link index: %d - %#+v", neigh.LinkIndex, neigh)
			continue
		}
		if len(hardwareAddr) != 6 {
			m.log.Warnf("skipping entry with unsupported interface MAC address %q: must be EUI-48", hardwareAddr)
			continue
		}

		// Create the entry with resolved hardware addresses
		entry := NeighbourEntry{
			NextHop:      nexthopAddr,
			LinkAddr:     neigh.HardwareAddr,
			HardwareAddr: hardwareAddr,
			UpdatedAt:    time.Now(),
			State:        NeighbourState(neigh.State),
		}

		m.log.Debugw("resolved neighbour entry",
			zap.Stringer("nexthop_addr", entry.NextHop),
			zap.Stringer("nexthop_hardware_addr", entry.LinkAddr),
			zap.Stringer("hardware_addr", entry.HardwareAddr),
			zap.Stringer("state", entry.State),
		)

		nexthopCache[nexthopAddr] = entry
	}

	// Swap the entire table atomically.
	m.nexthopCache.Swap(nexthopCache)

	m.log.Infow("updated nexthop cache", zap.Int("size", len(nexthopCache)))
	return nil
}
