package link

import (
	"context"
	"fmt"

	"github.com/vishvananda/netlink"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/controlplane/modules/route/internal/discovery"
)

// LinksCache is a cache of netlink links.
type LinksCache = discovery.Cache[int, netlink.LinkAttrs]

// LinksCacheView is a read-only view of the links cache.
type LinksCacheView = discovery.CacheView[int, netlink.LinkAttrs]

// Option is a function that configures the link monitor.
type Option func(*options)

// WithLog configures the link monitor with a logger.
func WithLog(log *zap.SugaredLogger) Option {
	return func(o *options) {
		o.Log = log
	}
}

type options struct {
	Log *zap.SugaredLogger
}

func newOptions() *options {
	return &options{
		Log: zap.NewNop().Sugar(),
	}
}

// LinkMonitor is a monitor of netlink links.
type LinkMonitor struct {
	cache *LinksCache
	log   *zap.SugaredLogger
}

// NewLinkMonitor creates a new link monitor.
func NewLinkMonitor(cache *LinksCache, options ...Option) *LinkMonitor {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	m := &LinkMonitor{
		cache: cache,
		log:   opts.Log,
	}

	// Bootstrap synchronously here.
	m.update()
	return m
}

// Run runs the link monitor until the specified context is canceled.
func (m *LinkMonitor) Run(ctx context.Context) error {
	m.log.Debugf("starting links monitor")
	defer m.log.Debugf("stopped links monitor")

	wg, ctx := errgroup.WithContext(ctx)
	wg.Go(func() error {
		return m.runSubscription(ctx)
	})

	return wg.Wait()
}

func (m *LinkMonitor) runSubscription(ctx context.Context) error {
	txRx := make(chan netlink.LinkUpdate, 1)
	opts := netlink.LinkSubscribeOptions{}
	if err := netlink.LinkSubscribeWithOptions(txRx, ctx.Done(), opts); err != nil {
		return fmt.Errorf("failed to subscribe to links updates: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-txRx:
			if err := m.update(); err != nil {
				m.log.Warnw("failed to process link update", zap.Error(err))
			}
		}
	}
}

func (m *LinkMonitor) update() error {
	links, err := netlink.LinkList()
	if err != nil {
		return fmt.Errorf("failed to list links: %w", err)
	}

	cache := map[int]netlink.LinkAttrs{}
	for _, link := range links {
		attrs := link.Attrs()
		cache[attrs.Index] = *attrs
	}

	// Swap the entire table atomically.
	m.cache.Swap(cache)

	m.log.Infof("updated links cache")

	return nil
}
