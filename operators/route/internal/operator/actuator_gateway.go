package operator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"

	"github.com/yanet-platform/xnetip"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/operator"
	ynpb "github.com/yanet-platform/yanet2/controlplane/ynpb/v1"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
	sidecarpb "github.com/yanet-platform/yanet2/operators/netlink-dataplane-sidecar/sidecarpb/v1"
	"github.com/yanet-platform/yanet2/operators/route/internal/discovery/neigh"
)

const staticRouteChunkSize = 1000

// GatewayActuator applies route-operator state to a single Gateway via
// the route module's UpdateFIB unary RPC.
type GatewayActuator struct {
	name             string
	conn             *grpc.ClientConn
	routes           routepb.RouteServiceClient
	netlinkSidecar   sidecarpb.NetlinkDataplaneServiceClient
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
	for _, o := range options {
		o(opts)
	}

	if opts.Function.Name.Unwrap() == "" {
		return nil, fmt.Errorf("gateway actuator: function is required")
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

	var netlinkSidecar sidecarpb.NetlinkDataplaneServiceClient
	if opts.NetlinkSidecarEnabled {
		netlinkSidecar = sidecarpb.NewNetlinkDataplaneServiceClient(conn)
	}
	actuator := &GatewayActuator{
		name:             cfg.Name,
		conn:             conn,
		routes:           routepb.NewRouteServiceClient(conn),
		netlinkSidecar:   netlinkSidecar,
		function:         function,
		functionActuator: operator.NewFunctionActuator(ynpb.NewFunctionServiceClient(conn), actuatorOptions...),
		devices:          opts.Devices,
		onFIBBuilt:       opts.OnFIBBuilt,
		log:              log.With(zap.String("function", fn.Name.Unwrap())),
	}
	return actuator, nil
}

// Close releases the underlying gRPC connection.
func (m *GatewayActuator) Close() error {
	return m.conn.Close()
}

// Name returns the configured gateway name.
func (m *GatewayActuator) Name() string {
	return m.name
}

// NetlinkSidecar returns the optional sidecar client for this gateway.
func (m *GatewayActuator) NetlinkSidecar() sidecarpb.NetlinkDataplaneServiceClient {
	return m.netlinkSidecar
}

// Apply builds and pushes the FIB for each module config to the gateway, then
// republishes the operator's network function. Static routes are published by
// NetlinkSidecarActuator before gateway fan-out starts.
//
// Every FIB is attempted and the function is published even on a partial
// failure — the joined errors let the reconcile loop retry under backoff.
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

	return errors.Join(err, m.applyFunction(ctx))
}

// NetlinkSidecarActuator publishes one static-route snapshot before delegating
// to the normal concurrent gateway FIB fan-out.
type NetlinkSidecarActuator struct {
	inner              Actuator
	staticRoutes       []*sidecarpb.StaticRoute
	updateTimeout      time.Duration
	snapshotNeighbours func() neigh.TableSnapshot
	targets            []netlinkSidecarTarget
}

type netlinkSidecarTarget struct {
	Name   string
	Client sidecarpb.NetlinkDataplaneServiceClient
}

// NewNetlinkSidecarActuator compiles an immutable route snapshot and wraps the
// gateway fan-out with ordered publication through the shared sidecar.
func NewNetlinkSidecarActuator(
	inner Actuator,
	staticRoutes []StaticRouteConfig,
	updateTimeout time.Duration,
	snapshotNeighbours func() neigh.TableSnapshot,
	gateways ...*GatewayActuator,
) (*NetlinkSidecarActuator, error) {
	if inner == nil {
		return nil, errors.New("publish static-route snapshot: wrapped actuator is nil")
	}
	if updateTimeout <= 0 {
		return nil, fmt.Errorf(
			"publish static-route snapshot: update timeout must be positive, got %s",
			updateTimeout,
		)
	}
	if snapshotNeighbours == nil {
		return nil, errors.New("publish static-route snapshot: neighbour snapshotter is nil")
	}
	routes, err := staticRoutesToProto(staticRoutes)
	if err != nil {
		return nil, fmt.Errorf("failed to build static-route snapshot: %w", err)
	}

	targets := make([]netlinkSidecarTarget, 0, len(gateways))
	for idx, gateway := range gateways {
		if gateway == nil {
			targets = append(targets, netlinkSidecarTarget{Name: fmt.Sprintf("target[%d]", idx)})
			continue
		}
		targets = append(targets, netlinkSidecarTarget{
			Name:   gateway.Name(),
			Client: gateway.NetlinkSidecar(),
		})
	}
	return &NetlinkSidecarActuator{
		inner:              inner,
		staticRoutes:       routes,
		updateTimeout:      updateTimeout,
		snapshotNeighbours: snapshotNeighbours,
		targets:            targets,
	}, nil
}

// Apply commits the static-route snapshot through the first reachable gateway.
// Gateway FIBs are not changed unless the sidecar commit succeeds.
func (m *NetlinkSidecarActuator) Apply(ctx context.Context, snapshot RouteSnapshot) error {
	var publishErr error
	for _, target := range m.targets {
		if target.Client == nil {
			publishErr = errors.Join(
				publishErr,
				fmt.Errorf("gateway %q has no netlink sidecar client", target.Name),
			)
			continue
		}

		updateCtx, cancel := context.WithTimeout(ctx, m.updateTimeout)
		err := pushStaticRoutes(updateCtx, target.Client, m.staticRoutes)
		cancel()
		if err == nil {
			snapshot.Neighbours = m.snapshotNeighbours()
			return m.inner.Apply(ctx, snapshot)
		}
		publishErr = errors.Join(
			publishErr,
			fmt.Errorf("gateway %q: %w", target.Name, err),
		)
		if ctx.Err() != nil {
			break
		}
	}

	if publishErr == nil {
		publishErr = errors.New("no gateway targets are configured")
	}
	return fmt.Errorf("failed to publish static-route snapshot: %w", publishErr)
}

// Close delegates resource cleanup to the wrapped gateway actuator.
func (m *NetlinkSidecarActuator) Close() error {
	if m.inner == nil {
		return nil
	}
	return m.inner.Close()
}

func pushStaticRoutes(
	ctx context.Context,
	client sidecarpb.NetlinkDataplaneServiceClient,
	routes []*sidecarpb.StaticRoute,
) error {
	stream, err := client.UpdateStaticRoutes(ctx)
	if err != nil {
		return fmt.Errorf("failed to start static-route snapshot: %w", err)
	}

	if len(routes) == 0 {
		if err := stream.Send(&sidecarpb.UpdateStaticRoutesRequest{}); err != nil {
			return fmt.Errorf(
				"failed to send empty static-route snapshot: %w",
				finishFailedStream(stream, err),
			)
		}
	}
	for start := 0; start < len(routes); start += staticRouteChunkSize {
		end := min(start+staticRouteChunkSize, len(routes))
		if err := stream.Send(&sidecarpb.UpdateStaticRoutesRequest{Routes: routes[start:end]}); err != nil {
			return fmt.Errorf(
				"failed to send static-route snapshot: %w",
				finishFailedStream(stream, err),
			)
		}
	}
	if _, err := stream.CloseAndRecv(); err != nil {
		return fmt.Errorf("failed to commit static-route snapshot: %w", err)
	}
	return nil
}

func finishFailedStream(
	stream sidecarpb.NetlinkDataplaneService_UpdateStaticRoutesClient,
	sendErr error,
) error {
	_, terminalErr := stream.CloseAndRecv()
	if terminalErr == nil || errors.Is(terminalErr, io.EOF) {
		return sendErr
	}
	return errors.Join(sendErr, terminalErr)
}

type staticRouteKey struct {
	prefix  netip.Prefix
	nexthop netip.Addr
}

func staticRoutesToProto(routes []StaticRouteConfig) ([]*sidecarpb.StaticRoute, error) {
	converted := make([]*sidecarpb.StaticRoute, 0, len(routes))
	seen := map[staticRouteKey]int{}
	for idx, route := range routes {
		prefix, err := netip.ParsePrefix(route.Prefix)
		if err != nil {
			return nil, fmt.Errorf("static route %d: invalid prefix %q: %w", idx, route.Prefix, err)
		}
		if prefix.Addr().Zone() != "" {
			return nil, fmt.Errorf("static route %d: prefix %q has a zone", idx, route.Prefix)
		}
		if prefix.Addr().Is4In6() {
			return nil, fmt.Errorf(
				"static route %d: prefix %q is an IPv4-mapped IPv6 prefix",
				idx,
				route.Prefix,
			)
		}
		prefix = prefix.Masked()

		nexthop, err := netip.ParseAddr(route.NexthopAddr)
		if err != nil {
			return nil, fmt.Errorf("static route %d: invalid nexthop %q: %w", idx, route.NexthopAddr, err)
		}
		if nexthop.Zone() != "" {
			return nil, fmt.Errorf("static route %d: nexthop %q has a zone", idx, route.NexthopAddr)
		}
		if nexthop.Is4In6() {
			return nil, fmt.Errorf(
				"static route %d: nexthop %q is an IPv4-mapped IPv6 address",
				idx,
				route.NexthopAddr,
			)
		}
		if route.Interface == "" {
			return nil, fmt.Errorf("static route %d: interface is required", idx)
		}
		if strings.ContainsRune(route.Interface, '\x00') {
			return nil, fmt.Errorf("static route %d: interface contains a NUL byte", idx)
		}
		if len(route.Interface) >= unix.IFNAMSIZ {
			return nil, fmt.Errorf(
				"static route %d: interface %q exceeds Linux IFNAMSIZ",
				idx,
				route.Interface,
			)
		}
		if prefix.Addr().BitLen() != nexthop.BitLen() {
			return nil, fmt.Errorf(
				"static route %d: prefix %q and nexthop %q use different address families",
				idx,
				route.Prefix,
				route.NexthopAddr,
			)
		}

		key := staticRouteKey{
			prefix:  prefix,
			nexthop: nexthop,
		}
		if previous, duplicate := seen[key]; duplicate {
			if routes[previous].Interface != route.Interface {
				return nil, fmt.Errorf(
					"static routes %d and %d have the same prefix and nexthop (%s via %s) "+
						"but different interfaces %q and %q; the RIB cannot distinguish these paths",
					previous, idx, prefix, nexthop, routes[previous].Interface, route.Interface,
				)
			}
			return nil, fmt.Errorf("static route %d duplicates static route %d", idx, previous)
		}
		seen[key] = idx

		prefixProto, err := commonpb.NewIPPrefixFromPrefix(prefix)
		if err != nil {
			return nil, fmt.Errorf("static route %d: convert prefix: %w", idx, err)
		}
		converted = append(converted, &sidecarpb.StaticRoute{
			Prefix:    prefixProto,
			Nexthop:   commonpb.NewIPAddressFromAddr(nexthop),
			Interface: route.Interface,
		})
	}
	return converted, nil
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
