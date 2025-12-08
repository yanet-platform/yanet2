package acl

import "C"
import (
	"context"
	"fmt"
	"net/netip"
	"sync"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/acl/controlplane/aclpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

////////////////////////////////////////////////////////////////////////////////

// ACLService implements the gRPC service for ACL management.
type ACLService struct {
	aclpb.UnimplementedAclServiceServer

	mu      sync.Mutex
	agents  []*ffi.Agent
	configs map[instanceKey]*ModuleConfig
}

func NewACLService(agents []*ffi.Agent) *ACLService {
	return &ACLService{
		agents:  agents,
		configs: make(map[instanceKey]*ModuleConfig),
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
			srcs:          make([]netip.Prefix, 0, len(reqRule.Srcs)),
			dsts:          make([]netip.Prefix, 0, len(reqRule.Dsts)),
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
			if len(reqSrc.Ip) != 4 && len(reqSrc.Ip) != 16 {
				return nil, fmt.Errorf("invalid network address length")
			}

			addr, _ := netip.AddrFromSlice(reqSrc.Ip)
			rule.srcs = append(rule.srcs, netip.PrefixFrom(addr, int(reqSrc.PrefixLen)))
		}

		for _, reqDst := range reqRule.Dsts {
			if len(reqDst.Ip) != 4 && len(reqDst.Ip) != 16 {
				return nil, fmt.Errorf("invalid network address length")
			}

			addr, _ := netip.AddrFromSlice(reqDst.Ip)
			rule.dsts = append(rule.dsts, netip.PrefixFrom(addr, int(reqDst.PrefixLen)))
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
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	if err := agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}); err != nil {
		return nil, fmt.Errorf("failed to update module on instance %d: %w", inst, err)
	}

	return &aclpb.UpdateConfigResponse{}, nil
}
