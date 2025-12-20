package acl

import (
	"context"
	"net/netip"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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
		rule := aclRule{
			counter:       reqRule.Counter,
			devices:       reqRule.Devices,
			vlanRanges:    make([]vlanRange, 0, len(reqRule.VlanRanges)),
			srcs:          make([]network, 0, len(reqRule.Srcs)),
			dsts:          make([]network, 0, len(reqRule.Dsts)),
			protoRanges:   make([]protoRange, 0, len(reqRule.ProtoRanges)),
			srcPortRanges: make([]portRange, 0, len(reqRule.SrcPortRanges)),
			dstPortRanges: make([]portRange, 0, len(reqRule.DstPortRanges)),
		}

		if reqRule.Action == aclpb.ActionKind_ACTION_KIND_PASS {
			rule.action = 0
		} else {
			rule.action = 1
		}

		for _, reqVlanRange := range reqRule.VlanRanges {
			// VLAN ID is 12 bits, so valid range is 0-4095
			if reqVlanRange.From > 4095 {
				return nil, status.Errorf(codes.InvalidArgument, "VLAN 'from' value %d exceeds maximum 4095", reqVlanRange.From)
			}
			if reqVlanRange.To > 4095 {
				return nil, status.Errorf(codes.InvalidArgument, "VLAN 'to' value %d exceeds maximum 4095", reqVlanRange.To)
			}
			if reqVlanRange.From > reqVlanRange.To {
				return nil, status.Errorf(codes.InvalidArgument, "VLAN 'from' value %d is greater than 'to' value %d", reqVlanRange.From, reqVlanRange.To)
			}
			rule.vlanRanges = append(rule.vlanRanges, vlanRange{
				from: uint16(reqVlanRange.From),
				to:   uint16(reqVlanRange.To),
			})
		}

		for _, reqSrc := range reqRule.Srcs {
			if (len(reqSrc.Addr) != 4 && len(reqSrc.Addr) != 16) || len(reqSrc.Addr) != len(reqSrc.Mask) {
				return nil, status.Error(codes.InvalidArgument, "invalid network address length")
			}

			addr, _ := netip.AddrFromSlice(reqSrc.Addr)
			mask, _ := netip.AddrFromSlice(reqSrc.Mask)
			rule.srcs = append(rule.srcs, network{
				addr: addr,
				mask: mask,
			})
		}

		for _, reqDst := range reqRule.Dsts {
			if (len(reqDst.Addr) != 4 && len(reqDst.Addr) != 16) || len(reqDst.Addr) != len(reqDst.Mask) {
				return nil, status.Error(codes.InvalidArgument, "invalid network address length")
			}

			addr, _ := netip.AddrFromSlice(reqDst.Addr)
			mask, _ := netip.AddrFromSlice(reqDst.Mask)
			rule.dsts = append(rule.dsts, network{
				addr: addr,
				mask: mask,
			})
		}

		for _, reqProtoRange := range reqRule.ProtoRanges {
			// Protocol is 8 bits, so valid range is 0-255
			if reqProtoRange.From > 255 {
				return nil, status.Errorf(codes.InvalidArgument, "Protocol 'from' value %d exceeds maximum 255", reqProtoRange.From)
			}
			if reqProtoRange.To > 255 {
				return nil, status.Errorf(codes.InvalidArgument, "Protocol 'to' value %d exceeds maximum 255", reqProtoRange.To)
			}
			if reqProtoRange.From > reqProtoRange.To {
				return nil, status.Errorf(codes.InvalidArgument, "Protocol 'from' value %d is greater than 'to' value %d", reqProtoRange.From, reqProtoRange.To)
			}
			rule.protoRanges = append(rule.protoRanges, protoRange{
				from: uint8(reqProtoRange.From),
				to:   uint8(reqProtoRange.To),
			})
		}

		for _, reqSrcPortRange := range reqRule.SrcPortRanges {
			// Port is 16 bits, so valid range is 0-65535
			if reqSrcPortRange.From > 65535 {
				return nil, status.Errorf(codes.InvalidArgument, "Source port 'from' value %d exceeds maximum 65535", reqSrcPortRange.From)
			}
			if reqSrcPortRange.To > 65535 {
				return nil, status.Errorf(codes.InvalidArgument, "Source port 'to' value %d exceeds maximum 65535", reqSrcPortRange.To)
			}
			if reqSrcPortRange.From > reqSrcPortRange.To {
				return nil, status.Errorf(codes.InvalidArgument, "Source port 'from' value %d is greater than 'to' value %d", reqSrcPortRange.From, reqSrcPortRange.To)
			}
			rule.srcPortRanges = append(rule.srcPortRanges, portRange{
				from: uint16(reqSrcPortRange.From),
				to:   uint16(reqSrcPortRange.To),
			})
		}

		for _, reqDstPortRange := range reqRule.DstPortRanges {
			// Port is 16 bits, so valid range is 0-65535
			if reqDstPortRange.From > 65535 {
				return nil, status.Errorf(codes.InvalidArgument, "Destination port 'from' value %d exceeds maximum 65535", reqDstPortRange.From)
			}
			if reqDstPortRange.To > 65535 {
				return nil, status.Errorf(codes.InvalidArgument, "Destination port 'to' value %d exceeds maximum 65535", reqDstPortRange.To)
			}
			if reqDstPortRange.From > reqDstPortRange.To {
				return nil, status.Errorf(codes.InvalidArgument, "Destination port 'from' value %d is greater than 'to' value %d", reqDstPortRange.From, reqDstPortRange.To)
			}
			rule.dstPortRanges = append(rule.dstPortRanges, portRange{
				from: uint16(reqDstPortRange.From),
				to:   uint16(reqDstPortRange.To),
			})
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

	name, err := req.GetTarget().Validate()
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
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

	name, err := req.GetTarget().Validate()
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	config, ok := m.configs[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	response := &aclpb.ShowConfigResponse{
		Target: req.Target,
		Rules:  config.rules,
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
	ctx context.Context, request *aclpb.UpdateFWStateConfigRequest,
) (*aclpb.UpdateFWStateConfigResponse, error) {
	name, err := request.GetTarget().Validate()
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Get fwstate configuration from request
	if request.SyncConfig == nil {
		return nil, status.Error(codes.InvalidArgument, "sync_config is required")
	}
	if request.MapConfig == nil {
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

	if err = newFwstateConfig.CreateMaps(request.MapConfig, uint16(dpConfig.WorkerCount()), m.log); err != nil {
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
	newFwstateConfig.SetSyncConfig(request.SyncConfig)
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

	name, err := req.GetTarget().Validate()
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
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
