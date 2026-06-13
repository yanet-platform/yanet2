package route_mpls

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	filterpb "github.com/yanet-platform/yanet2/common/filterpb/v1"
	"github.com/yanet-platform/yanet2/common/go/maptrie"
	"github.com/yanet-platform/yanet2/modules/route-mpls/bindings/go/croutempls"
	"github.com/yanet-platform/yanet2/modules/route-mpls/controlplane/routemplspb/v1"
)

// RouteMPLSServiceOption configures the RouteMPLSService constructor.
type RouteMPLSServiceOption func(*routeMPLSServiceOptions)

type routeMPLSServiceOptions struct {
	Log *zap.Logger
}

func newRouteMPLSServiceOptions() *routeMPLSServiceOptions {
	return &routeMPLSServiceOptions{
		Log: zap.NewNop(),
	}
}

// WithRouteMPLSServiceLog sets the logger for the RouteMPLSService.
func WithRouteMPLSServiceLog(log *zap.Logger) RouteMPLSServiceOption {
	return func(o *routeMPLSServiceOptions) {
		o.Log = log
	}
}

// RouteMPLSService is the gRPC service implementation for the route-mpls
// module.
type RouteMPLSService struct {
	routemplspb.UnimplementedRouteMPLSServiceServer

	backend Backend

	// shmLock serializes shared-memory mutations and protects the
	// configs map.
	shmLock sync.RWMutex
	configs map[string]routeMPLSConfig

	log *zap.Logger
}

// NextHop holds a single MPLS forwarding nexthop in the service's domain
// model.
type NextHop struct {
	// Source is the tunnel source IP address.
	Source netip.Addr
	// Destination is the tunnel destination IP address.
	Destination netip.Addr
	// MPLSLabel is the outgoing MPLS label.
	MPLSLabel uint32
	// Weight is the ECMP load-balancing weight.
	Weight uint64
	// Counter is the counter name for traffic accounting.
	Counter string
}

// NextHopList is a mutable list of nexthops associated with a single prefix.
type NextHopList struct {
	NextHops []NextHop
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

// Insert adds or replaces a nexthop in the list.
func (m *NextHopList) Insert(nextHop NextHop) {
	if idx := m.lookup(nextHop.Destination, nextHop.MPLSLabel); idx != -1 {
		m.NextHops[idx] = nextHop
	} else {
		m.NextHops = append(m.NextHops, nextHop)
	}
}

// Remove removes a nexthop from the list by destination and MPLS label.
func (m *NextHopList) Remove(nextHop NextHop) {
	if idx := m.lookup(nextHop.Destination, nextHop.MPLSLabel); idx != -1 {
		m.NextHops = slices.Delete(m.NextHops, idx, idx+1)
	}
}

type routeMPLSConfig struct {
	prefixes maptrie.MapTrie[netip.Prefix, netip.Addr, NextHopList]
	handle   ModuleHandle
}

// buildRules iterates the maptrie from longest to shortest prefix and
// assembles the croutempls.Rule slice, appending default drop rules for both
// IPv4 and IPv6 at the end.
func (m *routeMPLSConfig) buildRules() []croutempls.Rule {
	rules := make([]croutempls.Rule, 0)

	for prefixLen := 128; prefixLen >= 0; prefixLen-- {
		for prefix, nextHopList := range m.prefixes[prefixLen] {
			nexthops := make([]croutempls.Nexthop, 0, len(nextHopList.NextHops))
			for _, nh := range nextHopList.NextHops {
				nexthops = append(nexthops, nh.toFFI())
			}

			dst4s, _ := filter.Net4sFromPrefixes([]netip.Prefix{prefix})
			dst6s, _ := filter.Net6sFromPrefixes([]netip.Prefix{prefix})

			rules = append(rules, croutempls.Rule{
				Dst4s:    dst4s,
				Dst6s:    dst6s,
				Nexthops: nexthops,
			})
		}
	}

	default4Prefix := netip.PrefixFrom(netip.AddrFrom4([4]byte{}), 0)
	default4Dst, _ := filter.Net4sFromPrefixes([]netip.Prefix{default4Prefix})
	rules = append(rules, croutempls.Rule{
		Dst4s: default4Dst,
		Nexthops: []croutempls.Nexthop{
			{
				Kind:    croutempls.KindNone,
				Weight:  1,
				Counter: "no route mpls v4",
			},
		},
	})

	default16Prefix := netip.PrefixFrom(netip.AddrFrom16([16]byte{}), 0)
	default16Dst, _ := filter.Net6sFromPrefixes([]netip.Prefix{default16Prefix})
	rules = append(rules, croutempls.Rule{
		Dst6s: default16Dst,
		Nexthops: []croutempls.Nexthop{
			{
				Kind:    croutempls.KindNone,
				Weight:  1,
				Counter: "no route mpls v6",
			},
		},
	})

	return rules
}

func (m *NextHop) toFFI() croutempls.Nexthop {
	return croutempls.Nexthop{
		Kind:        croutempls.KindTun,
		Source:      m.Source,
		Destination: m.Destination,
		MPLSLabel:   m.MPLSLabel,
		Weight:      m.Weight,
		Counter:     m.Counter,
	}
}

// NewRouteMPLSService builds a RouteMPLSService bound to the supplied backend.
func NewRouteMPLSService(backend Backend, options ...RouteMPLSServiceOption) *RouteMPLSService {
	opts := newRouteMPLSServiceOptions()
	for _, o := range options {
		o(opts)
	}

	return &RouteMPLSService{
		backend: backend,
		configs: map[string]routeMPLSConfig{},
		log:     opts.Log,
	}
}

// ListConfigs returns the names of all route-mpls module configurations
// currently known to the service.
func (m *RouteMPLSService) ListConfigs(
	ctx context.Context,
	request *routemplspb.ListConfigsRequest,
) (*routemplspb.ListConfigsResponse, error) {
	m.shmLock.RLock()
	defer m.shmLock.RUnlock()

	response := &routemplspb.ListConfigsResponse{
		Configs: make([]string, 0, len(m.configs)),
	}
	for name := range m.configs {
		response.Configs = append(response.Configs, name)
	}
	sort.Strings(response.Configs)
	return response, nil
}

// ShowConfig returns the rules currently stored for the requested configuration.
func (m *RouteMPLSService) ShowConfig(
	ctx context.Context,
	req *routemplspb.ShowConfigRequest,
) (*routemplspb.ShowConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	m.shmLock.RLock()
	defer m.shmLock.RUnlock()

	config, ok := m.configs[name]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "config %q not found", name)
	}

	rules := make([]*routemplspb.Rule, 0)
	for _, prefixes := range config.prefixes {
		for prefix, nexthops := range prefixes {
			for _, nexthop := range nexthops.NextHops {
				rules = append(rules, &routemplspb.Rule{
					Prefix: &filterpb.IPPrefix{
						Addr:   prefix.Addr().AsSlice(),
						Length: uint32(prefix.Bits()),
					},
					Nexthop: &routemplspb.NextHop{
						Label:         nexthop.MPLSLabel,
						SourceIp:      commonpb.NewIPAddressFromAddr(nexthop.Source),
						DestinationIp: commonpb.NewIPAddressFromAddr(nexthop.Destination),
						Weight:        nexthop.Weight,
						Counter:       nexthop.Counter,
					},
				})
			}
		}
	}

	return &routemplspb.ShowConfigResponse{
		Name:  name,
		Rules: rules,
	}, nil
}

// DeleteConfig removes a route-mpls module configuration from the service and
// the dataplane.
func (m *RouteMPLSService) DeleteConfig(
	ctx context.Context,
	req *routemplspb.DeleteConfigRequest,
) (*routemplspb.DeleteConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	m.shmLock.Lock()
	defer m.shmLock.Unlock()

	config, ok := m.configs[name]
	if !ok {
		return nil, status.Error(codes.NotFound, "not found")
	}

	if err := m.backend.DeleteModule(name); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete module config %q: %v", name, err)
	}

	if config.handle != nil {
		config.handle.Free()
	}
	delete(m.configs, name)

	return &routemplspb.DeleteConfigResponse{}, nil
}

// CreateConfig creates a new route-mpls module configuration and publishes it
// to the dataplane.
func (m *RouteMPLSService) CreateConfig(
	ctx context.Context,
	req *routemplspb.CreateConfigRequest,
) (*routemplspb.CreateConfigResponse, error) {
	name := req.Name
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	prefixes := maptrie.NewMapTrie[netip.Prefix, netip.Addr, NextHopList](0)

	for _, rule := range req.Rules {
		prefix, err := makePrefix(rule.Prefix)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "failed to parse prefix: %v", err)
		}

		nextHop, err := makeNextHop(rule.Nexthop)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "failed to parse nexthop: %v", err)
		}

		prefixes.InsertOrUpdate(
			prefix,
			func() NextHopList {
				return NextHopList{
					NextHops: append(make([]NextHop, 0, 1), nextHop),
				}
			},
			func(list NextHopList) NextHopList {
				list.Insert(nextHop)
				return list
			},
		)
	}

	config := routeMPLSConfig{
		prefixes: prefixes,
	}

	m.shmLock.Lock()
	defer m.shmLock.Unlock()

	handle, err := m.backend.UpdateModule(name, config.buildRules())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	if old, ok := m.configs[name]; ok && old.handle != nil {
		old.handle.Free()
	}

	config.handle = handle
	m.configs[name] = config

	return &routemplspb.CreateConfigResponse{}, nil
}

// UpdateConfig applies incremental prefix updates or withdrawals to an
// existing route-mpls module configuration.
func (m *RouteMPLSService) UpdateConfig(
	ctx context.Context,
	req *routemplspb.UpdateConfigRequest,
) (*routemplspb.UpdateConfigResponse, error) {
	name := req.Name
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	m.shmLock.Lock()
	defer m.shmLock.Unlock()

	oldConfig, ok := m.configs[name]
	if !ok {
		oldConfig = routeMPLSConfig{
			prefixes: maptrie.NewMapTrie[netip.Prefix, netip.Addr, NextHopList](0),
		}
	}

	config := routeMPLSConfig{
		prefixes: oldConfig.prefixes.Clone(),
	}

	for _, update := range req.Updates {
		if u := update.GetUpdate(); u != nil {
			prefix, err := makePrefix(u.Prefix)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "failed to parse prefix: %v", err)
			}

			nextHop, err := makeNextHop(u.Nexthop)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "failed to parse nexthop: %v", err)
			}

			config.prefixes.InsertOrUpdate(
				prefix,
				func() NextHopList {
					return NextHopList{
						NextHops: append(make([]NextHop, 0, 1), nextHop),
					}
				},
				func(list NextHopList) NextHopList {
					list.Insert(nextHop)
					return list
				},
			)
		}

		if w := update.GetWithdraw(); w != nil {
			prefix, err := makePrefix(w.Prefix)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "failed to parse prefix: %v", err)
			}

			nextHop, err := makeNextHop(w.Nexthop)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "failed to parse nexthop: %v", err)
			}

			config.prefixes.UpdateOrDelete(
				prefix,
				func(list NextHopList) (NextHopList, bool) {
					list.Remove(nextHop)
					return list, len(list.NextHops) == 0
				},
			)
		}
	}

	handle, err := m.backend.UpdateModule(name, config.buildRules())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	if oldConfig.handle != nil {
		oldConfig.handle.Free()
	}

	config.handle = handle
	m.configs[name] = config

	return &routemplspb.UpdateConfigResponse{}, nil
}

func makePrefix(prefix *filterpb.IPPrefix) (netip.Prefix, error) {
	addr, ok := netip.AddrFromSlice(prefix.Addr)
	if !ok {
		return netip.Prefix{}, fmt.Errorf("invalid address length")
	}
	return netip.PrefixFrom(addr, int(prefix.Length)), nil
}

func makeNextHop(nexthop *routemplspb.NextHop) (NextHop, error) {
	src, err := nexthop.GetSourceIp().ToAddr()
	if err != nil {
		return NextHop{}, fmt.Errorf("invalid source_ip (bytes=%x): %w", nexthop.GetSourceIp().GetAddr(), err)
	}
	dst, err := nexthop.GetDestinationIp().ToAddr()
	if err != nil {
		return NextHop{}, fmt.Errorf("invalid destination_ip (bytes=%x): %w", nexthop.GetDestinationIp().GetAddr(), err)
	}

	return NextHop{
		Source:      src,
		Destination: dst,
		MPLSLabel:   nexthop.Label,
		Weight:      nexthop.Weight,
		Counter:     nexthop.Counter,
	}, nil
}
