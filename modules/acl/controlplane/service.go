package acl

import (
	"context"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"google.golang.org/protobuf/proto"

	"github.com/yanet-platform/yanet2/common/filterpb"
	"github.com/yanet-platform/yanet2/common/go/metrics"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb"
)

type ACLService struct {
	aclpb.UnimplementedACLServiceServer

	mu             sync.Mutex
	agent          *ffi.Agent
	configs        map[string]aclConfig
	memoryBytes    uint64
	handlerMetrics handlersMetrics

	log *zap.Logger
}

type aclConfig struct {
	rules       []*aclpb.Rule
	acl         *ModuleConfig
	fwstateName string
}

func NewACLService(agent *ffi.Agent, memoryBytes uint64, log *zap.Logger) *ACLService {
	return &ACLService{
		agent:          agent,
		configs:        make(map[string]aclConfig),
		memoryBytes:    memoryBytes,
		handlerMetrics: newHandlersMetrics(),
		log:            log,
	}
}

// newHandlerTracker creates a latency tracker for a gRPC handler.
//
// Usage pattern in handlers:
//
//	tracker := m.newHandlerTracker("HandlerName")
//	m.mu.Lock()
//	defer m.mu.Unlock()
//	defer tracker.Fix()
//
// Defers execute LIFO, so tracker.Fix() runs before m.mu.Unlock().
// This is intentional: the recorded latency covers the full time the
// handler holds the lock, which is where the actual work happens.
func (m *ACLService) newHandlerTracker(name string) *handlerMetricTracker {
	return newHandlerMetricTracker(
		"acl_handler_call_latency_ms",
		&m.handlerMetrics,
		defaultLatencyBoundsMS,
		metrics.Labels{"handler": name},
	)
}

// //////////////////////////////////////////////////////////////////////////////
func terminalAction(protoActions []*aclpb.Action) AclAction {
	const (
		cACLActionAllow       = 0
		cACLActionDeny        = 1
		cACLActionCheckState  = 3
		cACLActionCreateState = 4
	)

	if len(protoActions) == 0 {
		return AclAction{ID: cACLActionAllow}
	}

	a := protoActions[len(protoActions)-1]
	switch a.GetKind() {
	case aclpb.ActionKind_ACTION_KIND_PASS:
		return AclAction{ID: cACLActionAllow, Counter: a.GetCounter()}
	case aclpb.ActionKind_ACTION_KIND_DENY:
		return AclAction{ID: cACLActionDeny, Counter: a.GetCounter()}
	case aclpb.ActionKind_ACTION_KIND_CHECK_STATE:
		return AclAction{ID: cACLActionCheckState, Counter: a.GetCounter()}
	case aclpb.ActionKind_ACTION_KIND_CREATE_STATE:
		return AclAction{ID: cACLActionCreateState, Counter: a.GetCounter()}
	default:
		return AclAction{ID: cACLActionDeny, Counter: a.GetCounter()}
	}
}

func convertRules(reqRules []*aclpb.Rule) ([]AclRule, error) {
	rules := make([]AclRule, 0, len(reqRules))
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

		rule := AclRule{
			Actions:       []AclAction{terminalAction(reqRule.Actions)},
			Devices:       devices,
			VlanRanges:    vlanRanges,
			Src4s:         src4s,
			Dst4s:         dst4s,
			Src6s:         src6s,
			Dst6s:         dst6s,
			ProtoRanges:   protoRanges,
			SrcPortRanges: srcPortRanges,
			DstPortRanges: dstPortRanges,
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

	tracker := m.newHandlerTracker("UpdateConfig")
	m.mu.Lock()
	defer m.mu.Unlock()
	defer tracker.Fix()

	if existing, ok := m.configs[name]; ok && rulesEqual(existing.rules, req.Rules) {
		return &aclpb.UpdateConfigResponse{}, nil
	}

	rules, err := convertRules(req.Rules)
	if err != nil {
		return nil, err
	}

	config, err := NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create module config: %v", err)
	}

	if err := config.Update(rules); err != nil {
		config.Free()
		return nil, status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	oldConfigs, ok := m.configs[name]
	if ok && oldConfigs.fwstateName != "" {
		m.log.Info("transfer fwstate config for ACL module", zap.String("config", name))
		config.TransferFwStateConfig(oldConfigs.acl)
	}

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}); err != nil {
		config.Free()
		return nil, status.Errorf(codes.Internal, "failed to update module: %v", err)
	}

	if oldConfigs.acl != nil {
		oldConfigs.acl.Free()
	}

	m.configs[name] = aclConfig{
		rules:       req.Rules,
		acl:         config,
		fwstateName: oldConfigs.fwstateName,
	}

	return &aclpb.UpdateConfigResponse{}, nil
}

func (m *ACLService) ShowConfig(
	ctx context.Context,
	req *aclpb.ShowConfigRequest,
) (*aclpb.ShowConfigResponse, error) {
	tracker := m.newHandlerTracker("ShowConfig")
	m.mu.Lock()
	defer m.mu.Unlock()
	defer tracker.Fix()

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
	tracker := m.newHandlerTracker("ListConfigs")
	m.mu.Lock()
	defer m.mu.Unlock()
	defer tracker.Fix()

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
	tracker := m.newHandlerTracker("DeleteConfig")
	m.mu.Lock()
	defer m.mu.Unlock()
	defer tracker.Fix()

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	config, ok := m.configs[name]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "not found")
	}

	if config.acl != nil {
		if err := m.agent.DeleteModuleConfig(name); err != nil {
			return nil, status.Errorf(codes.Internal, "could not delete acl module config '%s': %v", name, err)
		}
		m.log.Info("successfully deleted ACL module config", zap.String("name", name))
		config.acl.Free()
	}

	delete(m.configs, name)

	response := &aclpb.DeleteConfigResponse{}

	return response, nil
}

func (m *ACLService) GetMetrics(
	ctx context.Context,
	req *aclpb.GetMetricsRequest,
) (*aclpb.GetMetricsResponse, error) {
	tracker := m.newHandlerTracker("GetMetrics")
	m.mu.Lock()
	defer m.mu.Unlock()
	defer tracker.Fix()

	metrics, err := m.collectMetrics()
	if err != nil {
		return nil, err
	}

	return &aclpb.GetMetricsResponse{
		Metrics: metrics,
	}, nil
}
