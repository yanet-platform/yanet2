package route

import (
	"context"
	"sort"
	"sync"
	"time"

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
}

func newRouteServiceOptions() *routeServiceOptions {
	return &routeServiceOptions{}
}

// WithMetrics sets the gRPC metrics factory.
//
// When unset, no metrics are collected.
func WithMetrics(factory grpcmetrics.Factory) RouteServiceOption {
	return func(o *routeServiceOptions) {
		o.Metrics = factory
	}
}

// configEntry is one route config applied to shared memory, together with
// the facts measured at the moment it was applied.
//
// A config's FIB is immutable once published: every update builds a fresh
// handle and retires the previous one. The sizes are therefore measured
// once here rather than walked out of the LPM keyspace on every scrape.
type configEntry struct {
	// Handle owns the published shared-memory config generation.
	Handle ModuleHandle
	// FIBRangeCountV4 is the number of IPv4 FIB ranges the config holds.
	FIBRangeCountV4 uint64
	// FIBRangeCountV6 is the number of IPv6 FIB ranges the config holds.
	FIBRangeCountV6 uint64
	// NexthopCount is the number of distinct hardware nexthops the config
	// resolves its prefixes to.
	NexthopCount uint64
	// UpdatedAt is when the FIB was applied to the dataplane, and backs
	// the staleness gauge.
	UpdatedAt time.Time
}

// Free releases the module handle held by the config.
//
// It is safe to call even when no handle is held.
func (m *configEntry) Free() {
	if m.Handle != nil {
		m.Handle.Free()
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
	configs map[string]configEntry

	metrics *grpcmetrics.ServerMetrics
}

// NewRouteService builds a RouteService bound to the supplied backend.
func NewRouteService(backend Backend, options ...RouteServiceOption) *RouteService {
	opts := newRouteServiceOptions()
	for _, o := range options {
		o(opts)
	}

	m := &RouteService{
		backend: backend,
		configs: map[string]configEntry{},
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

	entry, ok := m.configs[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	entries, err := entry.Handle.DumpFIB()
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

	entry, ok := m.configs[name]
	if !ok {
		return &routepb.DeleteConfigResponse{}, nil
	}

	if err := m.backend.DeleteModule(name); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete module config %q: %v", name, err)
	}
	entry.Free()
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

	// The counts are read once, here, off the handle just published: the
	// FIB never changes again for this generation, and each read walks the
	// LPM keyspace.
	m.configs[name] = configEntry{
		Handle:          module,
		FIBRangeCountV4: module.FIBRangeCountV4(),
		FIBRangeCountV6: module.FIBRangeCountV6(),
		NexthopCount:    module.RouteCount(),
		UpdatedAt:       time.Now(),
	}

	return &routepb.UpdateFIBResponse{}, nil
}

// Metrics returns route module metrics matching tags: per-config FIB
// gauges, the module-level dataplane counters, plus gRPC call metrics.
//
// A "counter" tag is pushed down into the dataplane counter read, so
// counters excluded by tags are never read from shared memory.
//
// Labels:
//   - config:       route config name (all gauge and counter metrics)
//   - family:       address family, "v4", "v6", or "unknown" when the
//     ethertype was not IP (route_fib_entries, route_forwarded_*,
//     route_drop_*)
//   - device:       dataplane device name (all dataplane counter metrics)
//   - pipeline:     pipeline name (all dataplane counter metrics)
//   - function:     pipeline function name (all dataplane counter metrics)
//   - chain:        pipeline chain name (all dataplane counter metrics)
//   - reason:       drop cause (route_drop_packets / route_drop_bytes only)
//   - grpc_type:    always "unary" (gRPC metrics)
//   - grpc_service: fully-qualified gRPC service name (gRPC metrics)
//   - grpc_method:  RPC name (gRPC metrics)
//   - grpc_code:    gRPC status code string (grpc_server_handled_total only)
func (m *RouteService) Metrics(tags ...*commonpb.MetricTag) ([]*commonpb.Metric, error) {
	result := m.collectConfigMetrics()
	result = append(result, m.collectDataplaneMetrics(tags)...)
	if m.metrics != nil {
		result = append(result, m.metrics.Collect()...)
	}
	return metrics.Filter(result, tags), nil
}

// collectConfigMetrics gathers the gauges describing what each config
// currently holds in shared memory, and when it was last applied.
//
// The FIB size is reported per address family, so the combined total stays
// derivable as a sum over the family label. Every value was measured when
// the config was applied, so a scrape costs no shared-memory traversal.
func (m *RouteService) collectConfigMetrics() []*commonpb.Metric {
	m.shmLock.RLock()
	defer m.shmLock.RUnlock()

	result := make([]*commonpb.Metric, 0, 4*len(m.configs))
	for name, entry := range m.configs {
		configLabels := []*commonpb.Label{
			{Name: "config", Value: name},
		}
		v4Labels := []*commonpb.Label{
			{Name: "config", Value: name},
			{Name: "family", Value: "v4"},
		}
		v6Labels := []*commonpb.Label{
			{Name: "config", Value: name},
			{Name: "family", Value: "v6"},
		}

		result = append(result,
			commonpb.NewMetricGauge("route_fib_entries", float64(entry.FIBRangeCountV4), v4Labels...),
			commonpb.NewMetricGauge("route_fib_entries", float64(entry.FIBRangeCountV6), v6Labels...),
			commonpb.NewMetricGauge("route_nexthops", float64(entry.NexthopCount), configLabels...),
			commonpb.NewMetricGauge(
				"route_config_updated_timestamp_seconds",
				float64(entry.UpdatedAt.Unix()),
				configLabels...,
			),
		)
	}

	return result
}
