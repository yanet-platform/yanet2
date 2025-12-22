package acl

import (
	"context"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/go/filter"
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
	rules   []*aclpb.Rule
	acl     *ModuleConfig
	fwstate *FwStateConfig
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

func convertRules(reqRules []*aclpb.Rule) ([]aclRule, error) {
	rules := make([]aclRule, 0, len(reqRules))
	for _, reqRule := range reqRules {
		devices, err := filter.MakeDevices(reqRule.Devices)
		if err != nil {
			return nil, err
		}
		vlanRanges, err := filter.MakeVlanRanges(reqRule.VlanRanges)
		if err != nil {
			return nil, err
		}
		src4s, err := filter.MakeIPNet4s(reqRule.Srcs)
		if err != nil {
			return nil, err
		}
		dst4s, err := filter.MakeIPNet4s(reqRule.Dsts)
		if err != nil {
			return nil, err
		}
		src6s, err := filter.MakeIPNet6s(reqRule.Srcs)
		if err != nil {
			return nil, err
		}
		dst6s, err := filter.MakeIPNet6s(reqRule.Dsts)
		if err != nil {
			return nil, err
		}
		protoRanges, err := filter.MakeProtoRanges(reqRule.ProtoRanges)
		if err != nil {
			return nil, err
		}
		srcPortRanges, err := filter.MakePortRanges(reqRule.SrcPortRanges)
		if err != nil {
			return nil, err
		}
		dstPortRanges, err := filter.MakePortRanges(reqRule.DstPortRanges)
		if err != nil {
			return nil, err
		}

		rule := aclRule{
			counter:       reqRule.Action.Counter,
			devices:       devices,
			vlanRanges:    vlanRanges,
			src4s:         src4s,
			dst4s:         dst4s,
			src6s:         src6s,
			dst6s:         dst6s,
			protoRanges:   protoRanges,
			srcPortRanges: srcPortRanges,
			dstPortRanges: dstPortRanges,
		}

		if reqRule.Action.Kind == aclpb.ActionKind_ACTION_KIND_PASS {
			rule.action = 0
		} else {
			rule.action = 1
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
	if ok && oldConfigs.fwstate != nil {
		// We set fwstate config only if it's already present,
		// effectively enabling firewall state tracking functionality
		m.log.Infow("set fwstate config for ACL module config",
			zap.String("config", name),
		)
		config.SetFwStateConfig(m.agent, oldConfigs.fwstate)
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
		rules:   reqRules,
		acl:     config,
		fwstate: oldConfigs.fwstate,
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
		Name:  name,
		Rules: config.rules,
	}

	// Get fwstate configuration if available
	if config.fwstate != nil {
		response.FwstateMap = config.fwstate.GetMapConfig()
		response.FwstateSync = config.fwstate.GetSyncConfig()
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

func (m *ACLService) UpdateFWStateConfig(
	ctx context.Context, req *aclpb.UpdateFWStateConfigRequest,
) (*aclpb.UpdateFWStateConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	// Get fwstate configuration from req
	if req.SyncConfig == nil {
		return nil, status.Error(codes.InvalidArgument, "sync_config is required")
	}
	if req.MapConfig == nil {
		return nil, status.Error(codes.InvalidArgument, "map_config is required")
	}

	m.log.Debugw("update fwstate config",
		zap.String("config", name),
	)

	m.mu.Lock()
	defer m.mu.Unlock()

	config := m.configs[name]

	newFwstateConfig, err := NewFWStateModuleConfig(m.agent, name, config.fwstate)
	if err != nil {
		m.log.Errorw("failed to create fwstate config",
			zap.String("config", name),
			zap.Error(err),
		)
		return nil, status.Errorf(codes.Internal, "failed to create fwstate config: %v", err)
	}

	dpConfig := m.agent.DPConfig()

	if err = newFwstateConfig.CreateMaps(req.MapConfig, uint16(dpConfig.WorkerCount()), m.log); err != nil {
		newFwstateConfig.DetachMaps() // in order not to pull them out from under the feet of another module
		newFwstateConfig.Free()
		m.log.Errorw("failed to create fwstate maps",
			zap.String("config", name),
			zap.Error(err),
		)
		return nil, status.Errorf(codes.Internal, "failed to create fwstate maps: %v", err)
	}

	// FIXME: validate new config
	// Set sync config
	newFwstateConfig.SetSyncConfig(req.SyncConfig)
	m.log.Debugw("update fwstate module config",
		zap.String("config", name),
	)

	if config.acl != nil {
		err := func() error {
			newACLConfig, err := NewModuleConfig(m.agent, name)
			if err != nil {
				return status.Errorf(codes.Internal, "failed to create module config: %v", err)
			}

			rules, err := convertRules(config.rules)
			if err != nil {
				newACLConfig.Free()
				return status.Errorf(codes.Internal, "failed to convertRules: %v", err)
			}

			if err := newACLConfig.Update(rules); err != nil {
				newACLConfig.Free()
				return status.Errorf(codes.Internal, "failed to update module config: %v", err)
			}

			newACLConfig.SetFwStateConfig(m.agent, newFwstateConfig)

			if err := m.agent.UpdateModules([]ffi.ModuleConfig{newACLConfig.AsFFIModule(), newFwstateConfig.asFFIModule()}); err != nil {
				newACLConfig.Free()
				return status.Errorf(codes.Internal, "failed to update module: %v", err)
			}
			config.acl.Free()
			config.acl = newACLConfig
			return nil
		}()
		if err != nil {
			newFwstateConfig.DetachMaps() // in order not to pull them out from under the feet of another module
			newFwstateConfig.Free()
			return nil, err
		}
	} else {
		if err := m.agent.UpdateModules([]ffi.ModuleConfig{newFwstateConfig.asFFIModule()}); err != nil {
			m.log.Errorw("failed to update fwstate module",
				zap.String("config", name),
				zap.Error(err),
			)
			return nil, status.Errorf(codes.Internal, "failed to update fwstate module: %v", err)
		}
	}

	if config.fwstate != nil {
		config.fwstate.DetachMaps()
		config.fwstate.Free()
	}

	config.fwstate = newFwstateConfig
	m.configs[name] = config

	m.log.Infow("successfully updated FWState module",
		zap.String("config", name),
	)

	return &aclpb.UpdateFWStateConfigResponse{}, nil
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
	if config.fwstate != nil {
		if err := m.agent.DeleteModuleConfigType(fwstateModuleTypeName, name); err != nil {
			return nil, status.Errorf(codes.Internal, "could not delete fwstate module config '%s': %v", name, err)
		}
		m.log.Infow("successfully deleted FWState module config", zap.String("name", name))
		config.fwstate.Free()
	}

	delete(m.configs, name)

	response := &aclpb.DeleteConfigResponse{}

	return response, nil
}
