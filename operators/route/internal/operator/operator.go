package operator

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/common/go/readiness"
	readinesspb "github.com/yanet-platform/yanet2/common/readinesspb/v1"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
	"github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

// ribReadiness drives the rib readiness scope (and, when BIRD is expected,
// the bird-session scope) from FeedRIB session signals and a sampling loop.
type ribReadiness interface {
	OnSessionStart(name string, sessionID uint64)
	OnUpdate(n int)
	OnSessionEnd(name string, sessionID uint64)
	Run(ctx context.Context) error
}

const (
	// staticTablePriority is the default priority assigned to entries in
	// the built-in "static" neighbour table.
	staticTablePriority = 10
)

// Actuator applies a desired set of FIBs to one or more downstream
// targets (gateways).
type Actuator = operator.Actuator[RouteSnapshot]

// Operator is the route operator's thin wrapper around the generic
// operator framework.
type Operator struct {
	cfg        *Config
	app        *operator.Operator[RouteSnapshot]
	routeSvc   *RouteService
	neighTable *neigh.NeighTable
	source     *RouteSource
	log        *zap.Logger
}

// NewOperator constructs an Operator from the supplied configuration.
func NewOperator(cfg *Config, options ...Option) (*Operator, error) {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log

	moduleName := cfg.Function.Module.Unwrap()

	routeRIBStore := newRIBStore(log)
	metrics := opts.Metrics(routeRIBStore, !cfg.NetlinkMonitor.Disabled)

	// Build the readiness tracker scope list: one fib:<gateway>:<module> scope per
	// gateway, plus a neighbours scope, a rib scope, and optionally a bird-session scope.
	scopeNames := make([]string, 0, len(cfg.Gateways)+3)
	for _, gw := range cfg.Gateways {
		scopeNames = append(scopeNames, fmt.Sprintf("fib:%s:%s", gw.Name, moduleName))
	}
	scopeNames = append(scopeNames, "neighbours", "rib")
	if cfg.Readiness.ExpectBird {
		scopeNames = append(scopeNames, "bird-session")
	}
	tracker := readiness.NewTracker(scopeNames,
		readiness.WithLog(log.With(zap.String("operator", "route"))),
	)

	neighTable := neigh.NewNeighTable()
	if _, err := neighTable.CreateSource("static", staticTablePriority, true); err != nil {
		return nil, fmt.Errorf("failed to create static neighbour source: %w", err)
	}

	// Propagate the neighbours scope based on netlink monitor config, and
	// construct the monitor itself only when it is enabled.
	var neighMonitor *neigh.NeighMonitor
	if cfg.NetlinkMonitor.Disabled {
		tracker.Set("neighbours", readinesspb.State_STATE_READY)
	} else {
		// Seed the neighbours scope at NOT_READY(SYNCING) before the monitor starts.
		tracker.SetWithReason("neighbours",
			readinesspb.State_STATE_NOT_READY,
			&readinesspb.Reason{Code: "SYNCING"},
		)
		neighOpts := []neigh.Option{
			neigh.WithOnSynced(func() {
				tracker.Set("neighbours", readinesspb.State_STATE_READY)
				metrics.OnNeighbourSynced()
			}),
			neigh.WithOnError(func(err error) {
				tracker.SetWithReason("neighbours",
					readinesspb.State_STATE_DEGRADED,
					&readinesspb.Reason{Code: "RESYNC", Message: err.Error()},
				)
				metrics.OnNeighbourError(err)
			}),
			neigh.WithOnResynced(func() {
				tracker.Set("neighbours", readinesspb.State_STATE_READY)
				metrics.OnNeighbourResynced()
			}),
			neigh.WithOnHealthy(func() {
				tracker.Touch("neighbours")
				metrics.OnNeighbourHealthy()
			}),
		}

		monitor, err := newNeighbourMonitor(cfg, neighTable, log, neighOpts...)
		if err != nil {
			return nil, fmt.Errorf("failed to create neighbour monitor: %w", err)
		}
		neighMonitor = monitor
	}

	source := NewRouteSource(neighTable, routeRIBStore)
	wake := source.WakeFunc()
	ribHelper := newRIBReadiness(cfg.Readiness, routeRIBStore, moduleName, tracker, log)

	routeSvc := NewRouteService(
		neighTable,
		WithRouteServiceRIBStore(routeRIBStore),
		WithRouteServiceRIBTTL(ribTTL(cfg)),
		WithRouteServiceOnChanged(wake),
		WithRouteServiceLog(log),
		WithRouteServiceOnRIBSessionStart(func(name string, sessionID uint64) {
			ribHelper.OnSessionStart(name, sessionID)
			metrics.OnRIBSessionStart(name, sessionID)
		}),
		WithRouteServiceOnRIBUpdate(func(n int) {
			ribHelper.OnUpdate(n)
			metrics.OnRIBUpdate(n)
		}),
		WithRouteServiceOnRIBSessionEnd(func(name string, sessionID uint64) {
			ribHelper.OnSessionEnd(name, sessionID)
			metrics.OnRIBSessionEnd(name, sessionID)
		}),
	)

	neighbourSvc := NewNeighbourService(
		neighTable,
		WithNeighbourServiceOnChanged(wake),
	)
	metricsSvc := NewMetricsService(
		WithMetricsServiceCollector(metrics),
	)
	operatorSvc := NewRouteOperatorService()

	actuators := make([]Actuator, 0, len(cfg.Gateways))
	for _, gw := range cfg.Gateways {
		gatewayMetrics := metrics.Gateway(gw.Name)

		actuator, err := NewGatewayActuator(
			gw,
			WithGatewayActuatorLog(log),
			WithGatewayActuatorFunction(cfg.Function),
			WithGatewayActuatorDevices(cfg.GatewayDevices[gw.Name]),
			WithGatewayActuatorOnFIBBuilt(gatewayMetrics.OnFIBBuilt),
		)
		if err != nil {
			for _, a := range actuators {
				_ = a.Close()
			}
			return nil, fmt.Errorf("failed to construct gateway actuator %q: %w", gw.Name, err)
		}

		// Wrap each actuator so that apply outcomes drive the per-gateway apply
		// metrics, then so that they drive the per-gateway fib readiness scope.
		metered := newMeteredActuator(actuator, gatewayMetrics)
		observed := operator.NewObservedActuator(metered, fmt.Sprintf("fib:%s:%s", gw.Name, moduleName), tracker.Observe)
		actuators = append(actuators, observed)
	}

	fanOut := operator.NewFanOutActuator(
		actuators,
		operator.WithFanOutLog(log),
	)

	readinessSvc := NewReadinessService(tracker)

	services := []operator.ServiceRegistrar{
		func(s *grpc.Server) string {
			operatorpb.RegisterRouteServiceServer(s, routeSvc)
			return operatorpb.RouteService_ServiceDesc.ServiceName
		},
		func(s *grpc.Server) string {
			operatorpb.RegisterNeighbourServiceServer(s, neighbourSvc)
			return operatorpb.NeighbourService_ServiceDesc.ServiceName
		},
		func(s *grpc.Server) string {
			operatorpb.RegisterMetricsServiceServer(s, metricsSvc)
			return operatorpb.MetricsService_ServiceDesc.ServiceName
		},
		func(s *grpc.Server) string {
			operatorpb.RegisterRouteOperatorServiceServer(s, operatorSvc)
			return operatorpb.RouteOperatorService_ServiceDesc.ServiceName
		},
		func(s *grpc.Server) string {
			operatorpb.RegisterReadinessServiceServer(s, readinessSvc)
			return operatorpb.ReadinessService_ServiceDesc.ServiceName
		},
	}

	workers := []operator.Runner{
		func(ctx context.Context) error {
			<-ctx.Done()
			tracker.Drain()
			return nil
		},
		ribHelper.Run,
	}
	if !cfg.NetlinkMonitor.Disabled {
		workers = append(workers, neighMonitor.Run)
	}

	app := operator.NewOperator(
		fanOut,
		source,
		operator.WithGRPCServer(cfg.Server, services...),
		operator.WithLog(log),
		operator.WithReconcile(cfg.Reconcile),
		operator.WithGateways(cfg.Register, cfg.Gateways...),
		operator.WithMetrics(metrics),
		operator.WithPreRun(func(ctx context.Context) error {
			if err := applyStaticSeed(cfg, routeSvc, neighTable); err != nil {
				return err
			}

			// The route source is woken so the first reconcile pass observes
			// the seeded state.
			if len(cfg.Static.Routes) > 0 || len(cfg.Static.Neighbours) > 0 {
				source.WakeFunc()()
			}

			return nil
		}),
		operator.WithWorkers(workers...),
	)

	return &Operator{
		cfg:        cfg,
		app:        app,
		routeSvc:   routeSvc,
		neighTable: neighTable,
		source:     source,
		log:        log,
	}, nil
}

// ribTTL returns the configured RIB TTL or the default if unset.
func ribTTL(cfg *Config) time.Duration {
	if cfg.RIBTTL > 0 {
		return cfg.RIBTTL
	}

	return DefaultRIBTTL
}

// newNeighbourMonitor constructs the netlink-backed neighbour monitor.
//
// Callers must only invoke this when the netlink monitor is enabled in
// the config.
func newNeighbourMonitor(
	cfg *Config,
	neighTable *neigh.NeighTable,
	log *zap.Logger,
	extraOpts ...neigh.Option,
) (*neigh.NeighMonitor, error) {
	source, err := neighTable.CreateSource(
		cfg.NetlinkMonitor.TableName,
		cfg.NetlinkMonitor.DefaultPriority,
		true,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create kernel neighbour source: %w", err)
	}

	opts := append([]neigh.Option{
		neigh.WithLog(log),
		neigh.WithLinkMap(cfg.LinkMap),
	}, extraOpts...)

	monitor := neigh.NewNeighMonitor(neighTable, source, opts...)

	return monitor, nil
}

// Close releases resources owned by the operator.
func (m *Operator) Close() error {
	if err := m.routeSvc.Close(); err != nil {
		m.log.Warn("failed to close route service", zap.Error(err))
	}
	return m.app.Close()
}

// Run drives the operator until the supplied context is cancelled.
func (m *Operator) Run(ctx context.Context) error {
	return m.app.Run(ctx)
}

// applyStaticSeed seeds the operator state from the YAML static config.
func applyStaticSeed(
	cfg *Config,
	routeSvc *RouteService,
	neighTable *neigh.NeighTable,
) error {
	module := cfg.Function.Module.Unwrap()
	for _, route := range cfg.Static.Routes {
		prefix, err := netip.ParsePrefix(route.Prefix)
		if err != nil {
			return fmt.Errorf("failed to parse static route prefix %q: %w", route.Prefix, err)
		}

		nexthop, err := netip.ParseAddr(route.NexthopAddr)
		if err != nil {
			return fmt.Errorf("failed to parse static route nexthop %q: %w", route.NexthopAddr, err)
		}

		holder := routeSvc.getOrCreateRib(module)
		if err := holder.AddUnicastRoute(prefix, nexthop, rib.RouteSourceStatic); err != nil {
			return fmt.Errorf("failed to seed static route %s via %s: %w", prefix, nexthop, err)
		}
	}

	if len(cfg.Static.Neighbours) > 0 {
		grouped := map[string][]neigh.NeighbourEntry{}
		for _, n := range cfg.Static.Neighbours {
			table := n.Table
			if table == "" {
				table = defaultStaticTable
			}

			addr, err := netip.ParseAddr(n.NextHop)
			if err != nil {
				return fmt.Errorf("failed to parse static neighbour next_hop %q: %w", n.NextHop, err)
			}

			linkMAC, err := parseMAC(n.LinkAddr)
			if err != nil {
				return fmt.Errorf("failed to parse static neighbour link_addr %q: %w", n.LinkAddr, err)
			}

			hwMAC, err := parseMAC(n.HardwareAddr)
			if err != nil {
				return fmt.Errorf("failed to parse static neighbour hardware_addr %q: %w", n.HardwareAddr, err)
			}

			grouped[table] = append(grouped[table], neigh.NeighbourEntry{
				NextHop: addr,
				HardwareRoute: neigh.HardwareRoute{
					SourceMAC:      hwMAC,
					DestinationMAC: linkMAC,
					Device:         n.Device,
				},
				UpdatedAt: time.Now(),
				State:     neigh.NeighbourStatePermanent,
				Priority:  n.Priority,
			})
		}

		for table, entries := range grouped {
			if err := neighTable.Add(table, entries); err != nil {
				return fmt.Errorf("failed to seed neighbours into table %q: %w", table, err)
			}
		}
	}

	return nil
}

// parseMAC parses an EUI-48 MAC address into a 6-byte array.
func parseMAC(s string) ([6]byte, error) {
	hw, err := net.ParseMAC(s)
	if err != nil {
		return [6]byte{}, err
	}
	if len(hw) != 6 {
		return [6]byte{}, fmt.Errorf("expected 6-byte MAC, got %d bytes", len(hw))
	}
	return [6]byte(hw), nil
}
