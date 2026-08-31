package route_mpls

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"sync"

	"go.uber.org/zap"

	"github.com/yanet-platform/xnetip"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/bindings/go/filter"
	commonpb "github.com/yanet-platform/yanet2/common/commonpb/v1"
	"github.com/yanet-platform/yanet2/common/go/maptrie"
	"github.com/yanet-platform/yanet2/controlplane/ffi"
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

	// deferred holds superseded module handles whose free was refused
	// because a live configuration generation still referenced them.
	// This service is their owner: it retries them on its next update,
	// through ReclaimDeferred, and nothing else remembers them.
	deferred []ModuleHandle

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
	Prefixes maptrie.MapTrie[netip.Prefix, netip.Addr, NextHopList]
	Handle   ModuleHandle
}

// Free releases the module handle held by the config.
//
// It is safe to call even when no handle is held.
func (m *routeMPLSConfig) Free() error {
	if m.Handle == nil {
		return nil
	}
	return m.Handle.Free()
}

// BuildRules iterates the maptrie from longest to shortest prefix and
// assembles the croutempls.Rule slice, appending default drop rules for both
// IPv4 and IPv6 at the end.
func (m *routeMPLSConfig) BuildRules() []croutempls.Rule {
	rules := make([]croutempls.Rule, 0)

	for prefixLen := 128; prefixLen >= 0; prefixLen-- {
		for prefix, nextHopList := range m.Prefixes[prefixLen] {
			nexthops := make([]croutempls.Nexthop, 0, len(nextHopList.NextHops))
			for _, nh := range nextHopList.NextHops {
				nexthops = append(nexthops, nh.ToFFI())
			}

			rule := croutempls.Rule{Nexthops: nexthops}
			if net4, ok := xnetip.ContiguousFromPrefix4(prefix); ok {
				rule.Dst4s = []xnetip.Contiguous[xnetip.Network4]{net4}
			} else if net6, ok := xnetip.ContiguousFromPrefix6(prefix); ok {
				rule.Dst6s = []xnetip.BiContiguous{xnetip.BiContiguousFromContiguous(net6)}
			}

			rules = append(rules, rule)
		}
	}

	rules = append(rules, croutempls.Rule{
		Dst4s: []xnetip.Contiguous[xnetip.Network4]{filter.UnspecifiedIPv4},
		Nexthops: []croutempls.Nexthop{
			{
				Kind:    croutempls.KindNone,
				Weight:  1,
				Counter: "no route mpls v4",
			},
		},
	})

	rules = append(rules, croutempls.Rule{
		Dst6s: []xnetip.BiContiguous{filter.UnspecifiedIPv6},
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

// ToFFI converts the nexthop into its bindings representation for the
// route-mpls dataplane.
func (m *NextHop) ToFFI() croutempls.Nexthop {
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
	for _, prefixes := range config.Prefixes {
		for prefix, nexthops := range prefixes {
			network, err := commonpb.NewIPPrefixFromPrefix(prefix)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "failed to encode prefix %q: %v", prefix, err)
			}
			for _, nexthop := range nexthops.NextHops {
				rules = append(rules, &routemplspb.Rule{
					Prefix: network,
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

	m.reclaimDeferred()
	m.parkOrFree(config.Handle)
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
		prefix, err := rule.GetPrefix().ToPrefix()
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
		Prefixes: prefixes,
	}

	m.shmLock.Lock()
	defer m.shmLock.Unlock()

	handle, err := m.backend.UpdateModule(name, config.BuildRules())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	m.reclaimDeferred()

	if old, ok := m.configs[name]; ok {
		m.parkOrFree(old.Handle)
	}

	config.Handle = handle
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
			Prefixes: maptrie.NewMapTrie[netip.Prefix, netip.Addr, NextHopList](0),
		}
	}

	config := routeMPLSConfig{
		Prefixes: oldConfig.Prefixes.Clone(),
	}

	for _, update := range req.Updates {
		if u := update.GetUpdate(); u != nil {
			prefix, err := u.GetPrefix().ToPrefix()
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "failed to parse prefix: %v", err)
			}

			nextHop, err := makeNextHop(u.Nexthop)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "failed to parse nexthop: %v", err)
			}

			config.Prefixes.InsertOrUpdate(
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
			prefix, err := w.GetPrefix().ToPrefix()
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "failed to parse prefix: %v", err)
			}

			nextHop, err := makeWithdrawNextHop(w.Nexthop)
			if err != nil {
				return nil, status.Errorf(codes.InvalidArgument, "failed to parse nexthop: %v", err)
			}

			config.Prefixes.UpdateOrDelete(
				prefix,
				func(list NextHopList) (NextHopList, bool) {
					list.Remove(nextHop)
					return list, len(list.NextHops) == 0
				},
			)
		}
	}

	handle, err := m.backend.UpdateModule(name, config.BuildRules())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update module config: %v", err)
	}

	m.reclaimDeferred()
	if ok {
		m.parkOrFree(oldConfig.Handle)
	}

	config.Handle = handle
	m.configs[name] = config

	return &routemplspb.UpdateConfigResponse{}, nil
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

// makeWithdrawNextHop parses only the destination and label, the sole
// fields a withdraw is matched by. Other nexthop fields are ignored.
func makeWithdrawNextHop(nexthop *routemplspb.NextHop) (NextHop, error) {
	destination, err := nexthop.GetDestinationIp().ToAddr()
	if err != nil {
		return NextHop{}, fmt.Errorf("invalid destination_ip (bytes=%x): %w", nexthop.GetDestinationIp().GetAddr(), err)
	}

	return NextHop{
		Destination: destination,
		MPLSLabel:   nexthop.GetLabel(),
	}, nil
}

// parkOrFree frees the handle when it is dangling and parks it for
// retry when a live generation still references it. The caller must
// hold m.shmLock.
func (m *RouteMPLSService) parkOrFree(handle ModuleHandle) {
	if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
		m.deferred = append(m.deferred, handle)
	}
}

// ReclaimDeferred retries every deferred handle, dropping the ones whose
// generations have drained and keeping the rest deferred. It is the
// reclamation handler for this module's superseded configs; the service
// itself runs it after each successful publish, and anything else may
// call it at any time.
func (m *RouteMPLSService) ReclaimDeferred() {
	m.shmLock.Lock()
	defer m.shmLock.Unlock()
	m.reclaimDeferred()
}

// reclaimDeferred is ReclaimDeferred without the lock. The caller must
// hold m.shmLock.
func (m *RouteMPLSService) reclaimDeferred() {
	kept := m.deferred[:0]
	for _, handle := range m.deferred {
		if err := handle.Free(); errors.Is(err, ffi.ErrStillReferenced) {
			kept = append(kept, handle)
		}
	}
	clear(m.deferred[len(kept):])
	m.deferred = kept
}
