package acl

import (
	"context"
	"slices"
	"sort"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"google.golang.org/protobuf/proto"

	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	aclpb "github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
)

// ModuleHandle is a handle to an ACL module configuration written to
// shared memory. Operations on the handle mutate the underlying C config;
// Free releases it.
type ModuleHandle interface {
	Free()
	AsFFIModule() ffi.ModuleConfig
	UpdateRules(rules []cacl.AclRule) error
	SetFwStateConfig(fw ffi.ModuleConfig)
	TransferFwStateConfig(old ffi.ModuleConfig)
	GetInfo() *cacl.AclConfigInfo
}

// Backend abstracts shared-memory operations for the ACL service.
type Backend interface {
	// NewModule allocates a new ACL module config in shared memory.
	// The returned handle is not yet published to the dataplane.
	NewModule(name string) (ModuleHandle, error)
	// UpdateModule publishes handle to dp_config_gen so the dataplane
	// picks it up on the next round.
	UpdateModule(handle ModuleHandle) error
	// DeleteModule removes a module config from the dataplane.
	DeleteModule(name string) error
	// TODO: remove this
	MemoryBytes() uint64
	// DPConfig returns the dataplane configuration handle for counter
	// and position queries.
	DPConfig() *ffi.DPConfig
}

// Option configures an ACLService.
type Option func(*options)

// options holds the optional parameters for ACLService construction.
type options struct {
	Metrics grpcmetrics.Factory
	Log     *zap.Logger
}

func newOptions() *options {
	return &options{
		Log: zap.NewNop(),
	}
}

// WithLog sets the service logger.
func WithLog(log *zap.Logger) Option {
	return func(o *options) {
		o.Log = log
	}
}

// WithMetrics sets the gRPC metrics factory.
//
// When unset, no metrics are collected.
func WithMetrics(factory grpcmetrics.Factory) Option {
	return func(o *options) {
		o.Metrics = factory
	}
}

type aclConfig struct {
	rules       []*aclpb.Rule
	acl         ModuleHandle
	fwstateName string
}

// Free releases the module handle held by the config.
//
// It is safe to call even when no handle is held.
func (m *aclConfig) Free() {
	if m.acl != nil {
		m.acl.Free()
	}
}

// configEntry holds the per-name state that lives outside m.mu: the lock
// serializing mutations of one config name, and the currently published
// config for that name.
//
// UpdateConfig is the only mutation that can create an entry: it is the
// only one of the four with*-calling RPCs that ever reaches withEntry or
// withEntries for a name with no existing entry. DeleteConfig and
// checkLinkable (LinkConfigs' pre-check) reject an unknown name before
// either gets there, and RelinkConfigs takes its names from
// linkedConfigNames, which only lists names already in the map, so none of
// the other three mutation paths ever interns one. Once created, an
// entry is never removed: DeleteConfig sets published to nil instead of
// dropping the map entry, keeping it as the lock anchor. Every mutation
// RPC is operator-driven, so the key set stays low-cardinality, and an
// entry costs one idle mutex.
//
// Acquiring a name's lock is a two-step operation: a caller fetches the
// entry pointer while holding m.mu, releases m.mu, and only then locks the
// entry's updateMu. Between those two steps a goroutine holds a bare entry
// pointer that no lock protects yet. If entries could be removed from the
// map, another goroutine could delete that entry and insert a fresh one for
// the same name during exactly that window. The first goroutine would then
// go on to lock the orphaned entry while the second locks its replacement,
// and both would believe they had serialized the same name when in fact
// they had not. Keeping entries append-only removes that class of race by
// construction, since the entry for a name stays the same object for the
// whole life of the process.
type configEntry struct {
	// updateMu serializes mutations of this name for the entry's whole
	// life, across the whole operation including a C compile: UpdateConfig,
	// DeleteConfig, and the fwstate adapter's link/relink of this name all
	// hold it for their duration.
	updateMu sync.Mutex
	// published is the currently active config for this name, or nil when
	// the name is absent (a tombstone left by DeleteConfig or a name that
	// was only ever touched by a failed mutation).
	//
	// It is written only while holding both updateMu and m.mu.Lock.
	//
	// An updateMu holder may read its own entry's published field without
	// m.mu, since it is the only possible writer while updateMu is held.
	published *aclConfig
}

// ACLService implements the gRPC ACL service.
type ACLService struct {
	aclpb.UnimplementedACLServiceServer

	// mu guards configs (including insertion of a fresh entry) and the
	// metrics snapshot's ordering relative to it.
	//
	// A read-only critical section under mu.RLock is always a short map
	// read: of configs itself, or of an entry's published field. A
	// mutating critical section under mu.Lock is a map insert, a swap of
	// an entry's published field, or both, followed by rebuilding and
	// publishing the metrics snapshot from the state just installed —
	// every mutation site does both under the same lock acquisition so
	// snapshot publishes stay totally ordered with map mutations. The
	// snapshot rebuild calls GetInfo, a cgo accessor that copies a handful
	// of integers out of an already-compiled C module, so it is cheap to
	// run under mu.Lock. The long-running work (backend.NewModule,
	// handle.UpdateRules, backend.UpdateModule, and the C compile they
	// trigger) runs under the target entry's updateMu instead, outside any
	// mu section, so a compile for one config never blocks a read or a
	// compile for another.
	mu      sync.RWMutex
	backend Backend
	// configs maps a name to its entry. See configEntry for the entry
	// lifecycle and locking rules.
	configs map[string]*configEntry
	metrics *grpcmetrics.ServerMetrics

	metricsState *aclMetricsState

	log *zap.Logger
}

// NewACLService creates an ACL gRPC service backed by the given Backend.
func NewACLService(backend Backend, options ...Option) *ACLService {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	m := &ACLService{
		backend:      backend,
		configs:      map[string]*configEntry{},
		metricsState: newACLMetricsState(),
		log:          opts.Log,
	}
	if opts.Metrics != nil {
		m.metrics = opts.Metrics(m.retention)
	}

	return m
}

// entry returns the configEntry for name, creating it under a brief write
// lock the first time name is touched.
//
// The returned pointer is stable for the entry's whole life, so a caller
// may keep it after releasing m.mu and use it to acquire updateMu.
func (m *ACLService) entry(name string) *configEntry {
	m.mu.RLock()
	e, ok := m.configs[name]
	m.mu.RUnlock()
	if ok {
		return e
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if e, ok := m.configs[name]; ok {
		return e
	}
	e = &configEntry{}
	m.configs[name] = e
	return e
}

// hasEntry reports whether name already has an entry in configs, regardless
// of whether that entry's published config is live or tombstoned.
//
// It takes m.mu.RLock for the lookup. DeleteConfig and checkLinkable use it
// as a read-only pre-check before a mutation reaches withEntry or
// withEntries, which would otherwise intern an entry for a name regardless
// of whether one already exists. Existence alone is the correct test for
// that pre-check: an entry is created only by UpdateConfig, so one already
// being present means the name was created at some point, or is being
// created right now by an UpdateConfig whose compile has not finished. In
// either case falling through to the locked path below is the right
// outcome, since it interns nothing for a name that already has an entry
// and its check of published runs under the name's own lock, after any
// in-flight compile for the name has finished. A name with no entry at all
// can never reach that state through any other path, so rejecting it here
// is exact, not an approximation refined later.
func (m *ACLService) hasEntry(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.configs[name]
	return ok
}

// withEntry fetches or creates the entry for name, holds its updateMu for
// the duration of fn, then returns fn's error.
//
// updateMu is locked and unlocked only here and in withEntries, nowhere
// else in the package. That is what makes mutation serialization for a
// name exhaustive: grep the package for updateMu.Lock( and
// updateMu.Unlock( and every match resolves to one of these two helpers.
func (m *ACLService) withEntry(name string, fn func(*configEntry) error) error {
	entry := m.entry(name)
	entry.updateMu.Lock()
	defer entry.updateMu.Unlock()

	return fn(entry)
}

// withEntries dedups and sorts names, fetches or creates the entry for
// each, locks their updateMu in sorted order, then runs fn with the
// entries keyed by name before unlocking in reverse order and returning
// fn's error.
//
// A consistent lock order across every multi-name caller avoids
// deadlocks between two calls that share some names but list them in a
// different order.
//
// updateMu is locked and unlocked only here and in withEntry, nowhere else
// in the package. That is what makes mutation serialization for a name
// exhaustive: grep the package for updateMu.Lock( and updateMu.Unlock( and
// every match resolves to one of these two helpers.
func (m *ACLService) withEntries(names []string, fn func(map[string]*configEntry) error) error {
	seen := make(map[string]struct{}, len(names))
	sorted := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	entries := make(map[string]*configEntry, len(sorted))
	locked := make([]*configEntry, len(sorted))
	for idx, name := range sorted {
		e := m.entry(name)
		e.updateMu.Lock()
		locked[idx] = e
		entries[name] = e
	}
	defer func() {
		for _, e := range slices.Backward(locked) {
			e.updateMu.Unlock()
		}
	}()

	return fn(entries)
}

// UnaryServerInterceptor returns the service's gRPC metrics interceptor, or nil
// when metrics are not configured.
func (m *ACLService) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	if m.metrics == nil {
		return nil
	}

	return m.metrics.UnaryServerInterceptor()
}

// retention keeps metrics for active configs.
func (m *ACLService) retention() func(metrics.MetricID) bool {
	snapshot := m.metricsState.load()

	return func(id metrics.MetricID) bool {
		config := id.Labels["config"]
		if config == "" {
			return true
		}

		return snapshot.containsConfig(config)
	}
}

// publishMetricsSnapshotLocked rebuilds metric metadata from configs and
// publishes it.
//
// The caller must already hold m.mu.Lock and must call this before
// unlocking, in the same critical section that just installed the map state
// being snapshotted. That ordering is what keeps snapshot publishes totally
// ordered with map mutations: a publish computed from an older map state can
// never run after, and so can never overwrite, a publish computed from a
// newer one. Calling it while holding only m.mu.RLock is a bug, since two
// readers could then publish concurrently and race each other.
func (m *ACLService) publishMetricsSnapshotLocked() {
	configInfos := make(map[string]cacl.AclConfigInfo, len(m.configs))
	for name, entry := range m.configs {
		if entry.published == nil || entry.published.acl == nil {
			continue
		}
		configInfos[name] = *entry.published.acl.GetInfo()
	}

	m.metricsState.publish(configInfos)
}

func labeler(fullMethod string, req any) metrics.Labels {
	switch r := req.(type) {
	case *aclpb.UpdateConfigRequest:
		return metrics.Labels{"config": r.GetName()}
	case *aclpb.DeleteConfigRequest:
		return metrics.Labels{"config": r.GetName()}
	case *aclpb.ShowConfigRequest:
		return metrics.Labels{"config": r.GetName()}
	default:
		return nil
	}
}

func convertRules(reqRules []*aclpb.Rule) ([]cacl.AclRule, error) {
	rules := make([]cacl.AclRule, 0, len(reqRules))
	for _, reqRule := range reqRules {
		devices, err := filterpb.ToDevices(reqRule.Devices)
		if err != nil {
			return nil, err
		}
		vlanRanges, err := filterpb.ToVlanRanges(reqRule.VlanRanges)
		if err != nil {
			return nil, err
		}
		src4s, err := filterpb.ToNet4s(reqRule.Srcs)
		if err != nil {
			return nil, err
		}
		dst4s, err := filterpb.ToNet4s(reqRule.Dsts)
		if err != nil {
			return nil, err
		}
		src6s, err := filterpb.ToNet6s(reqRule.Srcs)
		if err != nil {
			return nil, err
		}
		dst6s, err := filterpb.ToNet6s(reqRule.Dsts)
		if err != nil {
			return nil, err
		}
		protoRanges, err := filterpb.ToProtoRanges(reqRule.ProtoRanges)
		if err != nil {
			return nil, err
		}
		srcPortRanges, err := filterpb.ToPortRanges(reqRule.SrcPortRanges)
		if err != nil {
			return nil, err
		}
		dstPortRanges, err := filterpb.ToPortRanges(reqRule.DstPortRanges)
		if err != nil {
			return nil, err
		}
		actions, err := aclpb.ToActions(reqRule.Actions)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid actions in rule: %v", err)
		}
		fragment, err := filterpb.ToFragment(reqRule.Fragment)
		if err != nil {
			return nil, err
		}
		rule := cacl.AclRule{
			Actions:       actions,
			Counter:       reqRule.GetCounter(),
			Devices:       devices,
			VlanRanges:    vlanRanges,
			Src4s:         src4s,
			Dst4s:         dst4s,
			Src6s:         src6s,
			Dst6s:         dst6s,
			ProtoRanges:   protoRanges,
			SrcPortRanges: srcPortRanges,
			DstPortRanges: dstPortRanges,
			Fragment:      fragment,
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func rulesEqual(a, b []*aclpb.Rule) bool {
	if len(a) != len(b) {
		return false
	}
	for idx := range a {
		if !proto.Equal(a[idx], b[idx]) {
			return false
		}
	}
	return true
}

func (m *ACLService) UpdateConfig(
	ctx context.Context,
	req *aclpb.UpdateConfigRequest,
) (*aclpb.UpdateConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}
	// Rejecting an empty ruleset is enforced here in the Go control plane by
	// design, not in the C shared-memory load path. A matching C-side guard
	// can be added later if a non-Go caller ever needs the same protection.
	if len(req.GetRules()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one rule is required, an empty ruleset would drop all traffic")
	}

	var resp *aclpb.UpdateConfigResponse
	err := m.withEntry(name, func(entry *configEntry) error {
		oldConfig := entry.published

		if oldConfig != nil && rulesEqual(oldConfig.rules, req.Rules) {
			resp = &aclpb.UpdateConfigResponse{}
			return nil
		}

		rules, err := convertRules(req.Rules)
		if err != nil {
			return err
		}

		handle, err := m.backend.NewModule(name)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to create module config: %v", err)
		}

		if err := handle.UpdateRules(rules); err != nil {
			handle.Free()
			return status.Errorf(codes.Internal, "failed to update module config: %v", err)
		}

		fwstateName := ""
		if oldConfig != nil {
			fwstateName = oldConfig.fwstateName
		}

		if fwstateName != "" {
			handle.TransferFwStateConfig(oldConfig.acl.AsFFIModule())
			m.log.Info("transferred fwstate config for ACL module", zap.String("config", name))
		}

		if err := m.backend.UpdateModule(handle); err != nil {
			handle.Free()
			return status.Errorf(codes.Internal, "failed to update module: %v", err)
		}

		m.mu.Lock()
		entry.published = &aclConfig{
			rules:       req.Rules,
			acl:         handle,
			fwstateName: fwstateName,
		}
		m.publishMetricsSnapshotLocked()
		m.mu.Unlock()

		if oldConfig != nil {
			oldConfig.Free()
		}

		resp = &aclpb.UpdateConfigResponse{}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return resp, nil
}

func (m *ACLService) ShowConfig(
	ctx context.Context,
	req *aclpb.ShowConfigRequest,
) (*aclpb.ShowConfigResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	entry, ok := m.configs[name]
	if !ok || entry.published == nil {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	response := &aclpb.ShowConfigResponse{
		Name:        name,
		Rules:       entry.published.rules,
		FwstateName: entry.published.fwstateName,
	}

	return response, nil
}

func (m *ACLService) ListConfigs(
	ctx context.Context,
	req *aclpb.ListConfigsRequest,
) (*aclpb.ListConfigsResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	response := &aclpb.ListConfigsResponse{
		Configs: make([]string, 0, len(m.configs)),
	}

	for name, entry := range m.configs {
		if entry.published == nil {
			continue
		}
		response.Configs = append(response.Configs, name)
	}

	return response, nil
}

func (m *ACLService) DeleteConfig(
	ctx context.Context,
	req *aclpb.DeleteConfigRequest,
) (*aclpb.DeleteConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	// A read-only pre-check rejects a name with no entry at all before
	// withEntry would create one for it. A name whose entry already exists,
	// live or tombstoned or still being created by an in-flight
	// UpdateConfig, falls through to the locked path below, whose
	// authoritative re-check of published under the name's own lock decides
	// the outcome once any in-flight compile for the name has finished.
	// Skipping this pre-check entirely would still be correct, since that
	// locked re-check catches every case on its own. But skipping it would
	// also intern an entry for a name that never existed, and never remove
	// it.
	if !m.hasEntry(name) {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	err := m.withEntry(name, func(entry *configEntry) error {
		config := entry.published
		if config == nil {
			return status.Errorf(codes.NotFound, "config %q not found", name)
		}

		if config.acl != nil {
			if err := m.backend.DeleteModule(name); err != nil {
				return status.Errorf(codes.Internal, "could not delete acl module config '%s': %v", name, err)
			}
			m.log.Info("successfully deleted ACL module config", zap.String("name", name))
		}

		m.mu.Lock()
		entry.published = nil
		m.publishMetricsSnapshotLocked()
		m.mu.Unlock()

		config.Free()

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &aclpb.DeleteConfigResponse{}, nil
}
