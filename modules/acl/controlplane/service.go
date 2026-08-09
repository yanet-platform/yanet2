package acl

import (
	"context"
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
	"github.com/yanet-platform/yanet2/modules/fwstate/bindings/go/cfwstate"
)

// ModuleHandle is a handle to an ACL module configuration written to
// shared memory. Operations on the handle mutate the underlying C config;
// Free releases it.
type ModuleHandle interface {
	Free()
	AsFFIModule() ffi.ModuleConfig
	// Update compiles rules and optionally links the named fwstate-map
	// objects (fw4Name/fw6Name plus an optional sync config) in a single
	// C call. Pass empty fw4Name/fw6Name when the ruleset references no
	// fwstate map.
	Update(rules []cacl.AclRule, fw4Name, fw6Name string, syncConfig *cfwstate.SyncConfig) error
	GetInfo() *cacl.AclConfigInfo
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
	rules         []*aclpb.Rule
	acl           ModuleHandle
	fwtableNameV4 string
	fwtableNameV6 string
	syncConfig    *aclpb.SyncConfig
}

// Rules returns the cached proto rules.
func (m aclConfig) Rules() []*aclpb.Rule { return m.rules }

// Handle returns the compiled module handle.
func (m aclConfig) Handle() ModuleHandle { return m.acl }

// FwtableNameV4 returns the name of the referenced v4 fwstate-map.
func (m aclConfig) FwtableNameV4() string { return m.fwtableNameV4 }

// FwtableNameV6 returns the name of the referenced v6 fwstate-map.
func (m aclConfig) FwtableNameV6() string { return m.fwtableNameV6 }

// UsesFwtable reports whether this config references the given fwstate-map
// name as either its v4 or v6 table.
func (m aclConfig) UsesFwtable(mapName string) bool {
	return m.fwtableNameV4 == mapName || m.fwtableNameV6 == mapName
}

// SyncConfig returns the stored sync configuration.
func (m aclConfig) SyncConfig() *aclpb.SyncConfig { return m.syncConfig }

// ACLService implements the gRPC ACL service.
type ACLService struct {
	aclpb.UnimplementedACLServiceServer

	mu      sync.Mutex
	backend Backend
	configs map[string]aclConfig
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
		configs:      map[string]aclConfig{},
		metricsState: newACLMetricsState(),
		log:          opts.Log,
	}
	if opts.Metrics != nil {
		m.metrics = opts.Metrics(m.retention)
	}

	return m
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

// publishMetricsSnapshotLocked refreshes metric metadata. Caller must hold m.mu.
func (m *ACLService) publishMetricsSnapshotLocked() {
	configInfos := make(map[string]cacl.AclConfigInfo, len(m.configs))
	for name, cfg := range m.configs {
		if cfg.Handle() == nil {
			continue
		}
		configInfos[name] = *cfg.Handle().GetInfo()
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

	// fwtable_name_v4 / fwtable_name_v6 are allowed for any stateful
	// ruleset (CREATE_STATE or CHECK_STATE) and required when any rule uses
	// CREATE_STATE: the ACL dataplane links the named fwstate-map objects
	// to read or write firewall state. sync_config carries the sync-packet
	// parameters the dataplane copies on CREATE_STATE; it is required for
	// CREATE_STATE (the map is written and sync packets are emitted) and
	// optional otherwise (e.g. a CHECK_STATE-only ruleset that only reads
	// state).
	needsCreateState := rulesNeedCreateState(req.Rules)
	fwtableNameV4 := req.GetFwtableNameV4()
	fwtableNameV6 := req.GetFwtableNameV6()
	syncConfig := req.GetSyncConfig()
	if needsCreateState {
		if fwtableNameV4 == "" || fwtableNameV6 == "" {
			return nil, status.Error(codes.InvalidArgument,
				"fwtable_name_v4 and fwtable_name_v6 are required when any rule uses ACTION_KIND_CREATE_STATE")
		}
		if syncConfig == nil {
			return nil, status.Error(codes.InvalidArgument,
				"sync_config is required when any rule uses ACTION_KIND_CREATE_STATE")
		}
	}

	// Validate the sync config contents before ToC truncates ports to
	// uint16: missing address/MAC fields, out-of-range ports, and
	// unbounded timeouts are rejected here rather than emitting malformed
	// sync packets from the dataplane.
	if syncConfig != nil {
		if err := syncConfig.Validate(); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid sync config: %v", err)
		}
	}

	// Idempotency: same rules + same fwtable names + same sync_config
	// means no work to do.
	m.mu.Lock()
	if existing, ok := m.configs[name]; ok &&
		rulesEqual(existing.Rules(), req.Rules) &&
		existing.FwtableNameV4() == fwtableNameV4 &&
		existing.FwtableNameV6() == fwtableNameV6 &&
		syncConfigsEqual(existing.SyncConfig(), syncConfig) {
		m.mu.Unlock()
		return &aclpb.UpdateConfigResponse{}, nil
	}
	m.mu.Unlock()

	rules, err := convertRules(req.Rules)
	if err != nil {
		return nil, err
	}

	// Publish the ACL handle with the map names. The dataplane resolves
	// the named objects into per-worker fwtable pointers at ectx build
	// time, so the Go side only forwards the names.
	if err := m.applyUpdate(name, rules, req.Rules, fwtableNameV4, fwtableNameV6, syncConfig); err != nil {
		return nil, err
	}
	return &aclpb.UpdateConfigResponse{}, nil
}

// rulesNeedCreateState reports whether any rule uses CREATE_STATE, the
// action that drives fwstate sync-packet emission and therefore requires
// both the linked fwstate-map (to write state) and sync_config (to emit
// the sync packets). CHECK_STATE-only rulesets also link the map to
// read state but do not require sync_config.
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

// syncConfigsEqual reports whether two ACL SyncConfig protos are equal.
//
// Both nil and both empty compare equal; one nil and one non-nil do not.
func syncConfigsEqual(a, b *aclpb.SyncConfig) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return proto.Equal(a, b)
}

// applyUpdate builds a fresh ACL handle, attaches the rules (and the named
// maps plus sync_config when fw4Name/fw6Name are non-empty), publishes it,
// and swaps the cached config in place. Caller must NOT hold m.mu; this
// method acquires it.
//
// fw4Name and fw6Name are the object names of standalone fwstate-map
// objects; pass empty strings for configs that reference no fwstate map.
// When non-empty the names are always linked; sync_config is attached
// only when non-nil (CREATE_STATE configs carry one, CHECK_STATE-only
// configs do not).
func (m *ACLService) applyUpdate(
	name string,
	rules []cacl.AclRule,
	pbRules []*aclpb.Rule,
	fwtableNameV4 string,
	fwtableNameV6 string,
	syncConfig *aclpb.SyncConfig,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	handle, err := m.backend.NewModule(name)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to create module config: %v", err)
	}

	var syncCfg *cfwstate.SyncConfig
	if fwtableNameV4 != "" || fwtableNameV6 != "" {
		if syncConfig != nil {
			sync := syncConfig.ToC()
			syncCfg = &sync
		}
	}

	if err := handle.Update(rules, fwtableNameV4, fwtableNameV6, syncCfg); err != nil {
		handle.Free()
		return status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	if err := m.backend.UpdateModule(handle); err != nil {
		handle.Free()
		return status.Errorf(codes.Internal, "failed to update module: %v", err)
	}

	oldConfig, ok := m.configs[name]
	if ok {
		if handle := oldConfig.Handle(); handle != nil {
			handle.Free()
		}
	}

	var storedSync *aclpb.SyncConfig
	if syncConfig != nil {
		storedSync = proto.Clone(syncConfig).(*aclpb.SyncConfig)
	}

	m.configs[name] = aclConfig{
		rules:         pbRules,
		acl:           handle,
		fwtableNameV4: fwtableNameV4,
		fwtableNameV6: fwtableNameV6,
		syncConfig:    storedSync,
	}
	m.publishMetricsSnapshotLocked()

	if fwtableNameV4 != "" || fwtableNameV6 != "" {
		m.log.Info("updated ACL module with fwstate-map",
			zap.String("config", name),
			zap.String("fwtable_v4", fwtableNameV4),
			zap.String("fwtable_v6", fwtableNameV6))
	}
	return nil
}

func (m *ACLService) ShowConfig(
	ctx context.Context,
	req *aclpb.ShowConfigRequest,
) (*aclpb.ShowConfigResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	config, ok := m.configs[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	response := &aclpb.ShowConfigResponse{
		Name:          name,
		Rules:         config.Rules(),
		FwtableNameV4: config.FwtableNameV4(),
		FwtableNameV6: config.FwtableNameV6(),
		SyncConfig:    config.SyncConfig(),
	}

	return response, nil
}

func (m *ACLService) ListConfigs(
	ctx context.Context,
	req *aclpb.ListConfigsRequest,
) (*aclpb.ListConfigsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	response := &aclpb.ListConfigsResponse{
		Configs: make([]string, 0, len(m.configs)),
	}

	for name := range m.configs {
		response.Configs = append(response.Configs, name)
	}

	return response, nil
}

func (m *ACLService) DeleteConfig(
	ctx context.Context,
	req *aclpb.DeleteConfigRequest,
) (*aclpb.DeleteConfigResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	config, ok := m.configs[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	if handle := config.Handle(); handle != nil {
		if err := m.backend.DeleteModule(name); err != nil {
			return nil, status.Errorf(codes.Internal, "could not delete acl module config '%s': %v", name, err)
		}
		m.log.Info("successfully deleted ACL module config", zap.String("name", name))
		handle.Free()
	}

	delete(m.configs, name)
	m.publishMetricsSnapshotLocked()

	response := &aclpb.DeleteConfigResponse{}

	return response, nil
}

// PutConfigForTest stores a minimal config entry keyed by name with the
// given fwtable names, without compiling rules or resolving a map.
func (m *ACLService) PutConfigForTest(name, fwtableNameV4, fwtableNameV6 string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.configs[name] = aclConfig{
		fwtableNameV4: fwtableNameV4,
		fwtableNameV6: fwtableNameV6,
	}
}
