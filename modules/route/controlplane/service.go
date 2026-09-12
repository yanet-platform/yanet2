package route

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/controlplane/configstore"
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
// table object and retires the previous one, while the module config moves
// from entry to entry until its device table has to grow, under the
// store's write lock, the only place handles are read or written. The
// sizes are therefore measured once here rather than walked out of the
// LPM keyspace on every scrape.
type configEntry struct {
	// Module is the published module config, nil when it was published
	// by an earlier process and this one owns no handle for it, or once
	// it moved to a later entry.
	Module ModuleHandle
	// FIB is the published table object, nil while only the module
	// config is published or once it moved to a later entry.
	FIB FIBHandle

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
	// the staleness gauge. Zero when this process did not apply it.
	UpdatedAt time.Time
}

// Free releases the handles the entry still holds, the table object
// first. A refused free leaves the entry retryable, each free being a
// no-op once it succeeded.
func (m *configEntry) Free() error {
	if m.FIB != nil {
		if err := m.FIB.Free(); err != nil {
			return err
		}
	}
	if m.Module != nil {
		return m.Module.Free()
	}
	return nil
}

// RouteService is the gRPC service implementation backing the slim
// route-module shim.
type RouteService struct {
	routepb.UnimplementedRouteServiceServer

	backend Backend

	// configs owns the published configs and retries the retired ones
	// whose free was refused.
	configs *configstore.Store[*configEntry]

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
		configs:                configstore.NewStore[*configEntry](),
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
	names := m.configs.Names()
	configNames := make(map[string]struct{}, len(names))
	for _, name := range names {
		configNames[name] = struct{}{}
	}

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
	return &routepb.ListConfigsResponse{
		Configs: m.configs.Names(),
	}, nil
}

// ShowFIB returns the FIB entries currently applied in shared memory
// for the requested configuration.
//
// The read pins the configuration generation it walks and takes no lock
// of this service, so it neither waits for an update nor delays one.
func (m *RouteService) ShowFIB(
	ctx context.Context,
	req *routepb.ShowFIBRequest,
) (*routepb.ShowFIBResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	entries, err := m.backend.DumpFIB(name)
	if errors.Is(err, ffi.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}
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

	// The module goes first: it links the object, and a linked object
	// refuses deletion. Either half may already be gone after a failed
	// earlier attempt.
	err := m.configs.Delete(name, func(current *configEntry) error {
		if err := m.backend.DeleteModule(name); err != nil && !errors.Is(err, ffi.ErrNotFound) {
			return fmt.Errorf("failed to delete module config: %w", err)
		}
		if err := m.backend.DeleteFIB(name); err != nil && !errors.Is(err, ffi.ErrNotFound) {
			return fmt.Errorf("failed to delete fib object: %w", err)
		}
		return nil
	})
	if errors.Is(err, configstore.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete config %q: %v", name, err)
	}

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
// The conflict check spans the whole request: the FIB build keys a
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

			// Identity-parse failures are left for the FIB build to
			// reject, this check only needs the identity, not full validation.
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
		// A name the module's fixed-size device table cannot hold is a
		// request error, rejected before anything is built.
		for _, nh := range entry.GetNexthops() {
			if err := ffi.ValidateDeviceName(nh.GetDevice()); err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "invalid device name %q: %v", nh.GetDevice(), err)
			}
		}
	}

	// Runs before the backend call: a disabled-but-set or over-long name is
	// rejected before anything is applied, and the generated name written
	// onto nh.Counter is what the backend and the FIBEntry list agree on.
	if err := m.resolveNexthopCounters(entries); err != nil {
		return nil, err
	}

	// A publish that fails halfway still advances the entry to what went
	// out, so the error is carried past the store, which only stores on
	// success.
	var publishErr error
	err := m.configs.Update(name, func(current *configEntry, ok bool) (*configEntry, error) {
		published, err := m.backend.Published(name)
		if err != nil && !errors.Is(err, ffi.ErrNotFound) {
			return nil, fmt.Errorf("failed to read the published config %q: %w", name, err)
		}
		modulePublished := err == nil
		devices, grown := extendDeviceTable(published.Devices, entries)

		// The module moves along from the current entry unless the
		// table needs a device it lacks, then a fresh one takes over.
		entry := &configEntry{}
		if ok {
			entry.Module = current.Module
		}
		moduleNew := !modulePublished || grown
		if moduleNew {
			module, table, err := m.backend.NewModule(name, devices)
			if err != nil {
				return nil, fmt.Errorf("failed to build module config for %q: %w", name, err)
			}
			entry.Module = module
			devices = table
		}
		fib, err := m.backend.NewFIB(name, devices, entries)
		if err != nil {
			if moduleNew {
				m.discard(entry.Module)
			}
			return nil, fmt.Errorf("failed to build FIB for %q: %w", name, err)
		}

		// A first table goes out before its module, a grown one after.
		//
		// A module is refused until the object it links is published.
		// Once one is, the module's append-only device table keeps it
		// valid under the grown module, while the new object would not
		// resolve on the old module.
		moduleFirst := moduleNew && published.FIB
		if moduleFirst {
			if err := entry.Module.Publish(); err != nil {
				m.discard(fib)
				m.discard(entry.Module)
				return nil, fmt.Errorf("failed to publish module config for %q: %w", name, err)
			}
		}
		if err := fib.Publish(); err != nil {
			m.discard(fib)
			err = fmt.Errorf("failed to publish FIB for %q: %w", name, err)
			if !moduleFirst {
				if moduleNew {
					m.discard(entry.Module)
				}
				return nil, err
			}
			// The grown module is out and the published object stays
			// under it. The object moves along when this process owns
			// it, an earlier process's object is not ours to hold, so
			// its facts are read back off the dataplane and its apply
			// time stays unknown.
			publishErr = err
			if ok {
				entry.FIB = current.FIB
				current.FIB = nil
				entry.FIBRangeCountV4 = current.FIBRangeCountV4
				entry.FIBRangeCountV6 = current.FIBRangeCountV6
				entry.NexthopCount = current.NexthopCount
				entry.NexthopCounterNames = current.NexthopCounterNames
				entry.UpdatedAt = current.UpdatedAt
			} else if dumped, err := m.backend.DumpFIB(name); err == nil {
				entry.FIBRangeCountV4, entry.FIBRangeCountV6, entry.NexthopCount, entry.NexthopCounterNames = tableFacts(dumped)
			}
			return entry, nil
		}
		if moduleNew && !moduleFirst {
			if err := entry.Module.Publish(); err != nil {
				// The object is out, the module that was to run it is
				// not, so the entry keeps whatever module ran before.
				m.discard(entry.Module)
				entry.Module = nil
				if ok {
					entry.Module = current.Module
					current.Module = nil
				}
				publishErr = fmt.Errorf("failed to publish module config for %q: %w", name, err)
			}
		}
		if ok && entry.Module == current.Module {
			current.Module = nil
		}

		// The exported set is the reachable one, read back off the handle
		// rather than the request: an entry a later one fully shadows never
		// materializes a range here, so its counter name is not exported.
		nexthopCounterNames, err := fib.ActiveNexthopCounterNames()
		if err != nil {
			// The table is live, so this must never free it here: workers
			// may dereference it. The caller retries on error and resends
			// the whole FIB, so surfacing this one would burn a fresh
			// generation from the arena, the worst response to memory
			// pressure.
			nexthopCounterNames = nil
		}

		// The counts are read once, here, off the handle just published:
		// the FIB never changes again for this object, and each read walks
		// the LPM keyspace.
		entry.FIB = fib
		entry.FIBRangeCountV4 = fib.FIBRangeCountV4()
		entry.FIBRangeCountV6 = fib.FIBRangeCountV6()
		entry.NexthopCount = fib.RouteCount()
		entry.NexthopCounterNames = nexthopCounterNames
		entry.UpdatedAt = time.Now()
		return entry, nil
	})
	if err == nil {
		err = publishErr
	}
	if err != nil {
		code := codes.Internal
		if errors.Is(err, ErrTooManyNexthops) {
			code = codes.InvalidArgument
		}
		return nil, status.Error(code, err.Error())
	}

	return &routepb.UpdateFIBResponse{}, nil
}

// discard frees a handle that never got published. Such a handle is
// dangling, so the free cannot be refused.
func (m *RouteService) discard(handle interface{ Free() error }) {
	_ = handle.Free()
}

// tableFacts measures a dumped table: ranges per family, distinct
// hardware nexthops reachable through them and the sorted set of counter
// names in use.
func tableFacts(entries []croute.FIBEntry) (rangesV4, rangesV6, nexthops uint64, counterNames []string) {
	seen := map[HardwareRoute]struct{}{}
	names := map[string]struct{}{}
	for _, entry := range entries {
		if entry.AddressFamily == croute.AddressFamilyIPv4 {
			rangesV4++
		} else {
			rangesV6++
		}
		for _, nh := range entry.Nexthops {
			seen[HardwareRoute{
				SourceMAC:      [6]byte(nh.SrcMAC),
				DestinationMAC: [6]byte(nh.DstMAC),
				Device:         nh.Device,
			}] = struct{}{}
			if nh.Counter != "" {
				names[nh.Counter] = struct{}{}
			}
		}
	}
	return rangesV4, rangesV6, uint64(len(seen)), slices.Sorted(maps.Keys(names))
}

// extendDeviceTable appends the devices the entries name that the table
// lacks, in order of first appearance, and reports whether it grew.
func extendDeviceTable(table []string, entries []*routepb.FIBEntry) ([]string, bool) {
	known := make(map[string]struct{}, len(table))
	for _, device := range table {
		known[device] = struct{}{}
	}

	devices := table
	for _, entry := range entries {
		for _, nh := range entry.GetNexthops() {
			device := nh.GetDevice()
			if _, ok := known[device]; ok {
				continue
			}
			known[device] = struct{}{}
			devices = append(devices, device)
		}
	}
	return devices, len(devices) != len(table)
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
	names := m.configs.Names()
	result := make([]*commonpb.Metric, 0, 4*len(names))
	for _, name := range names {
		entry, ok := m.configs.Get(name)
		if !ok {
			continue
		}
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
		)
		// A table applied by an earlier process has no apply time to
		// report.
		if !entry.UpdatedAt.IsZero() {
			result = append(result, commonpb.NewMetricGauge(
				"route_config_updated_timestamp_seconds",
				float64(entry.UpdatedAt.Unix()),
				configLabels...,
			))
		}
	}

	return result
}

// ReclaimDeferred retries every retired config whose free was refused,
// dropping the ones whose generations have drained. The store runs it
// after each successful mutation, and anything else may call it at any
// time.
func (m *RouteService) ReclaimDeferred() {
	m.configs.ReclaimDeferred()
}
