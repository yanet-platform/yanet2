package acl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"google.golang.org/protobuf/proto"

	filterpbconv "github.com/yanet-platform/yanet2/bindings/go/filterpbconv/v1"
	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	aclpb "github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
	cfwstate "github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
	fwstatemap "github.com/yanet-platform/yanet2/objects/fwstate/controlplane"
)

// ModuleHandle is a handle to an ACL module configuration written to
// shared memory. The handle is fully built — rules compiled, emission
// sync config installed — at construction and never updated afterwards;
// Free releases it.
type ModuleHandle interface {
	Free() error
	AsFFIModule() ffi.ModuleConfig
	GetInfo() *cacl.AclConfigInfo
}

// Backend abstracts shared-memory operations for the ACL service.
type Backend interface {
	// NewModule allocates a new ACL module config in shared memory with
	// the ruleset compiled into it and the named fwstate-map objects
	// linked. The returned handle is not yet published to the dataplane.
	NewModule(
		name string,
		rules []cacl.AclRule,
		fw4MapName, fw6MapName string,
		emitConfig *cfwstate.SyncEmitConfig,
	) (ModuleHandle, error)
	// UpdateModule publishes handle to dp_config_gen so the dataplane
	// picks it up on the next round.
	UpdateModule(handle ModuleHandle) error
	// DeleteModule removes a module config from the dataplane.
	DeleteModule(name string) error
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
	rules      []*aclpb.Rule
	acl        ModuleHandle
	fw4MapName string
	fw6MapName string
	syncConfig *aclpb.SyncConfig
}

// Rules returns the rules held by the config.
func (m *aclConfig) Rules() []*aclpb.Rule {
	return m.rules
}

// Handle returns the module handle held by the config.
func (m *aclConfig) Handle() ModuleHandle {
	return m.acl
}

// Fw4MapName returns the name of the referenced v4 fwstate-map object.
func (m *aclConfig) Fw4MapName() string {
	return m.fw4MapName
}

// Fw6MapName returns the name of the referenced v6 fwstate-map object.
func (m *aclConfig) Fw6MapName() string {
	return m.fw6MapName
}

// SyncConfig returns the stored emission-side sync configuration, or nil
// when the config carries none.
func (m *aclConfig) SyncConfig() *aclpb.SyncConfig {
	return m.syncConfig
}

// Free releases the module handle held by the config.
//
// It is safe to call even when no handle is held.
func (m *aclConfig) Free() error {
	if m.acl == nil {
		return nil
	}
	return m.acl.Free()
}

// configEntry holds the per-name state that lives outside m.mu: the lock
// serializing mutations of one config name, and the currently published
// config for that name.
//
// UpdateConfig is the only mutation that can create an entry: it is the
// only with*-calling RPC that ever reaches withEntry for a name with no
// existing entry. DeleteConfig rejects an unknown name before it gets
// there, so no other mutation path ever interns one. Once created, an
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
	// life, across the whole operation including a C compile: UpdateConfig
	// and DeleteConfig both hold it for their duration.
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

// LockUpdate acquires the entry's update lock.
func (m *configEntry) LockUpdate() {
	m.updateMu.Lock()
}

// UnlockUpdate releases the entry's update lock.
func (m *configEntry) UnlockUpdate() {
	m.updateMu.Unlock()
}

// Published returns the config currently published for the entry.
func (m *configEntry) Published() *aclConfig {
	return m.published
}

// Publish stores the config currently published for the entry.
func (m *configEntry) Publish(config *aclConfig) {
	m.published = config
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
	// run under mu.Lock. The long-running work (backend.NewModule with
	// its C compile, and backend.UpdateModule) runs under the target
	// entry's updateMu instead, outside any mu section, so a compile for
	// one config never blocks a read or a compile for another.
	mu      sync.RWMutex
	backend Backend
	// configs maps a name to its entry. See configEntry for the entry
	// lifecycle and locking rules.
	configs map[string]*configEntry
	metrics *grpcmetrics.ServerMetrics

	metricsState *aclMetricsState

	// deferred holds superseded acl configs whose free was refused
	// because a live configuration generation still referenced them.
	// This service is their owner: it retries them on its next update,
	// through ReclaimDeferred, and nothing else remembers them.
	deferred []*aclConfig

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
// It takes m.mu.RLock for the lookup. DeleteConfig uses it as a read-only
// pre-check before withEntry, which would otherwise intern an entry for a
// name regardless of whether one already exists. Existence alone is the
// correct test for that pre-check: an entry is created only by UpdateConfig,
// so one already being present means the name was created at some point, or
// is being created right now by an UpdateConfig whose compile has not
// finished. In either case falling through to the locked path below is the
// right outcome, since it interns nothing for a name that already has an
// entry and its check of published runs under the name's own lock, after any
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
// Every mutation path acquires and releases the entry lock through the
// LockUpdate and UnlockUpdate receiver methods. Keeping that pair here makes
// serialization exhaustive, including backend work and C compilation.
func (m *ACLService) withEntry(name string, fn func(*configEntry) error) error {
	entry := m.entry(name)
	entry.LockUpdate()
	defer entry.UnlockUpdate()

	return fn(entry)
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
		devices, err := filterpbconv.ToDevices(reqRule.Devices)
		if err != nil {
			return nil, err
		}
		vlanRanges, err := filterpbconv.ToVlanRanges(reqRule.VlanRanges)
		if err != nil {
			return nil, err
		}
		src4s, err := filterpbconv.ToNet4s(reqRule.Srcs)
		if err != nil {
			return nil, err
		}
		dst4s, err := filterpbconv.ToNet4s(reqRule.Dsts)
		if err != nil {
			return nil, err
		}
		src6s, err := filterpbconv.ToNet6s(reqRule.Srcs)
		if err != nil {
			return nil, err
		}
		dst6s, err := filterpbconv.ToNet6s(reqRule.Dsts)
		if err != nil {
			return nil, err
		}
		protoRanges, err := filterpbconv.ToProtoRanges(reqRule.ProtoRanges)
		if err != nil {
			return nil, err
		}
		srcPortRanges, err := filterpbconv.ToPortRanges(reqRule.SrcPortRanges)
		if err != nil {
			return nil, err
		}
		dstPortRanges, err := filterpbconv.ToPortRanges(reqRule.DstPortRanges)
		if err != nil {
			return nil, err
		}
		actions, err := aclpb.ToActions(reqRule.Actions)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid actions in rule: %v", err)
		}
		fragment, err := filterpbconv.ToFragment(reqRule.Fragment)
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

// rulesNeedCreateState reports whether any rule uses CREATE_STATE, the
// action that synthesizes state-sync packets and therefore requires the
// emission-side sync config. CHECK_STATE-only rulesets read state and
// carry no sync config.
func rulesNeedCreateState(rules []*aclpb.Rule) bool {
	for _, rule := range rules {
		for _, action := range rule.GetActions() {
			if action.GetKind() == aclpb.ActionKind_ACTION_KIND_CREATE_STATE {
				return true
			}
		}
	}
	return false
}

// syncConfigsEqual reports whether two sync configs are equal. Both nil
// compares equal; one nil and one non-nil does not.
func syncConfigsEqual(a, b *aclpb.SyncConfig) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return proto.Equal(a, b)
}

// validateSyncConfig rejects an emission sync config that could not
// produce valid sync frames: a missing destination MAC, a missing
// multicast destination pair (the only destination the craft path
// uses), or out-of-range ports.
// validateMapNameOptional applies the C-side round-trip rules to a map
// link name; the empty name declares no link and stays valid.
func validateMapNameOptional(name string) error {
	if name == "" {
		return nil
	}
	return fwstatemap.ValidateMapName(name)
}

func validateSyncConfig(cfg *aclpb.SyncConfig) error {
	if portMulticast := cfg.GetPortMulticast(); portMulticast > maxSyncPort {
		return fmt.Errorf("port_multicast %d exceeds maximum allowed value %d", portMulticast, maxSyncPort)
	}
	if portUnicast := cfg.GetPortUnicast(); portUnicast > maxSyncPort {
		return fmt.Errorf("port_unicast %d exceeds maximum allowed value %d", portUnicast, maxSyncPort)
	}

	var missing []string

	if dstEther := cfg.GetDstEther(); dstEther == nil {
		missing = append(missing, "dst_ether")
	} else {
		eui := dstEther.EUI48()
		if isAllZeroBytes(eui[:]) {
			missing = append(missing, "dst_ether")
		}
	}

	if len(cfg.GetDstAddrMulticast().GetAddr()) != 16 || isAllZeroBytes(cfg.GetDstAddrMulticast().GetAddr()) {
		missing = append(missing, "dst_addr_multicast")
	}
	if cfg.GetPortMulticast() == 0 {
		missing = append(missing, "port_multicast")
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required sync config fields: %v", missing)
	}
	return nil
}

// maxSyncPort is the highest value accepted for the sync config ports,
// matching the width of the C-side uint16 port field.
const maxSyncPort uint32 = 65535

func isAllZeroBytes(b []byte) bool {
	for _, v := range b {
		if v != 0 {
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
		oldConfig := entry.Published()

		fw4MapName := req.GetFwtableNameV4()
		fw6MapName := req.GetFwtableNameV6()
		syncConfig := req.GetSyncConfig()

		// A non-empty name must round-trip through the fixed-size C
		// object registry: cp_module_link_object silently truncates
		// longer ones, which could link an entirely different map than
		// the one ShowConfig reports. An empty name stays valid: it
		// declares no link for that family.
		if err := validateMapNameOptional(fw4MapName); err != nil {
			return err
		}
		if err := validateMapNameOptional(fw6MapName); err != nil {
			return err
		}

		if rulesNeedCreateState(req.Rules) {
			if syncConfig == nil {
				return status.Error(codes.InvalidArgument,
					"sync_config is required when any rule uses ACTION_KIND_CREATE_STATE")
			}
		}
		if syncConfig != nil {
			if err := validateSyncConfig(syncConfig); err != nil {
				return status.Errorf(codes.InvalidArgument, "invalid sync config: %v", err)
			}
		}

		if oldConfig != nil && rulesEqual(oldConfig.Rules(), req.Rules) &&
			oldConfig.Fw4MapName() == fw4MapName &&
			oldConfig.Fw6MapName() == fw6MapName &&
			syncConfigsEqual(oldConfig.SyncConfig(), syncConfig) {
			resp = &aclpb.UpdateConfigResponse{}
			return nil
		}

		rules, err := convertRules(req.Rules)
		if err != nil {
			return err
		}

		var emitCfg *cfwstate.SyncEmitConfig
		if syncConfig != nil {
			emit := syncConfig.ToC()
			emitCfg = &emit
		}

		handle, err := m.backend.NewModule(
			name, rules, fw4MapName, fw6MapName, emitCfg,
		)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to create module config: %v", err)
		}

		if err := m.backend.UpdateModule(handle); err != nil {
			if err := handle.Free(); err != nil {
				m.log.Error("failed to free unpublished acl module",
					zap.Error(err))
			}
			return classifyUpdateError(err)
		}

		var storedSync *aclpb.SyncConfig
		if syncConfig != nil {
			storedSync = proto.Clone(syncConfig).(*aclpb.SyncConfig)
		}

		m.mu.Lock()
		entry.Publish(&aclConfig{
			rules:      req.Rules,
			acl:        handle,
			fw4MapName: fw4MapName,
			fw6MapName: fw6MapName,
			syncConfig: storedSync,
		})
		m.publishMetricsSnapshotLocked()
		m.mu.Unlock()

		// The publish retired the generations holding this service's
		// deferred configs; retry them, then retire the displaced one.
		m.ReclaimDeferred()
		if oldConfig != nil {
			m.parkOrFree(oldConfig)
		}

		resp = &aclpb.UpdateConfigResponse{}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return resp, nil
}

// classifyUpdateError maps a failed module update to its gRPC status.
//
// The C generation install validates every declared object link against
// the published objects and refuses the update with the exact error
// text "linked object '<type>:<name>' not found for module
// '<type>:<name>'". That refusal names a map the request asked for but
// no published object provides — a client-input error, so it surfaces
// as InvalidArgument with the C text intact, mirroring the fwstate
// update path; every other update failure is internal.
func classifyUpdateError(err error) error {
	if strings.Contains(err.Error(), "linked object") {
		return status.Errorf(codes.InvalidArgument, "failed to update module: %v", err)
	}
	return status.Errorf(codes.Internal, "failed to update module: %v", err)
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
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}
	config := entry.Published()
	if config == nil {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	response := &aclpb.ShowConfigResponse{
		Name:          name,
		Rules:         config.Rules(),
		FwtableNameV4: config.Fw4MapName(),
		FwtableNameV6: config.Fw6MapName(),
		SyncConfig:    config.SyncConfig(),
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
		if entry.Published() == nil {
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
		config := entry.Published()
		if config == nil {
			return status.Errorf(codes.NotFound, "config %q not found", name)
		}

		if config.Handle() != nil {
			if err := m.backend.DeleteModule(name); err != nil {
				return status.Errorf(codes.Internal, "could not delete acl module config '%s': %v", name, err)
			}
			m.log.Info("successfully deleted ACL module config", zap.String("name", name))
		}

		m.mu.Lock()
		entry.Publish(nil)
		m.publishMetricsSnapshotLocked()
		m.mu.Unlock()

		// The delete retired the generation holding the published
		// config; retry the deferred ones, then retire this one.
		m.ReclaimDeferred()
		m.parkOrFree(config)

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &aclpb.DeleteConfigResponse{}, nil
}

// GetRulesCounters returns the per-rule counters of the named config, or of
// every config when the request names none.
//
// Counters whose packets and bytes are both zero across all workers are
// omitted, matching the metrics read.
func (m *ACLService) GetRulesCounters(
	ctx context.Context,
	req *aclpb.GetRulesCountersRequest,
) (*aclpb.GetRulesCountersResponse, error) {
	name := req.GetName()

	if name != "" {
		m.mu.RLock()
		entry, ok := m.configs[name]
		m.mu.RUnlock()
		if !ok || entry.Published() == nil {
			return nil, status.Errorf(codes.NotFound, "config %q not found", name)
		}
	}

	dpConfig := m.backend.DPConfig()
	if dpConfig == nil {
		return &aclpb.GetRulesCountersResponse{}, nil
	}

	result := make([]*aclpb.RuleCounter, 0)
	for pos := range dpConfig.AllModulePositions(moduleType) {
		if name != "" && pos.ModuleName != name {
			continue
		}

		// Runtime-kind storages expand exactly the module's per-rule
		// registries, so this read excludes the predefined module
		// counters (rx, tx, acl_action_*, ...) by construction.
		groups, err := dpConfig.CountersByTags([]ffi.CounterTag{
			{Key: "device", Value: pos.Device},
			{Key: "pipeline", Value: pos.Pipeline},
			{Key: "function", Value: pos.Function},
			{Key: "chain", Value: pos.Chain},
			{Key: "module_type", Value: moduleType},
			{Key: "module_name", Value: pos.ModuleName},
			{Key: "kind", Value: "runtime"},
		}, nil)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to read rule counters of config %q: %v", pos.ModuleName, err)
		}

		for _, group := range groups {
			for _, counter := range group.Counters {
				var packets, bytes uint64
				for _, workerVals := range counter.Values {
					if len(workerVals) > 0 {
						packets += workerVals[0]
					}
					if len(workerVals) > 1 {
						bytes += workerVals[1]
					}
				}

				if packets == 0 && bytes == 0 {
					continue
				}

				result = append(result, &aclpb.RuleCounter{
					Config:   pos.ModuleName,
					Device:   pos.Device,
					Pipeline: pos.Pipeline,
					Function: pos.Function,
					Chain:    pos.Chain,
					Counter:  counter.Name,
					Packets:  packets,
					Bytes:    bytes,
				})
			}
		}
	}

	return &aclpb.GetRulesCountersResponse{Counters: result}, nil
}

// parkOrFree frees the config when it is dangling and parks it for
// retry when a live generation still references it.
func (m *ACLService) parkOrFree(config *aclConfig) {
	if err := config.Free(); errors.Is(err, ffi.ErrStillReferenced) {
		m.mu.Lock()
		m.deferred = append(m.deferred, config)
		m.mu.Unlock()
	}
}

// ReclaimDeferred retries every deferred config, dropping the ones whose
// generations have drained and keeping the rest deferred. It is the
// reclamation handler for this module's superseded configs; the service
// itself runs it after each successful publish, and anything else may
// call it at any time.
func (m *ACLService) ReclaimDeferred() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reclaimDeferred()
}

// reclaimDeferred is ReclaimDeferred without the lock. The caller must
// hold m.mu.
func (m *ACLService) reclaimDeferred() {
	kept := m.deferred[:0]
	for _, config := range m.deferred {
		if err := config.Free(); errors.Is(err, ffi.ErrStillReferenced) {
			kept = append(kept, config)
		}
	}
	clear(m.deferred[len(kept):])
	m.deferred = kept
}
