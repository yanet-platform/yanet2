package route

import (
	"context"
	"sort"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/modules/route/bindings/go/croute"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
)

// RouteServiceOption configures the RouteService constructor.
type RouteServiceOption func(*routeServiceOptions)

type routeServiceOptions struct {
	Metrics grpcmetrics.Factory
	Log     *zap.Logger
}

func newRouteServiceOptions() *routeServiceOptions {
	return &routeServiceOptions{
		Log: zap.NewNop(),
	}
}

// WithRouteServiceLog sets the logger for the RouteService.
func WithRouteServiceLog(log *zap.Logger) RouteServiceOption {
	return func(o *routeServiceOptions) {
		o.Log = log
	}
}

// WithMetrics sets the gRPC metrics factory.
//
// When unset, no metrics are collected.
func WithMetrics(factory grpcmetrics.Factory) RouteServiceOption {
	return func(o *routeServiceOptions) {
		o.Metrics = factory
	}
}

// RouteService is the gRPC service implementation backing the slim
// route-module shim.
type RouteService struct {
	routepb.UnimplementedRouteServiceServer

	backend Backend

	// shmLock serializes shared-memory mutations and protects the
	// configs map.
	shmLock sync.RWMutex
	configs map[string]ModuleHandle

	metrics *grpcmetrics.ServerMetrics

	log *zap.Logger
}

// NewRouteService builds a RouteService bound to the supplied backend.
func NewRouteService(backend Backend, options ...RouteServiceOption) *RouteService {
	opts := newRouteServiceOptions()
	for _, o := range options {
		o(opts)
	}

	m := &RouteService{
		backend: backend,
		configs: map[string]ModuleHandle{},
		log:     opts.Log,
	}
	if opts.Metrics != nil {
		m.metrics = opts.Metrics(m.retention)
	}

	return m
}

// UnaryServerInterceptor returns the service's gRPC metrics interceptor, or
// nil when metrics are not configured.
func (m *RouteService) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	if m.metrics == nil {
		return nil
	}

	return m.metrics.UnaryServerInterceptor()
}

// retention snapshots the live route config names and returns a predicate
// that keeps series whose "config" label is still live (or absent).
func (m *RouteService) retention() func(metrics.MetricID) bool {
	m.shmLock.RLock()
	configNames := make(map[string]struct{}, len(m.configs))
	for name := range m.configs {
		configNames[name] = struct{}{}
	}
	m.shmLock.RUnlock()

	return func(id metrics.MetricID) bool {
		config := id.Labels["config"]
		if config == "" {
			return true
		}

		_, ok := configNames[config]
		return ok
	}
}

func labeler(fullMethod string, req any) metrics.Labels {
	switch r := req.(type) {
	case *routepb.DeleteConfigRequest:
		return metrics.Labels{"config": r.GetName()}
	case *routepb.ShowFIBRequest:
		return metrics.Labels{"config": r.GetName()}
	case *routepb.UpdateFIBRequest:
		return metrics.Labels{"config": r.GetModuleName()}
	default:
		return nil
	}
}

// ListConfigs returns the names of all route module configurations
// currently known to the service.
func (m *RouteService) ListConfigs(
	ctx context.Context,
	req *routepb.ListConfigsRequest,
) (*routepb.ListConfigsResponse, error) {
	m.shmLock.RLock()
	defer m.shmLock.RUnlock()

	response := &routepb.ListConfigsResponse{
		Configs: make([]string, 0, len(m.configs)),
	}
	for name := range m.configs {
		response.Configs = append(response.Configs, name)
	}
	sort.Strings(response.Configs)
	return response, nil
}

// ShowFIB returns the FIB entries currently applied in shared memory
// for the requested configuration.
func (m *RouteService) ShowFIB(
	ctx context.Context,
	req *routepb.ShowFIBRequest,
) (*routepb.ShowFIBResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	// Hold RLock for the entire DumpFIB call so a concurrent Free under
	// shmLock.Lock cannot release the underlying shared memory.
	m.shmLock.RLock()
	defer m.shmLock.RUnlock()

	module, ok := m.configs[name]
	if !ok {
		return &routepb.ShowFIBResponse{}, nil
	}

	entries, err := module.DumpFIB()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to dump FIB: %v", err)
	}

	response := &routepb.ShowFIBResponse{
		Entries: make([]*routepb.FIBRangeEntry, 0, len(entries)),
	}
	for _, e := range entries {
		if req.GetIpv4Only() && e.AddressFamily != croute.AddressFamilyIPv4 {
			continue
		}
		if req.GetIpv6Only() && e.AddressFamily != croute.AddressFamilyIPv6 {
			continue
		}

		nexthops := make([]*routepb.FIBNexthop, len(e.Nexthops))
		for idx, nh := range e.Nexthops {
			nexthops[idx] = &routepb.FIBNexthop{
				DstMac: commonpb.NewMACAddressEUI48([6]byte(nh.DstMAC)),
				SrcMac: commonpb.NewMACAddressEUI48([6]byte(nh.SrcMAC)),
				Device: nh.Device,
			}
		}

		ipRange, err := commonpb.NewIPRange(e.PrefixFrom, e.PrefixTo)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to build IP range from FIB entry: %v", err)
		}

		response.Entries = append(response.Entries, &routepb.FIBRangeEntry{
			Range:    ipRange,
			Nexthops: nexthops,
		})
	}
	return response, nil
}

// DeleteConfig deletes a route module configuration.
func (m *RouteService) DeleteConfig(
	ctx context.Context,
	req *routepb.DeleteConfigRequest,
) (*routepb.DeleteConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	m.shmLock.Lock()
	defer m.shmLock.Unlock()

	module, ok := m.configs[name]
	if !ok {
		return &routepb.DeleteConfigResponse{}, nil
	}

	if err := m.backend.DeleteModule(name); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete module config %q: %v", name, err)
	}
	module.Free()
	delete(m.configs, name)

	return &routepb.DeleteConfigResponse{}, nil
}

// UpdateFIB applies a freshly-built FIB to the dataplane atomically.
func (m *RouteService) UpdateFIB(
	ctx context.Context,
	req *routepb.UpdateFIBRequest,
) (*routepb.UpdateFIBResponse, error) {
	name := req.GetModuleName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module_name is required")
	}

	m.shmLock.Lock()
	defer m.shmLock.Unlock()

	module, err := m.backend.UpdateModule(name, req.GetEntries())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to apply FIB for %q: %v", name, err)
	}

	if old, ok := m.configs[name]; ok {
		old.Free()
	}
	m.configs[name] = module

	return &routepb.UpdateFIBResponse{}, nil
}

// Metrics returns all route module metrics: per-config FIB size gauges, plus
// gRPC call metrics.
//
// Labels:
//   - config:       route config name (all gauge metrics)
//   - grpc_type:    always "unary" (gRPC metrics)
//   - grpc_service: fully-qualified gRPC service name (gRPC metrics)
//   - grpc_method:  RPC name (gRPC metrics)
//   - grpc_code:    gRPC status code string (grpc_server_handled_total only)
func (m *RouteService) Metrics() ([]*commonpb.Metric, error) {
	result := m.collectConfigMetrics()
	if m.metrics != nil {
		result = append(result, m.metrics.Collect()...)
	}
	return result, nil
}

// collectConfigMetrics gathers per-config gauges on a best-effort basis: a
// dump failure for one config is logged and skipped so the remaining configs
// still contribute their series.
func (m *RouteService) collectConfigMetrics() []*commonpb.Metric {
	m.shmLock.RLock()
	defer m.shmLock.RUnlock()

	result := make([]*commonpb.Metric, 0, len(m.configs))
	for name, module := range m.configs {
		labels := []*commonpb.Label{{Name: "config", Value: name}}

		count, err := module.FIBRangeCount()
		if err != nil {
			m.log.Warn("failed to collect fib metrics for config",
				zap.String("config", name),
				zap.Error(err),
			)
			continue
		}

		result = append(result, makeGauge("route_fib_entries", float64(count), labels...))
	}

	return result
}
