package forward

import (
	"context"
	"fmt"
	"net/netip"
	"sync"

	"go.uber.org/zap"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb"
)

type ffiConfigUpdater func(m *ForwardService, name string, instance uint32) error

type ForwardService struct {
	forwardpb.UnimplementedForwardServiceServer

	mu          sync.Mutex
	agents      []*ffi.Agent
	log         *zap.SugaredLogger
	deviceCount uint16
	updater     ffiConfigUpdater
}

func NewForwardService(agents []*ffi.Agent, log *zap.SugaredLogger) *ForwardService {
	return &ForwardService{
		agents: agents,
		log:    log,
	}
}

func (m *ForwardService) ListConfigs(
	ctx context.Context, request *forwardpb.ListConfigsRequest,
) (*forwardpb.ListConfigsResponse, error) {

	response := &forwardpb.ListConfigsResponse{
		InstanceConfigs: make([]*forwardpb.InstanceConfigs, len(m.agents)),
	}
	for inst := range m.agents {
		response.InstanceConfigs[inst] = &forwardpb.InstanceConfigs{
			Instance: uint32(inst),
		}
	}

	// Lock instances store and module updates
	m.mu.Lock()
	defer m.mu.Unlock()

	//TODO

	return response, nil
}

func (m *ForwardService) ShowConfig(ctx context.Context, req *forwardpb.ShowConfigRequest) (*forwardpb.ShowConfigResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, err
	}

	response := &forwardpb.ShowConfigResponse{Instance: inst}

	m.mu.Lock()
	defer m.mu.Unlock()

	//TODO
	_ = name
	return response, nil
}

func (m *ForwardService) UpdateConfig(ctx context.Context, req *forwardpb.UpdateConfigRequest) (*forwardpb.UpdateConfigResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, err
	}

	reqRules := req.Rules

	rules := make([]forwardRule, 0, len(reqRules))
	for _, reqRule := range reqRules {
		rule := forwardRule{
			target:     reqRule.Target,
			output:     reqRule.Output,
			counter:    reqRule.Counter,
			devices:    reqRule.Devices,
			vlanRanges: make([]vlanRange, 0, len(reqRule.VlanRanges)),
			srcs:       make([]netip.Prefix, 0, len(reqRule.Srcs)),
			dsts:       make([]netip.Prefix, 0, len(reqRule.Dsts)),
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

	m.log.Infow("successfully update forward config",
		zap.String("name", name),
		zap.Uint32("instance", inst),
	)

	return &forwardpb.UpdateConfigResponse{}, nil
}

func (m *ForwardService) DeleteConfig(ctx context.Context, req *forwardpb.DeleteConfigRequest) (*forwardpb.DeleteConfigResponse, error) {
	name, inst, err := req.GetTarget().Validate(uint32(len(m.agents)))
	if err != nil {
		return nil, err
	}
	// Remove module configuration from the control plane.
	// TODO

	deleted := DeleteConfig(m, name, inst)
	m.log.Infow("deleted module config",
		zap.String("name", name),
		zap.Uint32("instance", inst),
		zap.Bool("dataplane_hit", deleted),
	)

	response := &forwardpb.DeleteConfigResponse{
		Deleted: deleted,
	}
	return response, nil
}
