package forward

import (
	"context"
	"fmt"
	"net/netip"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb"
)

type ffiConfigUpdater func(m *ForwardService, name string) error

type ForwardService struct {
	forwardpb.UnimplementedForwardServiceServer

	mu          sync.Mutex
	agent       *ffi.Agent
	deviceCount uint16
	updater     ffiConfigUpdater
}

func NewForwardService(agent *ffi.Agent) *ForwardService {
	return &ForwardService{
		agent: agent,
	}
}

func (m *ForwardService) ListConfigs(
	ctx context.Context, request *forwardpb.ListConfigsRequest,
) (*forwardpb.ListConfigsResponse, error) {

	response := &forwardpb.ListConfigsResponse{
		Configs: make([]string, 0),
	}

	// Lock instances store and module updates
	m.mu.Lock()
	defer m.mu.Unlock()

	//TODO

	return response, nil
}

func (m *ForwardService) ShowConfig(ctx context.Context, req *forwardpb.ShowConfigRequest) (*forwardpb.ShowConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	response := &forwardpb.ShowConfigResponse{}

	m.mu.Lock()
	defer m.mu.Unlock()

	//TODO
	_ = name
	return response, nil
}

func (m *ForwardService) UpdateConfig(ctx context.Context, req *forwardpb.UpdateConfigRequest) (*forwardpb.UpdateConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	reqRules := req.Rules

	rules := make([]forwardRule, 0, len(reqRules))
	for _, reqRule := range reqRules {
		rule := forwardRule{
			target:     reqRule.Target,
			mode:       modeNone,
			counter:    reqRule.Counter,
			devices:    reqRule.Devices,
			vlanRanges: make([]vlanRange, 0, len(reqRule.VlanRanges)),
			srcs:       make([]netip.Prefix, 0, len(reqRule.Srcs)),
			dsts:       make([]netip.Prefix, 0, len(reqRule.Dsts)),
		}

		if reqRule.Mode == forwardpb.ForwardMode_IN {
			rule.mode = modeIn
		}
		if reqRule.Mode == forwardpb.ForwardMode_OUT {
			rule.mode = modeOut
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

	module, err := NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, fmt.Errorf("failed to create module config: %w", err)

	}

	if err := module.Update(rules); err != nil {
		return nil, fmt.Errorf("failed to update module config: %w", err)
	}

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{module.AsFFIModule()}); err != nil {
		return nil, fmt.Errorf("failed to update module: %w", err)
	}

	return &forwardpb.UpdateConfigResponse{}, nil
}

func (m *ForwardService) DeleteConfig(ctx context.Context, req *forwardpb.DeleteConfigRequest) (*forwardpb.DeleteConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}
	// Remove module configuration from the control plane.
	// TODO

	deleted := DeleteConfig(m, name)

	response := &forwardpb.DeleteConfigResponse{
		Deleted: deleted,
	}
	return response, nil
}
