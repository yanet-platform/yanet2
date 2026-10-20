package operator

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/xnetip"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/operator"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
	"github.com/yanet-platform/yanet2/operators/route/internal/rib"
	"github.com/yanet-platform/yanet2/operators/route/neigh"
)

// GatewayActuator applies route-operator state to a single Gateway via
// the route module's UpdateFIB unary RPC.
type GatewayActuator struct {
	name             string
	conn             *grpc.ClientConn
	routes           routepb.RouteServiceClient
	function         *ynpb.Function
	functionActuator *operator.FunctionActuator
	devices          []string
	module           string
	staticRoutes     map[netip.Prefix]map[netip.Addr]struct{}
	onFIBBuilt       func(module string, stats FIBBuildStats)
	log              *zap.Logger
}

// NewGatewayActuator dials the Gateway endpoint and returns a
// ready-to-use actuator.
func NewGatewayActuator(
	cfg operator.GatewayConfig,
	options ...GatewayActuatorOption,
) (*GatewayActuator, error) {
	opts := newGatewayActuatorOptions()
	for _, o := range options {
		o(opts)
	}

	if opts.Function.Name.Unwrap() == "" {
		return nil, fmt.Errorf("gateway actuator: function is required")
	}
	staticRoutes := map[netip.Prefix]map[netip.Addr]struct{}{}
	for _, route := range opts.StaticRoutes {
		prefix, err := netip.ParsePrefix(route.Prefix)
		if err != nil {
			return nil, fmt.Errorf("failed to parse static route prefix %q: %w", route.Prefix, err)
		}
		nexthop, err := netip.ParseAddr(route.NexthopAddr)
		if err != nil {
			return nil, fmt.Errorf("failed to parse static route nexthop %q: %w", route.NexthopAddr, err)
		}
		prefix = prefix.Masked()
		if staticRoutes[prefix] == nil {
			staticRoutes[prefix] = map[netip.Addr]struct{}{}
		}
		staticRoutes[prefix][nexthop.Unmap()] = struct{}{}
	}

	conn, err := operator.DialGateway(cfg)
	if err != nil {
		return nil, err
	}

	fn := opts.Function
	function := &ynpb.Function{
		Id: &commonpb.FunctionId{Name: fn.Name.Unwrap()},
		Chains: []*ynpb.FunctionChain{{
			Chain: &ynpb.Chain{
				Name: fn.Chain.Unwrap(),
				Modules: []*commonpb.ModuleId{{
					Type: "route",
					Name: fn.Module.Unwrap(),
				}},
			},
			Weight: fn.Weight,
		}},
	}

	log := opts.Log.With(zap.String("gateway", cfg.Name))
	actuatorOptions := []operator.FunctionActuatorOption{operator.WithFunctionLog(log)}
	if fn.IgnorePdump {
		actuatorOptions = append(actuatorOptions, operator.WithIgnorePDump())
	}

	return &GatewayActuator{
		name:             cfg.Name,
		conn:             conn,
		routes:           routepb.NewRouteServiceClient(conn),
		function:         function,
		functionActuator: operator.NewFunctionActuator(ynpb.NewFunctionServiceClient(conn), actuatorOptions...),
		devices:          opts.Devices,
		module:           fn.Module.Unwrap(),
		staticRoutes:     staticRoutes,
		onFIBBuilt:       opts.OnFIBBuilt,
		log:              log.With(zap.String("function", fn.Name.Unwrap())),
	}, nil
}

// Close releases the underlying gRPC connection.
func (m *GatewayActuator) Close() error {
	return m.conn.Close()
}

// Apply builds and pushes the FIB for each module config to the gateway,
// then republishes the operator's network function.
//
// Every FIB is attempted and the function is published even on a partial
// failure — the joined errors let the reconcile loop retry under backoff.
// Configured static-path resolution gates success but never publication.
func (m *GatewayActuator) Apply(ctx context.Context, snapshot RouteSnapshot) error {
	var err error
	for name, dump := range snapshot.RIBs {
		if name == "" {
			err = errors.Join(err, fmt.Errorf("FIB is missing module config name"))
			continue
		}

		fib, stats := BuildFIB(dump, snapshot.Neighbours, m.devices)
		fib.Name = name
		m.onFIBBuilt(name, stats)
		if e := m.pushFIB(ctx, fib); e != nil {
			err = errors.Join(err, fmt.Errorf("failed to push FIB to gateway %q: %w", m.name, e))
		}
	}

	return errors.Join(err, m.applyFunction(ctx), m.validateStaticRoutes(snapshot))
}

// validateStaticRoutes keeps configured requirements independent of runtime
// deletion and accepts only configured static paths in the gateway's local view.
func (m *GatewayActuator) validateStaticRoutes(snapshot RouteSnapshot) error {
	if len(m.staticRoutes) == 0 {
		return nil
	}

	neighbours := neigh.FilterByDevices(snapshot.Neighbours, m.devices)
	dump := snapshot.RIBs[m.module]
	var missing []string
	for prefix, nexthops := range m.staticRoutes {
		resolved := false
		for _, route := range dump[prefix.Bits()][prefix].Routes {
			if route.SourceID != rib.RouteSourceStatic {
				continue
			}
			nexthop := route.NextHop.Unmap()
			if _, ok := nexthops[nexthop]; !ok {
				continue
			}
			if _, ok := neighbours.Lookup(nexthop); ok {
				resolved = true
				break
			}
		}
		if !resolved {
			missing = append(missing, prefix.String())
		}
	}
	if len(missing) == 0 {
		return nil
	}
	slices.Sort(missing)
	return fmt.Errorf("gateway %q module %q has unresolved configured static prefixes: %s",
		m.name, m.module, strings.Join(missing, ", "),
	)
}

// applyFunction publishes the operator's single network-function definition to
// the gateway.
func (m *GatewayActuator) applyFunction(ctx context.Context) error {
	if err := m.functionActuator.Apply(ctx, m.function); err != nil {
		return fmt.Errorf("failed to update function on gateway %q: %w", m.name, err)
	}

	return nil
}

// pushFIB applies fib to the gateway via the UpdateFIB unary RPC.
func (m *GatewayActuator) pushFIB(ctx context.Context, fib FIB) error {
	entries := make([]*routepb.FIBEntry, len(fib.Entries))
	for idx, entry := range fib.Entries {
		e, err := fibEntryToProto(entry)
		if err != nil {
			return fmt.Errorf("failed to convert FIB entry for prefix %q: %w", entry.Prefix, err)
		}
		entries[idx] = e
	}

	req := &routepb.UpdateFIBRequest{
		ModuleName: fib.Name,
		Entries:    entries,
	}

	if _, err := m.routes.UpdateFIB(ctx, req); err != nil {
		return fmt.Errorf("failed to call UpdateFIB: %w", err)
	}

	m.log.Debug("pushed FIB to gateway", zap.String("name", fib.Name))
	return nil
}

// fibEntryToProto converts an internal FIBEntry to the wire range format.
func fibEntryToProto(entry FIBEntry) (*routepb.FIBEntry, error) {
	network, ok := xnetip.NetworkFromPrefix(entry.Prefix)
	if !ok {
		return nil, fmt.Errorf("invalid prefix %q", entry.Prefix)
	}

	nexthops := make([]*routepb.FIBNexthop, len(entry.Nexthops))
	for idx, nh := range entry.Nexthops {
		nexthops[idx] = &routepb.FIBNexthop{
			SrcMac: commonpb.NewMACAddressEUI48(nh.SourceMAC),
			DstMac: commonpb.NewMACAddressEUI48(nh.DestinationMAC),
			Device: nh.Device,
		}
	}

	ipRange, err := commonpb.NewIPRange(entry.Prefix.Addr(), network.LastAddr())
	if err != nil {
		return nil, err
	}

	return &routepb.FIBEntry{
		Range:    ipRange,
		Nexthops: nexthops,
	}, nil
}
