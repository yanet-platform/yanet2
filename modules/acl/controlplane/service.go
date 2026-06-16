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

// ACLService implements the gRPC ACL service.
type ACLService struct {
	aclpb.UnimplementedACLServiceServer

	mu      sync.Mutex
	backend Backend
	configs map[string]aclConfig
	metrics *grpcmetrics.ServerMetrics

	log *zap.Logger
}

// NewACLService creates an ACL gRPC service backed by the given Backend.
func NewACLService(backend Backend, options ...Option) *ACLService {
	opts := newOptions()
	for _, o := range options {
		o(opts)
	}

	m := &ACLService{
		backend: backend,
		configs: map[string]aclConfig{},
		log:     opts.Log,
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

// retention snapshots the live ACL config names and returns a predicate that
// keeps series whose "config" label is still live (or absent).
func (m *ACLService) retention() func(metrics.MetricID) bool {
	m.mu.Lock()
	configNames := make(map[string]struct{}, len(m.configs))
	for name := range m.configs {
		configNames[name] = struct{}{}
	}
	m.mu.Unlock()

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

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.configs[name]; ok && rulesEqual(existing.rules, req.Rules) {
		return &aclpb.UpdateConfigResponse{}, nil
	}

	rules, err := convertRules(req.Rules)
	if err != nil {
		return nil, err
	}

	handle, err := m.backend.NewModule(name)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create module config: %v", err)
	}

	if err := handle.UpdateRules(rules); err != nil {
		handle.Free()
		return nil, status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	oldConfigs, ok := m.configs[name]
	if ok && oldConfigs.fwstateName != "" {
		handle.TransferFwStateConfig(oldConfigs.acl.AsFFIModule())
		m.log.Info("transferred fwstate config for ACL module", zap.String("config", name))
	}

	if err := m.backend.UpdateModule(handle); err != nil {
		handle.Free()
		return nil, status.Errorf(codes.Internal, "failed to update module: %v", err)
	}

	if oldConfigs.acl != nil {
		oldConfigs.acl.Free()
	}

	m.configs[name] = aclConfig{
		rules:       req.Rules,
		acl:         handle,
		fwstateName: oldConfigs.fwstateName,
	}

	return &aclpb.UpdateConfigResponse{}, nil
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
		Name:        name,
		Rules:       config.rules,
		FwstateName: config.fwstateName,
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
		return nil, status.Error(codes.InvalidArgument, "not found")
	}

	if config.acl != nil {
		if err := m.backend.DeleteModule(name); err != nil {
			return nil, status.Errorf(codes.Internal, "could not delete acl module config '%s': %v", name, err)
		}
		m.log.Info("successfully deleted ACL module config", zap.String("name", name))
		config.acl.Free()
	}

	delete(m.configs, name)

	response := &aclpb.DeleteConfigResponse{}

	return response, nil
}

// GetInfo returns the compiled configuration metadata for the named ACL module,
// or nil if no config with that name is loaded.
func (m *ACLService) GetInfo(name string) *cacl.AclConfigInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	cfg, ok := m.configs[name]
	if !ok {
		return nil
	}

	return cfg.acl.GetInfo()
}
