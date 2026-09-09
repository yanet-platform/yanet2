package operator

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	netreconcile "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netlink"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

const netlinkSocketTimeout = 5 * time.Second

// Operator is the sidecar's lifecycle wrapper around the common framework.
type Operator struct {
	app *commonoperator.Operator[State]
}

// NewOperator constructs the sidecar and transfers all opened resources to it.
func NewOperator(cfg *Config, options ...Option) (_ *Operator, resultErr error) {
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
	state, err := opts.LoadNetplan(cfg.NetplanPath.Unwrap())
	if err != nil {
		return nil, fmt.Errorf("load startup netplan: %w", err)
	}
	if err := state.Validate(); err != nil {
		return nil, fmt.Errorf("validate startup netplan: %w", err)
	}
	if err := neighbour.ValidateManagedDevices(state, cfg.LinkMap); err != nil {
		return nil, fmt.Errorf("validate startup logical devices: %w", err)
	}
	source := NewSource(state)

	handle, err := opts.NewNetlinkHandle()
	if err != nil {
		if handle != nil {
			handle.Close()
		}
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
		if connection != nil {
			connections = append(connections, connection)
		}
		if dialErr != nil {
			return nil, fmt.Errorf("dial gateway %q: %w", gateway.Name, dialErr)
		}
		if connection == nil {
			return nil, fmt.Errorf("dial gateway %q: dialer returned nil", gateway.Name)
		}

		targets = append(targets, neighbour.GatewayTarget{
			Name:   gateway.Name,
			Client: operatorpb.NewNeighbourServiceClient(connection),
		})
	}

	actuator := NewActuator(
		linkReconciler,
		handle,
		targets,
		cfg.LinkMap,
		cfg.PublicationConfig(),
	)
	actuator.SetRuntimeResources(connections, handle)

	eventWorker := NewNeighbourEventWorker(
		source.Notify,
		opts.SubscribeNeighbours,
		cfg.Reconcile.MaxBackoff.Unwrap(),
		WithNeighbourEventWorkerLog(opts.Log),
	)
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
		commonoperator.WithWorkers(eventWorker.Run),
		commonoperator.WithLog(opts.Log),
	)

	resourcesOwned = false
	return &Operator{app: app}, nil
}

// Run drives the sidecar until cancellation or a worker failure.
func (m *Operator) Run(ctx context.Context) error {
	return m.app.Run(ctx)
}

// Close releases gateway connections and the shared netlink handle.
func (m *Operator) Close() error {
	return m.app.Close()
}

func validateDependencies(options *options) error {
	dependencies := []struct {
		Name    string
		Missing bool
	}{
		{Name: "logger", Missing: options.Log == nil},
		{Name: "netlink handle factory", Missing: options.NewNetlinkHandle == nil},
		{Name: "gateway dialer", Missing: options.DialGateway == nil},
		{Name: "netplan loader", Missing: options.LoadNetplan == nil},
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
