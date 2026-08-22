package route

import (
	"context"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/route/bindings/go/croute"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
)

// RouteServiceOption configures the RouteService constructor.
type RouteServiceOption func(*routeServiceOptions)

type routeServiceOptions struct {
	Metrics                grpcmetrics.Factory
	DisableNexthopCounters bool
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

// WithNexthopCountersDisabled turns off per-nexthop dataplane counters.
//
// UpdateFIB then rejects any nexthop that carries an explicit counter name,
// and never materializes one for a nexthop that left it empty.
func WithNexthopCountersDisabled() RouteServiceOption {
	return func(o *routeServiceOptions) {
		o.DisableNexthopCounters = true
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
	// NexthopCounterNames is the deduplicated, sorted set of per-nexthop
	// counter names reachable through this config, so the metrics path
	// can query them without re-walking the FIB.
	NexthopCounterNames []string
	// UpdatedAt is when the FIB was applied to the dataplane, and backs
	// the staleness gauge.
	UpdatedAt time.Time
}

// Free releases the module handle held by the config.
//
// It is safe to call even when no handle is held.
func (m *configEntry) Free() error {
	if m.Handle == nil {
		return nil
	}
	return m.Handle.Free()
}

// RouteService is the gRPC service implementation backing the slim
// route-module shim.
type RouteService struct {
	routepb.UnimplementedRouteServiceServer

	backend Backend

	// deferred holds superseded module handles whose free was refused
	// because a live configuration generation still referenced them.
	// This service is their owner: it retries them on its next update,
	// through ReclaimDeferred, and nothing else remembers them.
	deferred []ModuleHandle

	// shmLock serializes shared-memory mutations and protects the
	// configs map.
	shmLock sync.RWMutex
	configs map[string]configEntry

	metrics *grpcmetrics.ServerMetrics

	// disableNexthopCounters mirrors Config.DisableNexthopCounters: when
	// set, UpdateFIB rejects an explicit counter and never materializes
	// one for an empty nexthop.
	disableNexthopCounters bool
}

// NewRouteService builds a RouteService bound to the supplied backend.
func NewRouteService(backend Backend, options ...RouteServiceOption) *RouteService {
	opts := newRouteServiceOptions()
	for _, o := range options {
		o(opts)
	}

	m := &RouteService{
		backend:                backend,
		configs:                map[string]configEntry{},
		disableNexthopCounters: opts.DisableNexthopCounters,
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
		Entries: make([]*routepb.FIBEntry, 0, len(entries)),
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
				DstMac:  commonpb.NewMACAddressEUI48([6]byte(nh.DstMAC)),
				SrcMac:  commonpb.NewMACAddressEUI48([6]byte(nh.SrcMAC)),
				Device:  nh.Device,
				Counter: nh.Counter,
			}
		}

		ipRange, err := commonpb.NewIPRange(e.PrefixFrom, e.PrefixTo)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to build IP range from FIB entry: %v", err)
		}

		response.Entries = append(response.Entries, &routepb.FIBEntry{
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
	m.reclaimDeferred()
	m.parkOrFree(entry.Handle)
	delete(m.configs, name)

	return &routepb.DeleteConfigResponse{}, nil
}

// nexthopCounterPrefix keeps a nexthop counter name from colliding with a
// route_* name or a generic per-module one ("rx", "tx", "drop", ...).
const nexthopCounterPrefix = "nexthop_"

func materializeNexthopCounter(device string, dstMAC [6]byte) string {
	return nexthopCounterPrefix + device + "_" + hex.EncodeToString(dstMAC[:])
}

// resolveNexthopCounters validates and materializes nexthop counter names
// across entries.
//
// A generated name is never truncated on overflow: the trailing MAC is what
// makes two nexthops distinct, so truncating would merge their counters.
//
// The conflict check spans the whole request: backend.UpdateModule keys a
// nexthop's route by hardware identity alone, so only the first entry for
// an identity sets its counter, and a per-entry check could miss the clash.
func (m *RouteService) resolveNexthopCounters(entries []*routepb.FIBEntry) error {
	identityCounters := map[HardwareRoute]string{}

	for _, entry := range entries {
		for _, nh := range entry.GetNexthops() {
			counter := nh.GetCounter()

			if m.disableNexthopCounters {
				if counter != "" {
					return status.Errorf(
						codes.InvalidArgument,
						"nexthop for device %q (dst_mac=%x) carries a counter name but nexthop counters are disabled (disable_nexthop_counters)",
						nh.GetDevice(),
						nh.GetDstMac().GetAddr(),
					)
				}
				continue
			}

			generated := counter == ""
			if generated {
				counter = materializeNexthopCounter(nh.GetDevice(), nh.GetDstMac().EUI48())
				nh.Counter = counter
			} else if !strings.HasPrefix(counter, nexthopCounterPrefix) {
				return status.Errorf(
					codes.InvalidArgument,
					"nexthop counter %q must start with %q",
					counter,
					nexthopCounterPrefix,
				)
			}

			// Rejected rather than silently truncated at the C strnlen
			// boundary, which would diverge from the name registered later.
			if strings.IndexByte(counter, 0) >= 0 {
				return status.Errorf(
					codes.InvalidArgument,
					"nexthop counter %q must not contain a NUL byte",
					counter,
				)
			}

			if len(counter) > croute.CounterNameMaxLen {
				if generated {
					return status.Errorf(
						codes.InvalidArgument,
						"generated nexthop counter %q for device %q exceeds the maximum length of %d",
						counter,
						nh.GetDevice(),
						croute.CounterNameMaxLen,
					)
				}
				return status.Errorf(
					codes.InvalidArgument,
					"nexthop counter %q exceeds the maximum length of %d",
					counter,
					croute.CounterNameMaxLen,
				)
			}

			// Identity-parse failures are left for backend.UpdateModule to
			// reject — this check only needs the identity, not full validation.
			if hardwareRoute, err := newHardwareRoute(nh); err == nil {
				if prior, ok := identityCounters[hardwareRoute]; ok && prior != counter {
					return status.Errorf(
						codes.InvalidArgument,
						"nexthop %s (device %q) carries conflicting counter names %q and %q",
						hardwareRoute,
						hardwareRoute.Device,
						prior,
						counter,
					)
				}
				identityCounters[hardwareRoute] = counter
			}
		}
	}

	return nil
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

	entries := req.GetEntries()
	for _, entry := range entries {
		start, end, err := entry.GetRange().ToRange()
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "failed to parse range: %v", err)
		}
		if start.Compare(end) > 0 {
			return nil, status.Errorf(codes.InvalidArgument, "invalid range: start %s is after end %s", start, end)
		}
	}

	// Runs before the backend call: a disabled-but-set or over-long name is
	// rejected before anything is applied, and the generated name written
	// onto nh.Counter is what the backend and the FIBEntry list agree on.
	if err := m.resolveNexthopCounters(entries); err != nil {
		return nil, err
	}

	m.shmLock.Lock()
	defer m.shmLock.Unlock()

	module, err := m.backend.UpdateModule(name, entries)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to apply FIB for %q: %v", name, err)
	}

	// The exported set is the reachable one, read back off the handle
	// rather than the request: an entry a later one fully shadows never
	// materializes a range here, so its counter name is not exported.
	nexthopCounterNames, err := module.ActiveNexthopCounterNames()
	if err != nil {
		// The module is live, so this must never free it here — workers may
		// dereference it. The caller retries on error and resends the
		// whole FIB, so surfacing this one would burn a fresh config
		// generation from the arena, the worst response to memory pressure.
		nexthopCounterNames = nil
	}

	m.reclaimDeferred()

	if old, ok := m.configs[name]; ok {
		m.parkOrFree(old.Handle)
	}

	// The counts are read once, here, off the handle just published: the
	// FIB never changes again for this generation, and each read walks the
	// LPM keyspace.
	m.configs[name] = configEntry{
		Handle:              module,
		FIBRangeCountV4:     module.FIBRangeCountV4(),
		FIBRangeCountV6:     module.FIBRangeCountV6(),
		NexthopCount:        module.RouteCount(),
		NexthopCounterNames: nexthopCounterNames,
		UpdatedAt:           time.Now(),
	}

	return &routepb.UpdateFIBResponse{}, nil
}

// Metrics returns route module metrics matching tags: per-config FIB
// gauges, the module-level dataplane counters, the per-nexthop dataplane
// counters, plus gRPC call metrics.
//
// A "counter" tag is pushed down into both dataplane counter reads, so
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
//   - counter:      per-nexthop counter name (route_nexthop_packets /
//     route_nexthop_bytes only)
//   - grpc_type:    always "unary" (gRPC metrics)
//   - grpc_service: fully-qualified gRPC service name (gRPC metrics)
//   - grpc_method:  RPC name (gRPC metrics)
//   - grpc_code:    gRPC status code string (grpc_server_handled_total only)
func (m *RouteService) Metrics(tags ...*commonpb.MetricTag) ([]*commonpb.Metric, error) {
	result := m.collectConfigMetrics()
	result = append(result, m.collectDataplaneMetrics(tags)...)
	result = append(result, m.collectNexthopMetrics(tags)...)
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

// parkOrFree frees the handle when it is dangling and parks it for
// retry when a live generation still references it. The caller must
// hold m.shmLock.
func (m *RouteService) parkOrFree(handle ModuleHandle) {
	if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
		m.deferred = append(m.deferred, handle)
	}
}

// ReclaimDeferred retries every deferred handle, dropping the ones whose
// generations have drained and keeping the rest deferred. It is the
// reclamation handler for this module's superseded configs; the service
// itself runs it after each successful publish, and anything else may
// call it at any time.
func (m *RouteService) ReclaimDeferred() {
	m.shmLock.Lock()
	defer m.shmLock.Unlock()
	m.reclaimDeferred()
}

// reclaimDeferred is ReclaimDeferred without the lock. The caller must
// hold m.shmLock.
func (m *RouteService) reclaimDeferred() {
	kept := m.deferred[:0]
	for _, handle := range m.deferred {
		if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
			kept = append(kept, handle)
		}
	}
	clear(m.deferred[len(kept):])
	m.deferred = kept
}
