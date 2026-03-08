package route_mpls

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/filterpb"
	"github.com/yanet-platform/yanet2/common/go/filter/ipnet4"
	"github.com/yanet-platform/yanet2/common/go/filter/ipnet6"
	"github.com/yanet-platform/yanet2/common/go/maptrie"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
	route_mplspb "github.com/yanet-platform/yanet2/modules/route-mpls/controlplane/route-mplspb"
)

type RouteMPLSService struct {
	route_mplspb.UnimplementedRouteMPLSServiceServer

	mu      sync.Mutex
	agent   *ffi.Agent
	configs map[string]routeMPLSConfig

	log *zap.SugaredLogger
}

type NextHop struct {
	Source      netip.Addr
	Destination netip.Addr
	MPLSLabel   uint32

	LocalPref uint32
	ASPath    []uint32
	Med       uint32

	Weight uint64

	Counter string
}

type NextHopList struct {
	NextHops []NextHop
}

func nextHopCompareCB(l NextHop, r NextHop) int {
	if prefDiff := int(l.LocalPref) - int(r.LocalPref); prefDiff != 0 {
		return prefDiff
	}

	if pathDiff := len(r.ASPath) - len(l.ASPath); pathDiff != 0 {
		return pathDiff
	}

	return int(l.Med) - int(r.Med)
}

func (m *NextHopList) lookup(destination netip.Addr, mplsLabel uint32) int {
	for idx, known := range m.NextHops {
		if known.MPLSLabel == mplsLabel &&
			known.Destination == destination {
			return idx
		}
	}

	return -1
}

func (m *NextHopList) Insert(nextHop NextHop) {
	if idx := m.lookup(nextHop.Destination, nextHop.MPLSLabel); idx != -1 {
		m.NextHops[idx] = nextHop
	} else {
		m.NextHops = append(m.NextHops, nextHop)
	}

	slices.SortFunc(m.NextHops, nextHopCompareCB)
}

func (m *NextHopList) Remove(nextHop NextHop) {
	if idx := m.lookup(nextHop.Destination, nextHop.MPLSLabel); idx != -1 {
		m.NextHops = slices.Delete(m.NextHops, idx, idx+1)
	}
}

type routeMPLSConfig struct {
	prefixes  maptrie.MapTrie[netip.Prefix, netip.Addr, NextHopList]
	routeMPLS *ModuleConfig
}

func NewRouteMPLSService(
	agent *ffi.Agent,
	log *zap.SugaredLogger,
) *RouteMPLSService {
	return &RouteMPLSService{
		agent:   agent,
		configs: make(map[string]routeMPLSConfig),
		log:     log,
	}
}

func (m *RouteMPLSService) ListConfigs(
	ctx context.Context,
	request *route_mplspb.ListConfigsRequest,
) (*route_mplspb.ListConfigsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	response := &route_mplspb.ListConfigsResponse{
		Configs: make([]string, 0, len(m.configs)),
	}

	for name := range m.configs {
		response.Configs = append(response.Configs, name)
	}

	return response, nil
}

func (m *RouteMPLSService) ShowConfig(
	ctx context.Context,
	req *route_mplspb.ShowConfigRequest,
) (*route_mplspb.ShowConfigResponse, error) {
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

	rules := make([]*route_mplspb.Rule, 0)

	for _, prefixes := range config.prefixes {
		for prefix, nexthops := range prefixes {
			for _, nexthop := range nexthops.NextHops {
				rules = append(
					rules,
					&route_mplspb.Rule{
						Prefix: &filterpb.IPPrefix{
							Addr:   prefix.Addr().AsSlice(),
							Length: uint32(prefix.Bits()),
						},
						Nexthop: &route_mplspb.NextHop{
							Label:         nexthop.MPLSLabel,
							SourceIp:      nexthop.Source.AsSlice(),
							DestinationIp: nexthop.Destination.AsSlice(),
							LocalPref:     nexthop.LocalPref,
							AsPath:        nexthop.ASPath,
							Med:           nexthop.Med,
							Weight:        nexthop.Weight,
							Counter:       nexthop.Counter,
						},
					},
				)
			}
		}
	}

	response := &route_mplspb.ShowConfigResponse{
		Name:  name,
		Rules: rules,
	}

	return response, nil
}

func (m *RouteMPLSService) DeleteConfig(
	ctx context.Context,
	req *route_mplspb.DeleteConfigRequest,
) (*route_mplspb.DeleteConfigResponse, error) {
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

	if config.routeMPLS != nil {
		if err := m.agent.DeleteModuleConfig(name); err != nil {
			return nil, status.Errorf(codes.Internal, "could not delete acl module config '%s': %v", name, err)
		}
		m.log.Infow("successfully deleted ACL module config", zap.String("name", name))
		config.routeMPLS.Free()
	}

	delete(m.configs, name)

	response := &route_mplspb.DeleteConfigResponse{}

	return response, nil
}

func (m *NextHop) toFFI() routeMPLSNextHop {
	return routeMPLSNextHop{
		Kind:        routeMPLSKindTun,
		Source:      m.Source,
		Destination: m.Destination,
		MPLSLabel:   m.MPLSLabel,
		Weight:      m.Weight,
		Counter:     m.Counter,
	}
}

func (m *routeMPLSConfig) submit() error {
	ffiRules := make([]routeMPLSRule, 0)

	for prefixLen := 128; prefixLen >= 0; prefixLen-- {
		for prefix, nextHopList := range m.prefixes[prefixLen] {
			ffiNextHops := make([]routeMPLSNextHop, 0, len(nextHopList.NextHops))
			for _, nexthop := range nextHopList.NextHops {
				ffiNextHops = append(
					ffiNextHops,
					nexthop.toFFI(),
				)
			}

			dst4s, _ := ipnet4.FromNetIpPrefixes([]netip.Prefix{prefix})
			dst6s, _ := ipnet6.FromNetIpPrefixes([]netip.Prefix{prefix})

			ffiRule := routeMPLSRule{
				Dst4s:    dst4s,
				Dst6s:    dst6s,
				NextHops: ffiNextHops,
			}
			ffiRules = append(ffiRules, ffiRule)
		}
	}

	default4Prefix := netip.PrefixFrom(netip.AddrFrom4([4]byte{}), 0)
	default4Dst, _ := ipnet4.FromNetIpPrefixes([]netip.Prefix{default4Prefix})
	ffiRules = append(ffiRules, routeMPLSRule{
		Dst4s: default4Dst,
		NextHops: []routeMPLSNextHop{
			routeMPLSNextHop{
				Kind:    routeMPLSKindNone,
				Weight:  1,
				Counter: "no route mpls v4",
			},
		},
	})

	default16Prefix := netip.PrefixFrom(netip.AddrFrom16([16]byte{}), 0)
	default16Dst, _ := ipnet6.FromNetIpPrefixes([]netip.Prefix{default16Prefix})

	ffiRules = append(ffiRules, routeMPLSRule{
		Dst6s: default16Dst,
		NextHops: []routeMPLSNextHop{
			routeMPLSNextHop{
				Kind:    routeMPLSKindNone,
				Weight:  1,
				Counter: "no route mpls v6",
			},
		},
	})

	return m.routeMPLS.Update(ffiRules)
}

func makePrefix(prefix *filterpb.IPPrefix) (netip.Prefix, error) {
	addr, err := netip.AddrFromSlice(prefix.Addr)
	if !err {
		return netip.Prefix{}, fmt.Errorf("invalid address length")
	}

	return netip.PrefixFrom(addr, int(prefix.Length)), nil
}

func makeNextHop(nexthop *route_mplspb.NextHop) (NextHop, error) {
	src, ok := netip.AddrFromSlice(nexthop.SourceIp)
	if !ok {
		return NextHop{}, fmt.Errorf("invalid source address")
	}
	dst, ok := netip.AddrFromSlice(nexthop.DestinationIp)
	if !ok {
		return NextHop{}, fmt.Errorf("invalid destination address")
	}

	return NextHop{
		Source:      src,
		Destination: dst,
		MPLSLabel:   nexthop.Label,

		LocalPref: nexthop.LocalPref,
		ASPath:    nexthop.AsPath,
		Med:       nexthop.Med,

		Weight: nexthop.Weight,

		Counter: nexthop.Counter,
	}, nil
}

func (m *RouteMPLSService) CreateConfig(
	ctx context.Context,
	req *route_mplspb.CreateConfigRequest,
) (*route_mplspb.CreateConfigResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := req.Name
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	_, ok := m.configs[name]
	if ok {
		return nil, status.Error(codes.InvalidArgument, "already exists")
	}

	prefixes := maptrie.NewMapTrie[netip.Prefix, netip.Addr, NextHopList](0)

	for _, rule := range req.Rules {
		prefix, err := makePrefix(rule.Prefix)
		if err != nil {
			return nil, err
		}

		nextHop, err := makeNextHop(rule.Nexthop)
		if err != nil {
			return nil, err
		}

		prefixes.InsertOrUpdate(
			prefix,
			func() NextHopList {
				return NextHopList{
					NextHops: append(make([]NextHop, 0, 1), nextHop),
				}
			},
			func(m NextHopList) NextHopList {
				m.Insert(nextHop)

				return m
			},
		)
	}

	module, err := NewModuleConfig(m.agent, name)

	if err != nil {
		return nil, err
	}

	config := routeMPLSConfig{
		prefixes:  prefixes,
		routeMPLS: module,
	}

	if err := config.submit(); err != nil {
		module.Free()
		return nil, err
	}

	m.configs[name] = config

	response := &route_mplspb.CreateConfigResponse{}

	return response, nil
}

func (m *RouteMPLSService) UpdateConfig(
	ctx context.Context,
	req *route_mplspb.UpdateConfigRequest,
) (*route_mplspb.UpdateConfigResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := req.Name
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	oldConfig, ok := m.configs[name]
	if !ok {
		oldConfig = routeMPLSConfig{
			prefixes:  maptrie.NewMapTrie[netip.Prefix, netip.Addr, NextHopList](0),
			routeMPLS: nil,
		}
	}

	config := routeMPLSConfig{
		prefixes:  oldConfig.prefixes.Clone(),
		routeMPLS: nil,
	}

	for _, update := range req.Updates {
		if update := update.GetUpdate(); update != nil {
			prefix, err := makePrefix(update.Prefix)
			if err != nil {
				return nil, err
			}

			nextHop, err := makeNextHop(update.Nexthop)
			if err != nil {
				return nil, err
			}

			config.prefixes.InsertOrUpdate(
				prefix,
				func() NextHopList {
					return NextHopList{
						NextHops: append(make([]NextHop, 0, 1), nextHop),
					}
				},
				func(m NextHopList) NextHopList {
					m.Insert(nextHop)

					return m
				},
			)

		}

		if withdraw := update.GetWithdraw(); withdraw != nil {
			prefix, err := makePrefix(withdraw.Prefix)
			if err != nil {
				return nil, err
			}

			nextHop, err := makeNextHop(withdraw.Nexthop)
			if err != nil {
				return nil, err
			}

			config.prefixes.UpdateOrDelete(
				prefix,
				func(m NextHopList) (NextHopList, bool) {
					m.Remove(nextHop)
					return m, len(m.NextHops) == 0
				},
			)

		}
	}

	module, err := NewModuleConfig(m.agent, name)
	if err != nil {
		return nil, err
	}

	config.routeMPLS = module

	if err := config.submit(); err != nil {
		config.routeMPLS.Free()
		return nil, err
	}

	if err := m.agent.UpdateModules([]ffi.ModuleConfig{config.routeMPLS.AsFFIModule()}); err != nil {
		config.routeMPLS.Free()
		return nil, status.Errorf(codes.Internal, "failed to update module: %v", err)
	}

	if oldConfig.routeMPLS != nil {
		oldConfig.routeMPLS.Free()
	}

	m.configs[name] = config

	response := &route_mplspb.UpdateConfigResponse{}

	return response, nil

}
