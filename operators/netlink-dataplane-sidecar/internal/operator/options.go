package operator

import (
	"time"

	"github.com/vishvananda/netlink"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	netreconcile "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netlink"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
)

// NetlinkHandle is the shared kernel handle surface used by all reconcilers.
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

// NetplanLoader reads and validates the desired configuration at startup.
type NetplanLoader func(string) (netplan.State, error)

type options struct {
	NewNetlinkHandle    NetlinkHandleFactory
	DialGateway         GatewayDialer
	LoadNetplan         NetplanLoader
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
		LoadNetplan:         netplan.ParseFile,
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

var _ NetlinkHandle = (*netreconcile.Handle)(nil)

// WithNetplanLoader replaces startup configuration loading.
func WithNetplanLoader(loader NetplanLoader) Option {
	return func(options *options) {
		options.LoadNetplan = loader
	}
}

// WithNeighbourSubscriber replaces the event socket subscription.
func WithNeighbourSubscriber(subscribe NeighbourSubscriber) Option {
	return func(options *options) {
		options.SubscribeNeighbours = subscribe
	}
}
