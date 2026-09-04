package operator

import (
	"github.com/vishvananda/netlink"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	netreconcile "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netlink"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/route"
)

// NetlinkHandle is the shared kernel handle surface used by all reconcilers.
type NetlinkHandle interface {
	netreconcile.Backend
	route.Backend
	neighbour.Backend
	Close()
}

// NetlinkHandleFactory creates the one shared handle owned by an Operator.
type NetlinkHandleFactory func() (NetlinkHandle, error)

// GatewayConnection supports generated gRPC clients and explicit cleanup.
type GatewayConnection interface {
	grpc.ClientConnInterface
	Close() error
}

// GatewayDialer opens a connection using common gateway configuration.
type GatewayDialer func(commonoperator.GatewayConfig) (GatewayConnection, error)

// LinkReconcilerFactory binds link reconciliation to the shared handle.
type LinkReconcilerFactory func(NetlinkHandle) LinkReconciler

// RouteReconcilerFactory binds route reconciliation to the shared handle.
type RouteReconcilerFactory func(
	NetlinkHandle,
	route.ReconcilerConfig,
) (RouteReconciler, error)

type options struct {
	NewNetlinkHandle    NetlinkHandleFactory
	DialGateway         GatewayDialer
	NewLinkReconciler   LinkReconcilerFactory
	NewRouteReconciler  RouteReconcilerFactory
	LoadNetplan         NetplanLoader
	DiscoverNeighbours  NeighbourDiscoverer
	PublishNeighbours   NeighbourPublisher
	SubscribeNeighbours NeighbourSubscriber
	Log                 *zap.Logger
}

func newOptions() *options {
	return &options{
		NewNetlinkHandle: func() (NetlinkHandle, error) {
			return netlink.NewHandle()
		},
		DialGateway: func(config commonoperator.GatewayConfig) (GatewayConnection, error) {
			return commonoperator.DialGateway(config)
		},
		NewLinkReconciler: func(handle NetlinkHandle) LinkReconciler {
			return netreconcile.NewReconciler(handle, netreconcile.NewProcSysctl())
		},
		NewRouteReconciler: func(
			handle NetlinkHandle,
			config route.ReconcilerConfig,
		) (RouteReconciler, error) {
			return route.NewReconciler(handle, config)
		},
		LoadNetplan:         netplanParseFile,
		DiscoverNeighbours:  neighbour.Discover,
		PublishNeighbours:   neighbour.Publish,
		SubscribeNeighbours: netlink.NeighSubscribeWithOptions,
		Log:                 zap.NewNop(),
	}
}

// Option configures NewOperator and its external dependencies.
type Option func(*options)

// WithLog sets the logger for the common operator framework.
func WithLog(log *zap.Logger) Option {
	return func(options *options) {
		options.Log = log
	}
}

// WithNetlinkHandleFactory replaces creation of the shared kernel handle.
func WithNetlinkHandleFactory(factory NetlinkHandleFactory) Option {
	return func(options *options) {
		options.NewNetlinkHandle = factory
	}
}

// WithGatewayDialer replaces gateway dialing.
func WithGatewayDialer(dialer GatewayDialer) Option {
	return func(options *options) {
		options.DialGateway = dialer
	}
}

// WithLinkReconcilerFactory replaces link reconciler construction.
func WithLinkReconcilerFactory(factory LinkReconcilerFactory) Option {
	return func(options *options) {
		options.NewLinkReconciler = factory
	}
}

// WithRouteReconcilerFactory replaces route reconciler construction.
func WithRouteReconcilerFactory(factory RouteReconcilerFactory) Option {
	return func(options *options) {
		options.NewRouteReconciler = factory
	}
}

// WithNetplanLoader replaces per-pass netplan file parsing.
func WithNetplanLoader(loader NetplanLoader) Option {
	return func(options *options) {
		options.LoadNetplan = loader
	}
}

// WithNeighbourDiscoverer replaces managed neighbour discovery.
func WithNeighbourDiscoverer(discoverer NeighbourDiscoverer) Option {
	return func(options *options) {
		options.DiscoverNeighbours = discoverer
	}
}

// WithNeighbourPublisher replaces gateway neighbour publication.
func WithNeighbourPublisher(publisher NeighbourPublisher) Option {
	return func(options *options) {
		options.PublishNeighbours = publisher
	}
}

// WithNeighbourSubscriber replaces netlink event subscription.
func WithNeighbourSubscriber(subscriber NeighbourSubscriber) Option {
	return func(options *options) {
		options.SubscribeNeighbours = subscriber
	}
}

var _ NetlinkHandle = (*netlink.Handle)(nil)
