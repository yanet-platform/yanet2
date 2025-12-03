package acl

import "C"
import (
	"context"
	"fmt"
	"net/netip"
	"sync"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

////////////////////////////////////////////////////////////////////////////////

type aclConfig struct {
	rules  []*aclpb.Rule
	module *ModuleConfig
}

// ACLService implements the gRPC service for ACL management.
type ACLService struct {
	aclpb.UnimplementedAclServiceServer

	mu      sync.Mutex
	agents  []*ffi.Agent
	configs map[instanceKey]aclConfig
}

func NewACLService(agents []*ffi.Agent) *ACLService {
	return &ACLService{
		agents:  agents,
		configs: make(map[instanceKey]aclConfig),
	}
}

////////////////////////////////////////////////////////////////////////////////

type instanceKey struct {
	name     string
	instance uint32
}

////////////////////////////////////////////////////////////////////////////////

func (m *ACLService) UpdateConfig(
	ctx context.Context,
	req *aclpb.UpdateConfigRequest,
) (*aclpb.UpdateConfigResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name, inst, err := req.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	reqRules := req.Rules

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
			rule.vlanRanges = append(rule.vlanRanges, vlanRange{
				from: uint16(reqVlanRange.From),
				to:   uint16(reqVlanRange.To),
			})
		}

		for _, reqSrc := range reqRule.Srcs {
			if (len(reqSrc.Addr) != 4 && len(reqSrc.Addr) != 16) || len(reqSrc.Addr) != len(reqSrc.Mask) {
				return nil, fmt.Errorf("invalid network address length")
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
				return nil, fmt.Errorf("invalid network address length")
			}

			addr, _ := netip.AddrFromSlice(reqDst.Addr)
			mask, _ := netip.AddrFromSlice(reqDst.Mask)
			rule.dsts = append(rule.dsts, network{
				addr: addr,
				mask: mask,
			})
		}

		for _, reqProtoRange := range reqRule.ProtoRanges {
			rule.protoRanges = append(rule.protoRanges, protoRange{
				from: uint16(reqProtoRange.From),
				to:   uint16(reqProtoRange.To),
			})
		}

		for _, reqSrcPortRange := range reqRule.SrcPortRanges {
			rule.srcPortRanges = append(rule.srcPortRanges, portRange{
				from: uint16(reqSrcPortRange.From),
				to:   uint16(reqSrcPortRange.To),
			})
		}

		for _, reqDstPortRange := range reqRule.DstPortRanges {
			rule.dstPortRanges = append(rule.dstPortRanges, portRange{
				from: uint16(reqDstPortRange.From),
				to:   uint16(reqDstPortRange.To),
			})
		}

		rules = append(rules, rule)
	}

	if inst >= uint32(len(m.agents)) {
		return nil, fmt.Errorf("invalid instance id")
	}
	agent := m.agents[inst]

	module, err := NewModuleConfig(agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)

	}

	if err := module.Update(rules); err != nil {
		FreeModuleConfig(module)
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	if err := agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}); err != nil {
		FreeModuleConfig(module)
		return nil, fmt.Errorf("failed to update module on instance %d: %w", inst, err)
	}

	// Module was updated - it is time to delete an old one
	key := instanceKey{
		instance: inst,
		name:     name,
	}
	if oldModule, ok := m.configs[key]; ok {
		FreeModuleConfig(oldModule.module)
	}

	m.configs[key] = aclConfig{
		rules:  reqRules,
		module: module,
	}

	return &aclpb.UpdateConfigResponse{}, nil
}

func (m *ACLService) ShowConfig(
	ctx context.Context,
	req *aclpb.ShowConfigRequest,
) (*aclpb.ShowConfigResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name, inst, err := req.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	key := instanceKey{
		instance: inst,
		name:     name,
	}

	config, ok := m.configs[key]

	if !ok {
		return nil, status.Error(codes.InvalidArgument, "not found")
	}

	response := &aclpb.ShowConfigResponse{
		Target: &commonpb.TargetModule{
			DataplaneInstance: inst,
			ConfigName:        name,
		},
		Rules: config.rules,
	}

	return response, nil
}

func (m *ACLService) ListConfigs(
	ctx context.Context,
	req *aclpb.ListConfigsRequest,
) (*aclpb.ListConfigsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	response := &aclpb.ListConfigsResponse{}

	for key := range m.configs {
		response.Targets = append(response.Targets, &commonpb.TargetModule{
			DataplaneInstance: key.instance,
			ConfigName:        key.name,
		})
	}

	return response, nil
}

func (m *ACLService) DeleteConfig(
	ctx context.Context,
	req *aclpb.DeleteConfigRequest,
) (*aclpb.DeleteConfigResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name, inst, err := req.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	key := instanceKey{
		instance: inst,
		name:     name,
	}

	_, ok := m.configs[key]

	if !ok {
		return nil, status.Error(codes.InvalidArgument, "not found")
	}

	if DeleteModule(m, name, inst) {
		return nil, fmt.Errorf("could not deletee module on instance %d", inst)
	}

	delete(m.configs, key)

	response := &aclpb.DeleteConfigResponse{}

	return response, nil
}
