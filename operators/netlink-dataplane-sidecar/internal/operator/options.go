package operator

import (
	"time"

	vnetlink "github.com/vishvananda/netlink"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/desired"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	netreconcile "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netlink"
)

// NetlinkHandle supports concurrent setup and observation in one namespace.
type NetlinkHandle interface {
	netreconcile.Backend
	neighbour.Backend
	SetSocketTimeout(time.Duration) error
	Close()
}

// NetlinkHandleFactory transfers one usable shared handle on success only.
type NetlinkHandleFactory func() (NetlinkHandle, error)

// GatewayConnection supports generated gRPC clients and explicit cleanup.
type GatewayConnection interface {
	grpc.ClientConnInterface
	Close() error
}

// GatewayDialer transfers one usable connection on success only.
type GatewayDialer func(commonoperator.GatewayConfig) (GatewayConnection, error)

// ConfigSourceFactory selects the startup adapter without loading it.
type ConfigSourceFactory func(*Config) (desired.Source, error)

type options struct {
	NewNetlinkHandle    NetlinkHandleFactory
	DialGateway         GatewayDialer
	NewConfigSource     ConfigSourceFactory
	SubscribeNeighbours NeighbourSubscriber
	Log                 *zap.Logger
}

func newOptions() *options {
	return &options{
		NewNetlinkHandle: func() (NetlinkHandle, error) {
			handle, err := netreconcile.NewHandle()
			if err != nil {
				return nil, err
			}
			return handle, nil
		},
		DialGateway: func(config commonoperator.GatewayConfig) (GatewayConnection, error) {
			connection, err := commonoperator.DialGateway(config)
			if err != nil {
				return nil, err
			}
			return connection, nil
		},
		NewConfigSource:     newConfigSource,
		SubscribeNeighbours: vnetlink.NeighSubscribeWithOptions,
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

var _ NetlinkHandle = (*netreconcile.Handle)(nil)

// WithConfigSourceFactory replaces startup configuration adapter selection.
func WithConfigSourceFactory(factory ConfigSourceFactory) Option {
	return func(options *options) {
		options.NewConfigSource = factory
	}
}

// WithNeighbourSubscriber replaces the kernel event subscription.
func WithNeighbourSubscriber(subscribe NeighbourSubscriber) Option {
	return func(options *options) {
		options.SubscribeNeighbours = subscribe
	}
}
