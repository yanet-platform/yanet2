package acl

import (
	"context"
	"errors"

	"github.com/yanet-platform/xnetip"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"google.golang.org/protobuf/proto"

	filterpbconv "github.com/yanet-platform/yanet2/bindings/go/filterpbconv/v1"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/common/go/grpcmetrics"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/controlplane/configstore"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/bindings/go/cacl"
	aclpb "github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb/v1"
	fwstatemap "github.com/yanet-platform/yanet2/objects/fwstate/controlplane"
)

// ModuleHandle is a handle to an ACL module configuration written to
// shared memory. The handle is fully built at construction and never
// updated afterwards; Free releases it.
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

// errUnchanged is reported by the update callback when the request matches
// the published config, so nothing is compiled or published.
var errUnchanged = errors.New("config unchanged")

type aclConfig struct {
	rules      []*aclpb.Rule
	acl        ModuleHandle
	fw4MapName string
	fw6MapName string
	// info is the compile metadata copied out of the module before it
	// was published, so metrics never call into a handle.
	info cacl.AclConfigInfo
}

// Rules returns the rules held by the config.
func (m *aclConfig) Rules() []*aclpb.Rule {
	return m.rules
}

// Info returns the compile metadata of the config's module.
func (m *aclConfig) Info() cacl.AclConfigInfo {
	return m.info
}

// Fw4MapName returns the name of the referenced v4 fwstate-map object.
func (m *aclConfig) Fw4MapName() string {
	return m.fw4MapName
}

// Fw6MapName returns the name of the referenced v6 fwstate-map object.
func (m *aclConfig) Fw6MapName() string {
	return m.fw6MapName
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

// ACLService implements the gRPC ACL service.
type ACLService struct {
	aclpb.UnimplementedACLServiceServer

	backend Backend
	configs *configstore.Store[*aclConfig]
	metrics *grpcmetrics.ServerMetrics

	// moduleMetricsFlight coalesces concurrent structural counter
	// scrapes into one shared collection of per-position reads, run
	// without holding a config lock like every shared-memory read.
	moduleMetricsFlight metricsFlight[[]*commonpb.Metric]
	// ruleMetricsFlight coalesces concurrent per-rule counter reads of
	// both rule RPCs into one shared-memory read, equally without
	// holding a config lock.
	ruleMetricsFlight metricsFlight[[]ffi.CounterGroup]

	log *zap.Logger
}

// NewACLService creates an ACL gRPC service backed by the given Backend.
func NewACLService(backend Backend, options ...Option) *ACLService {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	m := &ACLService{
		backend:             backend,
		configs:             configstore.NewStore[*aclConfig](),
		moduleMetricsFlight: newMetricsFlight[[]*commonpb.Metric]("module_metrics"),
		ruleMetricsFlight:   newMetricsFlight[[]ffi.CounterGroup]("rule_metrics"),
		log:                 opts.Log,
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
	names := m.configs.Names()
	live := make(map[string]struct{}, len(names))
	for _, name := range names {
		live[name] = struct{}{}
	}

	return func(id metrics.MetricID) bool {
		config := id.Labels["config"]
		if config == "" {
			return true
		}

		_, ok := live[config]
		return ok
	}
}

// configInfos returns the compile metadata of every published config.
func (m *ACLService) configInfos() map[string]cacl.AclConfigInfo {
	names := m.configs.Names()
	infos := make(map[string]cacl.AclConfigInfo, len(names))
	for _, name := range names {
		if config, ok := m.configs.Get(name); ok {
			infos[name] = config.Info()
		}
	}

	return infos
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

// mergedNet4s decodes the IPv4 networks a rule carries in the legacy
// mixed-family list and the typed list, legacy entries first.
func mergedNet4s(legacy []*filterpb.IPNet, typed []*commonpb.IPv4Network) ([]xnetip.Contiguous[xnetip.Network4], error) {
	nets, err := filterpbconv.ToNet4s(legacy)
	if err != nil {
		return nil, err
	}
	typedNets, err := filterpbconv.ToNet4sFromNetworks(typed)
	if err != nil {
		return nil, err
	}

	return append(nets, typedNets...), nil
}

// mergedNet6s decodes the IPv6 networks a rule carries in the legacy
// mixed-family list and the typed list, legacy entries first.
func mergedNet6s(legacy []*filterpb.IPNet, typed []*commonpb.IPv6Network) ([]xnetip.BiContiguous, error) {
	nets, err := filterpbconv.ToNet6s(legacy)
	if err != nil {
		return nil, err
	}
	typedNets, err := filterpbconv.ToNet6sFromNetworks(typed)
	if err != nil {
		return nil, err
	}

	return append(nets, typedNets...), nil
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
		src4s, err := mergedNet4s(reqRule.Srcs, reqRule.Sources4)
		if err != nil {
			return nil, err
		}
		dst4s, err := mergedNet4s(reqRule.Dsts, reqRule.Destinations4)
		if err != nil {
			return nil, err
		}
		src6s, err := mergedNet6s(reqRule.Srcs, reqRule.Sources6)
		if err != nil {
			return nil, err
		}
		dst6s, err := mergedNet6s(reqRule.Dsts, reqRule.Destinations6)
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

// validateMapNameOptional applies the C-side round-trip rules to a map
// link name; the empty name declares no link and stays valid.
func validateMapNameOptional(name string) error {
	if name == "" {
		return nil
	}
	return fwstatemap.ValidateMapName(name)
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
	if req.GetSyncConfig() != nil {
		return nil, status.Error(codes.InvalidArgument,
			"sync_config belongs to fwstate")
	}

	fw4MapName := req.GetFwtableNameV4()
	fw6MapName := req.GetFwtableNameV6()
	// A non-empty name must round-trip through the fixed-size C
	// object registry: cp_module_link_object silently truncates
	// longer ones, which could link an entirely different map than
	// the one ShowConfig reports. An empty name stays valid: it
	// declares no link for that family.
	if err := validateMapNameOptional(fw4MapName); err != nil {
		return nil, err
	}
	if err := validateMapNameOptional(fw6MapName); err != nil {
		return nil, err
	}

	err := m.configs.Update(name, func(current *aclConfig, ok bool) (*aclConfig, error) {
		if ok && rulesEqual(current.Rules(), req.Rules) &&
			current.Fw4MapName() == fw4MapName &&
			current.Fw6MapName() == fw6MapName {
			return nil, errUnchanged
		}

		rules, err := convertRules(req.Rules)
		if err != nil {
			return nil, err
		}

		handle, err := m.backend.NewModule(
			name, rules, fw4MapName, fw6MapName,
		)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to create module config: %v", err)
		}

		if err := m.backend.UpdateModule(handle); err != nil {
			if err := handle.Free(); err != nil {
				m.log.Error("failed to free unpublished acl module",
					zap.Error(err))
			}
			if errors.Is(err, ffi.ErrFailedPrecondition) {
				return nil, status.Errorf(codes.FailedPrecondition, "failed to update module: %v", err)
			}
			return nil, status.Errorf(codes.Internal, "failed to update module: %v", err)
		}

		return &aclConfig{
			rules:      req.Rules,
			acl:        handle,
			fw4MapName: fw4MapName,
			fw6MapName: fw6MapName,
			info:       *handle.GetInfo(),
		}, nil
	})
	if err != nil && !errors.Is(err, errUnchanged) {
		return nil, err
	}

	return &aclpb.UpdateConfigResponse{}, nil
}

func (m *ACLService) ShowConfig(
	ctx context.Context,
	req *aclpb.ShowConfigRequest,
) (*aclpb.ShowConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	config, ok := m.configs.Get(name)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	response := &aclpb.ShowConfigResponse{
		Name:          name,
		Rules:         config.Rules(),
		FwtableNameV4: config.Fw4MapName(),
		FwtableNameV6: config.Fw6MapName(),
	}

	return response, nil
}

func (m *ACLService) ListConfigs(
	ctx context.Context,
	req *aclpb.ListConfigsRequest,
) (*aclpb.ListConfigsResponse, error) {
	return &aclpb.ListConfigsResponse{Configs: m.configs.Names()}, nil
}

func (m *ACLService) DeleteConfig(
	ctx context.Context,
	req *aclpb.DeleteConfigRequest,
) (*aclpb.DeleteConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	err := m.configs.Delete(name, func(*aclConfig) error {
		if err := m.backend.DeleteModule(name); err != nil {
			return status.Errorf(codes.Internal, "could not delete acl module config '%s': %v", name, err)
		}
		m.log.Info("successfully deleted ACL module config", zap.String("name", name))

		return nil
	})
	if errors.Is(err, configstore.ErrNotFound) {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}
	if err != nil {
		return nil, err
	}

	return &aclpb.DeleteConfigResponse{}, nil
}

// GetRulesCounters returns the per-rule counters of the named config, or of
// every config when the request names none.
//
// The counters come from the same merged read the rule metrics scrape
// uses, so the two requests never read the family concurrently.
// Counters whose packets and bytes are both zero across all workers are
// omitted, matching the metrics read.
func (m *ACLService) GetRulesCounters(
	ctx context.Context,
	req *aclpb.GetRulesCountersRequest,
) (*aclpb.GetRulesCountersResponse, error) {
	name := req.GetName()

	if name != "" {
		if _, ok := m.configs.Get(name); !ok {
			return nil, status.Errorf(codes.NotFound, "config %q not found", name)
		}
	}

	dpConfig := m.backend.DPConfig()
	if dpConfig == nil {
		return &aclpb.GetRulesCountersResponse{}, nil
	}

	groups, err := m.readRuleCounterGroups(ctx, dpConfig)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, status.Errorf(
			codes.Internal, "failed to read rule counters: %v", err,
		)
	}

	result := make([]*aclpb.RuleCounter, 0)
	for _, group := range groups {
		location := groupLocation(group.Tags)
		if name != "" && location["module_name"] != name {
			continue
		}

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
				Config:   location["module_name"],
				Device:   location["device"],
				Pipeline: location["pipeline"],
				Function: location["function"],
				Chain:    location["chain"],
				Counter:  counter.Name,
				Packets:  packets,
				Bytes:    bytes,
			})
		}
	}

	return &aclpb.GetRulesCountersResponse{Counters: result}, nil
}
