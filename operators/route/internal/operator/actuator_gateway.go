package operator

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/operator"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
)

// GatewayActuator applies route-operator state to a single Gateway via
// the route module's UpdateFIB unary RPC.
type GatewayActuator struct {
	name        string
	conn        *grpc.ClientConn
	routes      routepb.RouteServiceClient
	funcApplier *operator.FunctionApplier
	devices     []string
	onFIBBuilt  func(module string, stats FIBBuildStats)
	log         *zap.Logger
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

	endpoint := cfg.Endpoint.Unwrap()
	conn, err := grpc.NewClient(
		endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to dial gateway %q at %q: %w", cfg.Name, endpoint, err)
	}

	fn := opts.Function
	spec := operator.FunctionChainSpec{
		Name:   fn.Name.Unwrap(),
		Chain:  fn.Chain.Unwrap(),
		Weight: fn.Weight,
		Modules: []*commonpb.ModuleId{{
			Type: "route",
			Name: fn.Module.Unwrap(),
		}},
	}

	return &GatewayActuator{
		name:   cfg.Name,
		conn:   conn,
		routes: routepb.NewRouteServiceClient(conn),
		funcApplier: operator.NewFunctionApplier(
			ynpb.NewFunctionServiceClient(conn),
			spec,
			operator.WithIgnorePdump(fn.IgnorePdump),
		),
		devices:    opts.Devices,
		onFIBBuilt: opts.OnFIBBuilt,
		log: opts.Log.With(
			zap.String("gateway", cfg.Name),
			zap.String("function", fn.Name.Unwrap()),
		),
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
// failure; the joined errors let the reconcile loop retry under backoff.
func (m *GatewayActuator) Apply(ctx context.Context, snapshot RouteSnapshot) error {
	neighbours := neigh.FilterByDevices(snapshot.Neighbours, m.devices)

	var err error
	for name, dump := range snapshot.RIBs {
		if name == "" {
			err = errors.Join(err, fmt.Errorf("FIB is missing module config name"))
			continue
		}

		fib, stats := BuildFIB(dump, neighbours)
		fib.Name = name
		m.onFIBBuilt(name, stats)
		if e := m.pushFIB(ctx, fib); e != nil {
			err = errors.Join(err, fmt.Errorf("failed to push FIB to gateway %q: %w", m.name, e))
		}
	}

	return errors.Join(err, m.applyFunction(ctx))
}

// applyFunction publishes the operator's single network-function definition to
// the gateway.
func (m *GatewayActuator) applyFunction(ctx context.Context) error {
	skipped, err := m.funcApplier.Apply(ctx)
	if err != nil {
		return fmt.Errorf("failed to update function on gateway %q: %w", m.name, err)
	}

	if skipped {
		m.log.Debug("function already correct, skipped")
	} else {
		m.log.Info("updated function")
	}

	return nil
}

// pushFIB applies fib to the gateway via the UpdateFIB unary RPC.
func (m *GatewayActuator) pushFIB(ctx context.Context, fib FIB) error {
	entries := make([]*routepb.FIBEntry, len(fib.Entries))
	for idx, entry := range fib.Entries {
		entries[idx] = fibEntryToProto(entry)
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

// fibEntryToProto converts an internal FIBEntry to the wire format.
func fibEntryToProto(entry FIBEntry) *routepb.FIBEntry {
	nexthops := make([]*routepb.FIBNexthop, len(entry.Nexthops))
	for idx, nh := range entry.Nexthops {
		nexthops[idx] = &routepb.FIBNexthop{
			SrcMac: commonpb.NewMACAddressEUI48(nh.SourceMAC),
			DstMac: commonpb.NewMACAddressEUI48(nh.DestinationMAC),
			Device: nh.Device,
		}
	}
	return &routepb.FIBEntry{
		Prefix:   entry.Prefix.String(),
		Nexthops: nexthops,
	}
}
