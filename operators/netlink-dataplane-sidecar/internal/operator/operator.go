package operator

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.uber.org/zap"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/xbackoff"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	netreconcile "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netlink"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

const netlinkSocketTimeout = 5 * time.Second

// NewOperator constructs the sidecar and transfers all opened resources to it.
func NewOperator(cfg *Config, options ...Option) (_ *commonoperator.Operator[State], resultErr error) {
	if cfg == nil {
		return nil, errors.New("construct netlink dataplane sidecar: config is nil")
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("construct netlink dataplane sidecar: invalid config: %w", err)
	}

	opts := newOptions()
	for _, option := range options {
		option(opts)
	}
	if err := validateDependencies(opts); err != nil {
		return nil, err
	}
	configSource, err := opts.NewConfigSource(cfg)
	if err != nil {
		return nil, fmt.Errorf("create startup config source: %w", err)
	}
	if configSource == nil {
		return nil, errors.New("create startup config source: factory returned nil")
	}
	state, err := configSource.Load()
	if err != nil {
		return nil, fmt.Errorf("load startup configuration: %w", err)
	}
	if err := state.Validate(); err != nil {
		return nil, fmt.Errorf("validate startup configuration: %w", err)
	}
	if err := neighbour.ValidateManagedDevices(state, cfg.LinkMap); err != nil {
		return nil, fmt.Errorf("validate startup logical devices: %w", err)
	}
	source := NewSource(state)
	state = state.Clone()

	handle, err := opts.NewNetlinkHandle()
	if err != nil {
		return nil, fmt.Errorf("create netlink handle: %w", err)
	}
	if handle == nil {
		return nil, errors.New("create netlink handle: factory returned nil")
	}

	connections := []GatewayConnection{}
	resourcesOwned := true
	defer func() {
		if resourcesOwned {
			resultErr = errors.Join(resultErr, closeRuntimeResources(connections, handle))
		}
	}()

	if err := handle.SetSocketTimeout(netlinkSocketTimeout); err != nil {
		return nil, fmt.Errorf("configure netlink socket timeout: %w", err)
	}
	linkReconciler := netreconcile.NewReconciler(handle, netreconcile.NewProcSysctl())

	commonGateways := cfg.Gateways
	targets := make([]neighbour.GatewayTarget, 0, len(cfg.Gateways))
	for idx, gateway := range cfg.Gateways {
		connection, dialErr := opts.DialGateway(commonGateways[idx])
		if dialErr != nil {
			return nil, fmt.Errorf("dial gateway %q: %w", gateway.Name, dialErr)
		}
		if connection == nil {
			return nil, fmt.Errorf("dial gateway %q: dialer returned nil", gateway.Name)
		}
		connections = append(connections, connection)

		targets = append(targets, neighbour.GatewayTarget{
			Name:   gateway.Name,
			Client: operatorpb.NewNeighbourServiceClient(connection),
		})
	}

	actuator := NewActuator(
		handle,
		targets,
		cfg.LinkMap,
		cfg.PublicationConfig(),
		connections,
	)

	backoff := xbackoff.New(cfg.Reconcile.InitialBackoff.Unwrap(),
		xbackoff.WithMax(cfg.Reconcile.MaxBackoff.Unwrap()),
		xbackoff.WithOnRetry(func(_ int, delay time.Duration, err error) {
			opts.Log.Warn("interface setup failed; retrying", zap.Duration("backoff", delay), zap.Error(err))
		}),
	)
	bootstrap := func(ctx context.Context) error {
		err := backoff.RunContext(ctx, func() error {
			// A missing parent must not prevent configuration of existing links.
			return errors.Join(linkReconciler.Create(ctx, state), linkReconciler.Configure(ctx, state))
		})
		if err == nil {
			opts.Log.Info("configured startup interfaces")
		}
		return nil
	}
	metrics := commonoperator.NewReconcilerMetrics(
		"generic_operator",
		commonpb.NewLabel("operator", "netlink-dataplane-sidecar"),
	)
	app := commonoperator.NewOperator(
		actuator,
		source,
		commonoperator.WithGRPCServer(cfg.Server,
			commonoperator.NewMetricsServiceRegistrar("netlink-dataplane-sidecar", metrics),
		),
		commonoperator.WithGateways(cfg.Register, commonGateways...),
		commonoperator.WithReconcile(cfg.Reconcile),
		commonoperator.WithMetrics(metrics),
		commonoperator.WithWorkers(bootstrap, func(ctx context.Context) error {
			return WatchNeighbours(ctx, source, opts.SubscribeNeighbours)
		}),
		commonoperator.WithLog(opts.Log),
	)

	resourcesOwned = false
	return app, nil
}

func validateDependencies(options *options) error {
	dependencies := []struct {
		Name    string
		Missing bool
	}{
		{Name: "logger", Missing: options.Log == nil},
		{Name: "netlink handle factory", Missing: options.NewNetlinkHandle == nil},
		{Name: "gateway dialer", Missing: options.DialGateway == nil},
		{Name: "config source factory", Missing: options.NewConfigSource == nil},
		{Name: "neighbour subscriber", Missing: options.SubscribeNeighbours == nil},
	}
	for _, dependency := range dependencies {
		if dependency.Missing {
			return fmt.Errorf("construct netlink dataplane sidecar: %s is nil", dependency.Name)
		}
	}
	return nil
}

func closeRuntimeResources(connections []GatewayConnection, handle NetlinkHandle) error {
	var closeErr error
	for idx, connection := range slices.Backward(connections) {
		if err := connection.Close(); err != nil {
			closeErr = errors.Join(
				closeErr,
				fmt.Errorf("close gateway connection %d: %w", idx, err),
			)
		}
	}
	if handle != nil {
		handle.Close()
	}
	return closeErr
}
