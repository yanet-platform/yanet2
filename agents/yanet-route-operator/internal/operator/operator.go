package operator

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/cenkalti/backoff/v5"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/yanet-platform/yanet2/agents/yanet-route-operator/internal/discovery/neigh"
	"github.com/yanet-platform/yanet2/agents/yanet-route-operator/internal/rib"
	"github.com/yanet-platform/yanet2/agents/yanet-route-operator/operatorpb"
	"github.com/yanet-platform/yanet2/common/go/operator"
	"github.com/yanet-platform/yanet2/controlplane/gateway"
)

const (
	// staticTablePriority is the default priority assigned to entries in
	// the built-in "static" neighbour table.
	staticTablePriority = 10
)

var (
	serviceNames = []string{
		operatorpb.RouteService_ServiceDesc.ServiceName,
		operatorpb.NeighbourService_ServiceDesc.ServiceName,
		operatorpb.RouteOperatorService_ServiceDesc.ServiceName,
		operatorpb.MetricsService_ServiceDesc.ServiceName,
	}
)

// Operator wires together the gRPC server, gateway-registration loop
// and reconciler for the route operator.
type Operator struct {
	cfg          *Config
	server       *GRPCServer
	reconciler   *Reconciler
	actuator     Actuator
	routeSvc     *RouteService
	neighbourSvc *NeighbourService
	neighTable   *neigh.NeighTable
	neighMonitor *neigh.NeighMonitor
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

	// Reconciler needs services for snapshot + services need a wake
	// callback. Construct a placeholder closure first; once both sides
	// exist we resolve the closure to the real reconciler.
	var reconciler *Reconciler
	wake := func() {
		if reconciler != nil {
			reconciler.Wake()
		}
	}

	routeSvc := NewRouteService(
		neighTable,
		WithRouteServiceLog(log),
		WithRouteServiceRIBTTL(ribTTL(cfg)),
		WithRouteServiceOnChanged(wake),
	)
	neighbourSvc := NewNeighbourService(
		neighTable,
		WithNeighbourServiceOnChanged(wake),
	)
	metricsSvc := NewMetricsService(
		WithMetricsServiceCollector(metrics),
	)
	operatorSvc := NewRouteOperatorService()

	server := NewGRPCServer(
		cfg.Server,
		routeSvc,
		neighbourSvc,
		metricsSvc,
		operatorSvc,
		WithGRPCLog(log),
	)

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

	actuator := operator.NewFanOutActuator(
		actuators,
		operator.WithFanOutLog(log),
	)

	snapshot := SnapshotFunc(func() []FIB {
		ribs := routeSvc.Snapshot()
		view := neighTable.View()

		fibs := make([]FIB, 0, len(ribs))
		for name, ribRef := range ribs {
			fib, _ := BuildFIB(ribRef.DumpRoutes(), view)
			fib.Name = name
			fibs = append(fibs, fib)
		}
		return fibs
	})

	reconciler = NewReconciler(
		actuator,
		snapshot,
		WithReconcileInterval(cfg.Reconcile.Interval.Unwrap()),
		WithReconcileBackoff(
			cfg.Reconcile.InitialBackoff.Unwrap(),
			cfg.Reconcile.MaxBackoff.Unwrap(),
		),
		WithReconcilerMetrics(metrics),
		WithReconcilerLog(log),
	)

	return &Operator{
		cfg:          cfg,
		server:       server,
		reconciler:   reconciler,
		actuator:     actuator,
		routeSvc:     routeSvc,
		neighbourSvc: neighbourSvc,
		neighTable:   neighTable,
		neighMonitor: neighMonitor,
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
	return m.actuator.Close()
}

// Run starts the gRPC server, gateway-registration loops and reconcile
// loop. It blocks until the supplied context is cancelled or any
// goroutine returns an error.
func (m *Operator) Run(ctx context.Context) error {
	if err := m.applyStaticSeed(); err != nil {
		return fmt.Errorf("failed to apply static seed: %w", err)
	}

	wg, ctx := errgroup.WithContext(ctx)
	listener, err := net.Listen("tcp", m.cfg.Server.Endpoint.Unwrap())
	if err != nil {
		return fmt.Errorf("failed to listen gRPC operator endpoint %q: %w", m.cfg.Server.Endpoint.Unwrap(), err)
	}

	wg.Go(func() error {
		return m.server.Run(ctx, listener)
	})
	wg.Go(func() error {
		return m.runGatewayRegistration(ctx, listener.Addr())
	})
	wg.Go(func() error {
		return m.reconciler.Run(ctx)
	})
	wg.Go(func() error {
		return m.runNeighbourMonitor(ctx)
	})

	return wg.Wait()
}

// runNeighbourMonitor runs the netlink monitor when enabled.
func (m *Operator) runNeighbourMonitor(ctx context.Context) error {
	if m.neighMonitor == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	return m.neighMonitor.Run(ctx)
}

// applyStaticSeed seeds the operator state from the YAML static config.
func (m *Operator) applyStaticSeed() error {
	module := m.cfg.Function.Module.Unwrap()
	for _, route := range m.cfg.Static.Routes {
		prefix, err := netip.ParsePrefix(route.Prefix)
		if err != nil {
			return fmt.Errorf("failed to parse static route prefix %q: %w", route.Prefix, err)
		}
		nexthop, err := netip.ParseAddr(route.NexthopAddr)
		if err != nil {
			return fmt.Errorf("failed to parse static route nexthop %q: %w", route.NexthopAddr, err)
		}
		holder := m.routeSvc.getOrCreateRib(module)
		if err := holder.AddUnicastRoute(prefix, nexthop, rib.RouteSourceStatic); err != nil {
			return fmt.Errorf("failed to seed static route %s via %s: %w", prefix, nexthop, err)
		}
	}

	if len(m.cfg.Static.Neighbours) > 0 {
		grouped := map[string][]neigh.NeighbourEntry{}
		for _, n := range m.cfg.Static.Neighbours {
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
			if err := m.neighTable.Add(table, entries); err != nil {
				return fmt.Errorf("failed to seed neighbours into table %q: %w", table, err)
			}
		}
	}

	if len(m.cfg.Static.Routes) > 0 || len(m.cfg.Static.Neighbours) > 0 {
		m.reconciler.Wake()
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

// runGatewayRegistration heart-beats this operator's service set to
// every configured gateway.
func (m *Operator) runGatewayRegistration(
	ctx context.Context,
	endpoint net.Addr,
) error {
	if len(m.cfg.Gateways) == 0 {
		m.log.Warn("no gateways configured for operator registration",
			zap.Strings("services", serviceNames),
		)
		return nil
	}

	interval := m.cfg.Register.Interval.Unwrap()
	shortBackOff := func() backoff.BackOff {
		return backoff.NewExponentialBackOff()
	}

	wg, ctx := errgroup.WithContext(ctx)
	for _, cfg := range m.cfg.Gateways {
		log := m.log.With(
			zap.String("gateway", cfg.Name),
			zap.String("gateway_endpoint", cfg.Endpoint.Unwrap()),
		)
		registrar, err := gateway.NewGatewayRegistrar(
			cfg.Endpoint.Unwrap(),
			nil,
			gateway.WithLog(log),
			gateway.WithBackOff(shortBackOff),
			gateway.WithMaxElapsedTime(interval/2),
		)
		if err != nil {
			return fmt.Errorf("failed to create gateway registrar for %q: %w", cfg.Name, err)
		}

		wg.Go(func() error {
			defer func() {
				if err := registrar.Close(); err != nil {
					log.Warn("failed to close gateway registrar", zap.Error(err))
				}
			}()
			loop := gateway.NewRegistrationLoop(
				registrar,
				serviceNames,
				endpoint.String(),
				gateway.WithLoopInterval(interval),
				gateway.WithLoopLog(log),
			)
			return loop.Run(ctx)
		})
	}

	return wg.Wait()
}
