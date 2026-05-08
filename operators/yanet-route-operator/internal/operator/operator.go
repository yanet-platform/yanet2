package operator

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/yanet2/operators/yanet-route-operator/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/operators/yanet-route-operator/internal/rib"
	"github.com/yanet-platform/yanet2/operators/yanet-route-operator/operatorpb"
	"github.com/yanet-platform/yanet2/common/go/operator"
)

const (
	// staticTablePriority is the default priority assigned to entries in
	// the built-in "static" neighbour table.
	staticTablePriority = 10
)

// Actuator applies a desired set of FIBs to one or more downstream
// targets (gateways).
type Actuator = operator.Actuator[[]FIB]

// Operator is the route operator's thin wrapper around the generic
// operator framework.
type Operator struct {
	cfg          *Config
	app          *operator.Operator[[]FIB]
	routeSvc     *RouteService
	neighTable   *neigh.NeighTable
	neighMonitor *neigh.NeighMonitor
	source       *RouteSource
	log          *zap.Logger
}

// NewOperator constructs an Operator from the supplied configuration.
func NewOperator(cfg *Config, options ...Option) (*Operator, error) {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	log := opts.Log
	metrics := NewMetrics()

	neighTable := neigh.NewNeighTable()
	if _, err := neighTable.CreateSource("static", staticTablePriority, true); err != nil {
		return nil, fmt.Errorf("failed to create static neighbour source: %w", err)
	}

	neighMonitor, err := newNeighbourMonitor(cfg, neighTable, log)
	if err != nil {
		return nil, fmt.Errorf("failed to create neighbour monitor: %w", err)
	}

	routeRIBStore := newRIBStore(log)
	source := NewRouteSource(neighTable, routeRIBStore)
	wake := source.WakeFunc()

	routeSvc := NewRouteService(
		neighTable,
		WithRouteServiceRIBStore(routeRIBStore),
		WithRouteServiceRIBTTL(ribTTL(cfg)),
		WithRouteServiceOnChanged(wake),
		WithRouteServiceLog(log),
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
		actuator, err := NewGatewayActuator(
			gw,
			WithGatewayActuatorLog(log),
			WithGatewayActuatorFunction(cfg.Function),
		)
		if err != nil {
			for _, a := range actuators {
				_ = a.Close()
			}
			return nil, fmt.Errorf("failed to construct gateway actuator %q: %w", gw.Name, err)
		}

		actuators = append(actuators, actuator)
	}

	fanOut := operator.NewFanOutActuator(
		actuators,
		operator.WithFanOutLog(log),
	)

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
	}

	app := operator.NewOperator(
		cfg.Server,
		fanOut,
		source,
		services,
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
		operator.WithWorkers(neighbourMonitorRunner(neighMonitor)),
	)

	return &Operator{
		cfg:          cfg,
		app:          app,
		routeSvc:     routeSvc,
		neighTable:   neighTable,
		neighMonitor: neighMonitor,
		source:       source,
		log:          log,
	}, nil
}

// ribTTL returns the configured RIB TTL or the default if unset.
func ribTTL(cfg *Config) time.Duration {
	if cfg.RIBTTL > 0 {
		return cfg.RIBTTL
	}

	return DefaultRIBTTL
}

// newNeighbourMonitor constructs the netlink-backed neighbour monitor
// when enabled in the config; otherwise returns nil.
func newNeighbourMonitor(
	cfg *Config,
	neighTable *neigh.NeighTable,
	log *zap.Logger,
) (*neigh.NeighMonitor, error) {
	if cfg.NetlinkMonitor.Disabled {
		return nil, nil
	}

	source, err := neighTable.CreateSource(
		cfg.NetlinkMonitor.TableName,
		cfg.NetlinkMonitor.DefaultPriority,
		true,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create kernel neighbour source: %w", err)
	}

	monitor := neigh.NewNeighMonitor(
		neighTable,
		source,
		neigh.WithLog(log),
		neigh.WithLinkMap(cfg.LinkMap),
	)

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

// neighbourMonitorRunner adapts a NeighMonitor (or its absence) to the
// framework's Runner type.
func neighbourMonitorRunner(monitor *neigh.NeighMonitor) operator.Runner {
	return func(ctx context.Context) error {
		if monitor == nil {
			return nil
		}

		return monitor.Run(ctx)
	}
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
