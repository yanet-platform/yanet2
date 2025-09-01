package core

import (
	"context"
	"errors"
	"net/netip"
	"sync"

	"github.com/yanet-platform/yanet2/agent/balancer/internal/controlplane"
	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

type Core struct {
	controlPlane *controlplane.Client

	lock  sync.Mutex
	state map[netip.Addr]*Service
}

func New(controlPlane *controlplane.Client) *Core {
	return &Core{
		controlPlane: controlPlane,
		state:        map[netip.Addr]*Service{},
	}
}

// Reload updates services in the control plane: removes outdated, adds new, skips unchanged.
// Thread-safe. Uses in-memory information rather than fetching the actual control plane state.
func (c *Core) Reload(ctx context.Context, config *Config) error {
	c.lock.Lock()
	defer c.lock.Unlock()

	newServices := make(map[netip.Addr]*Service, len(config.Services))
	for i, s := range config.Services {
		newServices[s.VIP] = &config.Services[i]
	}

	for vip, old := range c.state {
		if s, ok := newServices[vip]; ok && s.Equals(*old) {
			delete(newServices, vip)
			continue
		}
		err := c.controlPlane.RemoveService(ctx, &balancerpb.RemoveServiceRequest{
			ServiceAddr: vip.AsSlice(),
		})
		if err != nil {
			return errors.New("could not remove service from controlplane")
		}
		delete(c.state, vip)
	}

	for _, s := range newServices {
		err := c.controlPlane.AddService(ctx, &balancerpb.AddServiceRequest{
			Service: convertService(s),
		})
		if err != nil {
			return errors.New("could not add service to controlplane")
		}
	}
	return nil
}

func convertService(s *Service) *balancerpb.Service {
	service := &balancerpb.Service{
		Addr: s.VIP.AsSlice(),
		Prefixes: []*balancerpb.Prefix{
			// FIXME: load rules from fw
			{
				Addr: make([]byte, 16),
				Size: 0,
			},
		},
		Reals: make([]*balancerpb.Real, 0, len(s.Reals)),
	}
	for _, r := range s.Reals {
		service.Reals = append(service.Reals, &balancerpb.Real{
			Weight:  uint32(r.Weight),
			DstAddr: r.IP.AsSlice(),
			SrcAddr: s.IPv4OuterSourceNetwork.Addr().AsSlice(),
			SrcMask: xnetip.Mask(s.IPv4OuterSourceNetwork),
		})
	}
	switch s.ForwardingMethod {
	case TUN:
		service.ForwardingMethod = balancerpb.ForwardingMethod_FORWARDING_METHOD_TUN
	case GRE:
		service.ForwardingMethod = balancerpb.ForwardingMethod_FORWARDING_METHOD_GRE
	}
	return service
}
