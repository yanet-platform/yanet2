package acl

import (
	"context"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/filter/device"
	"github.com/yanet-platform/yanet2/common/go/filter/ipnet4"
	"github.com/yanet-platform/yanet2/common/go/filter/ipnet6"
	"github.com/yanet-platform/yanet2/common/go/filter/portrange"
	"github.com/yanet-platform/yanet2/common/go/filter/protorange"
	"github.com/yanet-platform/yanet2/common/go/filter/vlanrange"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb"
)

// ACLService implements the gRPC service for ACL management.
type ACLService struct {
	aclpb.UnimplementedACLServiceServer

	mu      sync.Mutex
	agent   *ffi.Agent
	configs map[string]aclConfig

	log *zap.SugaredLogger
}

type aclConfig struct {
	rules       []*aclpb.Rule
	acl         *ModuleConfig
	fwstateName string
}

// NewACLService creates a new ACL service
func NewACLService(agent *ffi.Agent, log *zap.SugaredLogger) *ACLService {
	return &ACLService{
		agent:   agent,
		configs: make(map[string]aclConfig),
		log:     log,
	}
}

////////////////////////////////////////////////////////////////////////////////

func convertRules(reqRules []*aclpb.Rule) ([]AclRule, error) {
	rules := make([]AclRule, 0, len(reqRules))
	for _, reqRule := range reqRules {
		devices, err := device.FromDevices(reqRule.Devices)
		if err != nil {
			return nil, err
		}
		vlanRanges, err := vlanrange.FromVlanRanges(reqRule.VlanRanges)
		if err != nil {
			return nil, err
		}
		src4s, err := ipnet4.FromIPNets(reqRule.Srcs)
		if err != nil {
			return nil, err
		}
		dst4s, err := ipnet4.FromIPNets(reqRule.Dsts)
		if err != nil {
			return nil, err
		}
		src6s, err := ipnet6.FromIPNets(reqRule.Srcs)
		if err != nil {
			return nil, err
		}
		dst6s, err := ipnet6.FromIPNets(reqRule.Dsts)
		if err != nil {
			return nil, err
		}
		protoRanges, err := protorange.FromProtoRanges(reqRule.ProtoRanges)
		if err != nil {
			return nil, err
		}
		srcPortRanges, err := portrange.FromPortRanges(reqRule.SrcPortRanges)
		if err != nil {
			return nil, err
		}
		dstPortRanges, err := portrange.FromPortRanges(reqRule.DstPortRanges)
		if err != nil {
			return nil, err
		}

		rule := AclRule{
			Counter:       reqRule.Action.Counter,
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

		switch reqRule.Action.Kind {
		case aclpb.ActionKind_ACTION_KIND_PASS:
			rule.Action = 0 // ACL_ACTION_ALLOW
		case aclpb.ActionKind_ACTION_KIND_DENY:
			rule.Action = 1 // ACL_ACTION_DENY
		case aclpb.ActionKind_ACTION_KIND_COUNT:
			rule.Action = 2 // ACL_ACTION_COUNT
		case aclpb.ActionKind_ACTION_KIND_CHECK_STATE:
			rule.Action = 3 // ACL_ACTION_CHECK_STATE
		case aclpb.ActionKind_ACTION_KIND_CREATE_STATE:
			rule.Action = 4 // ACL_ACTION_CREATE_STATE
		default:
			rule.Action = 1 // ACL_ACTION_DENY
		}

		rules = append(rules, rule)
	}
	return rules, nil
}

func (m *ACLService) UpdateConfig(
	ctx context.Context,
	req *aclpb.UpdateConfigRequest,
) (*aclpb.UpdateConfigResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	reqRules := req.Rules

	rules, err := convertRules(reqRules)
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
		m.log.Infow("transfer fwstate config for ACL module", zap.String("config", name))
		config.TransferFwStateConfig(oldConfigs.acl)
	}

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{config.AsFFIModule()}); err != nil {
		config.Free()
		return nil, status.Errorf(codes.Internal, "failed to update module: %v", err)
	}

	// Module was updated - it is time to delete an old one
	if oldConfigs.acl != nil {
		oldConfigs.acl.Free()
	}

	m.configs[name] = aclConfig{
		rules:       reqRules,
		acl:         config,
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
		if err := m.agent.DeleteModuleConfig(name); err != nil {
			return nil, status.Errorf(codes.Internal, "could not delete acl module config '%s': %v", name, err)
		}
		m.log.Infow("successfully deleted ACL module config", zap.String("name", name))
		config.acl.Free()
	}

	delete(m.configs, name)

	response := &aclpb.DeleteConfigResponse{}

	return response, nil
}
