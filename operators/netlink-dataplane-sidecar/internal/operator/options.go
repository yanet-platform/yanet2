package operator

import (
	"time"

	"github.com/vishvananda/netlink"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"
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
	SetSocketTimeout(time.Duration) error
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

type options struct {
	NewNetlinkHandle NetlinkHandleFactory
	DialGateway      GatewayDialer
	Log              *zap.Logger
}

func newOptions() *options {
	return &options{
		NewNetlinkHandle: func() (NetlinkHandle, error) {
			return netlink.NewHandle(unix.NETLINK_ROUTE)
		},
		DialGateway: func(config commonoperator.GatewayConfig) (GatewayConnection, error) {
			return commonoperator.DialGateway(config)
		},
		Log: zap.NewNop(),
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

var _ NetlinkHandle = (*netlink.Handle)(nil)
