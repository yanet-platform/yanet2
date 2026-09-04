package operator

import (
	"context"
	"errors"
	"fmt"
	"slices"

	commonoperator "github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/neighbour"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/netplan"
	"github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/internal/route"
	operatorpb "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

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

	linkReconciler := opts.NewLinkReconciler(handle)
	if linkReconciler == nil {
		return nil, errors.New("create link reconciler: factory returned nil")
	}
	routeReconciler, err := opts.NewRouteReconciler(handle, route.ReconcilerConfig{
		Table:    cfg.Route.Table,
		Protocol: cfg.Route.Protocol,
		Priority: cfg.Route.Priority,
	})
	if err != nil {
		return nil, fmt.Errorf("create route reconciler: %w", err)
	}
	if routeReconciler == nil {
		return nil, errors.New("create route reconciler: factory returned nil")
	}

	store := route.NewStore()
	service, err := route.NewService(store, route.ServiceConfig{MaxRoutes: cfg.Route.MaxRoutes})
	if err != nil {
		return nil, fmt.Errorf("create route service: %w", err)
	}

	commonGateways := cfg.OperatorGateways()
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
			Name:            gateway.Name,
			TableName:       gateway.NeighbourTable,
			DefaultPriority: gateway.NeighbourPriority,
			Devices:         append([]string(nil), gateway.Devices...),
			Client:          operatorpb.NewNeighbourServiceClient(connection),
		})
	}

	actuator := NewActuator(
		cfg.NetplanPath.Unwrap(),
		linkReconciler,
		routeReconciler,
		handle,
		targets,
		cfg.LinkMap,
		WithActuatorNetplanLoader(opts.LoadNetplan),
		WithActuatorNeighbourDiscoverer(opts.DiscoverNeighbours),
		WithActuatorNeighbourPublisher(opts.PublishNeighbours),
	)
	actuator.SetRuntimeResources(connections, handle)

	source := NewSource(store)
	eventWorker := NewNeighbourEventWorker(
		store,
		opts.SubscribeNeighbours,
		cfg.Reconcile.MaxBackoff.Unwrap(),
	)
	app := commonoperator.NewOperator(
		actuator,
		source,
		commonoperator.WithGRPCServer(cfg.Server, service.Register),
		commonoperator.WithGateways(cfg.Register, commonGateways...),
		commonoperator.WithReconcile(cfg.Reconcile),
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
		{Name: "link reconciler factory", Missing: options.NewLinkReconciler == nil},
		{Name: "route reconciler factory", Missing: options.NewRouteReconciler == nil},
		{Name: "netplan loader", Missing: options.LoadNetplan == nil},
		{Name: "neighbour discoverer", Missing: options.DiscoverNeighbours == nil},
		{Name: "neighbour publisher", Missing: options.PublishNeighbours == nil},
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

func netplanParseFile(path string) (netplan.State, error) {
	return netplan.ParseFile(path)
}
