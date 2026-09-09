package operator

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/yanet-platform/xnetip"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/operator"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
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
	for _, option := range options {
		option(opts)
	}

	if opts.Function.Name.Unwrap() == "" {
		return nil, fmt.Errorf("gateway actuator: function is required")
	}
	connection, err := operator.DialGateway(cfg)
	if err != nil {
		return nil, err
	}

	config := opts.Function
	function := &ynpb.Function{
		Id: &commonpb.FunctionId{Name: config.Name.Unwrap()},
		Chains: []*ynpb.FunctionChain{{
			Chain: &ynpb.Chain{
				Name: config.Chain.Unwrap(),
				Modules: []*commonpb.ModuleId{{
					Type: "route",
					Name: config.Module.Unwrap(),
				}},
			},
			Weight: config.Weight,
		}},
	}
	log := opts.Log.With(zap.String("gateway", cfg.Name))
	actuatorOptions := []operator.FunctionActuatorOption{operator.WithFunctionLog(log)}
	if config.IgnorePdump {
		actuatorOptions = append(actuatorOptions, operator.WithIgnorePDump())
	}
	return &GatewayActuator{
		name:             cfg.Name,
		conn:             connection,
		routes:           routepb.NewRouteServiceClient(connection),
		function:         function,
		functionActuator: operator.NewFunctionActuator(ynpb.NewFunctionServiceClient(connection), actuatorOptions...),
		devices:          opts.Devices,
		onFIBBuilt:       opts.OnFIBBuilt,
		log:              log.With(zap.String("function", config.Name.Unwrap())),
	}, nil
}

// Close releases the underlying gRPC connection.
func (m *GatewayActuator) Close() error {
	return m.conn.Close()
}

// Apply pushes each FIB and republishes the network function.
//
// Every gateway operation is attempted even after a partial failure.
func (m *GatewayActuator) Apply(ctx context.Context, snapshot RouteSnapshot) error {
	var applyErr error
	for name, dump := range snapshot.RIBs {
		if name == "" {
			m.log.Warn("skipping unnamed module config")
			continue
		}
		fib, stats := BuildFIB(dump, snapshot.Neighbours, m.devices, WithFIBScopeSource(snapshot.NeighbourScopeSource))
		if stats.AmbiguousNextHops != 0 {
			m.log.Warn("unscoped next hops have multiple devices", zap.String("module", name), zap.Int("ambiguous_next_hops", stats.AmbiguousNextHops))
		}
		fib.Name = name
		m.onFIBBuilt(name, stats)
		if err := m.pushFIB(ctx, fib); err != nil {
			applyErr = errors.Join(applyErr, fmt.Errorf("module %q: %w", name, err))
		}
	}
	return errors.Join(applyErr, m.applyFunction(ctx))
}

func (m *GatewayActuator) applyFunction(ctx context.Context) error {
	if err := m.functionActuator.Apply(ctx, m.function); err != nil {
		return fmt.Errorf("failed to update function on gateway %q: %w", m.name, err)
	}
	return nil
}

func (m *GatewayActuator) pushFIB(ctx context.Context, fib FIB) error {
	entries := make([]*routepb.FIBEntry, len(fib.Entries))
	for idx, entry := range fib.Entries {
		converted, err := fibEntryToProto(entry)
		if err != nil {
			return fmt.Errorf("failed to convert FIB entry for prefix %q: %w", entry.Prefix, err)
		}
		entries[idx] = converted
	}
	request := &routepb.UpdateFIBRequest{
		ModuleName: fib.Name,
		Entries:    entries,
	}
	if _, err := m.routes.UpdateFIB(ctx, request); err != nil {
		return fmt.Errorf("failed to call UpdateFIB: %w", err)
	}
	m.log.Debug("pushed FIB to gateway", zap.String("name", fib.Name))
	return nil
}

func fibEntryToProto(entry FIBEntry) (*routepb.FIBEntry, error) {
	network, ok := xnetip.NetworkFromPrefix(entry.Prefix)
	if !ok {
		return nil, fmt.Errorf("invalid prefix %q", entry.Prefix)
	}
	nexthops := make([]*routepb.FIBNexthop, len(entry.Nexthops))
	for idx, nexthop := range entry.Nexthops {
		nexthops[idx] = &routepb.FIBNexthop{
			SrcMac: commonpb.NewMACAddressEUI48(nexthop.SourceMAC),
			DstMac: commonpb.NewMACAddressEUI48(nexthop.DestinationMAC),
			Device: nexthop.Device,
		}
	}
	ipRange, err := commonpb.NewIPRange(entry.Prefix.Addr(), network.LastAddr())
	if err != nil {
		return nil, err
	}
	return &routepb.FIBEntry{Range: ipRange, Nexthops: nexthops}, nil
}
