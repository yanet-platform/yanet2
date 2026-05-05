package route

import (
	"context"
	"net/netip"
	"sync"

	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yanet-platform/yanet2/common/commonpb"
	"github.com/yanet-platform/yanet2/common/go/xnetip"
	"github.com/yanet-platform/yanet2/modules/route/bindings/go/croute"
	"github.com/yanet-platform/yanet2/modules/route/controlplane/routepb"
)

// RouteServiceOption configures the RouteService constructor.
type RouteServiceOption func(*routeServiceOptions)

type routeServiceOptions struct {
	Log *zap.Logger
}

func newRouteServiceOptions() *routeServiceOptions {
	return &routeServiceOptions{
		Log: zap.NewNop(),
	}
}

// WithRouteServiceLog sets the logger for the RouteService.
func WithRouteServiceLog(log *zap.Logger) RouteServiceOption {
	return func(o *routeServiceOptions) {
		o.Log = log
	}
}

// RouteService is the gRPC service implementation backing the slim
// route-module shim.
type RouteService struct {
	routepb.UnimplementedRouteServiceServer

	backend Backend

	// shmLock serializes shared-memory mutations and protects the
	// configs map.
	shmLock sync.RWMutex
	configs map[string]ModuleHandle

	log *zap.Logger
}

// NewRouteService builds a RouteService bound to the supplied backend.
func NewRouteService(backend Backend, options ...RouteServiceOption) *RouteService {
	opts := newRouteServiceOptions()
	for _, o := range options {
		o(opts)
	}

	return &RouteService{
		backend: backend,
		configs: map[string]ModuleHandle{},
		log:     opts.Log,
	}
}

// ListConfigs returns the names of all route module configurations
// currently known to the service.
func (m *RouteService) ListConfigs(
	ctx context.Context,
	req *routepb.ListConfigsRequest,
) (*routepb.ListConfigsResponse, error) {
	m.shmLock.RLock()
	defer m.shmLock.RUnlock()

	response := &routepb.ListConfigsResponse{
		Configs: make([]string, 0, len(m.configs)),
	}
	for name := range m.configs {
		response.Configs = append(response.Configs, name)
	}
	return response, nil
}

// ShowFIB returns the FIB entries currently applied in shared memory
// for the requested configuration.
func (m *RouteService) ShowFIB(
	ctx context.Context,
	req *routepb.ShowFIBRequest,
) (*routepb.ShowFIBResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	// Hold RLock for the entire DumpFIB call so a concurrent Free under
	// shmLock.Lock cannot release the underlying shared memory.
	m.shmLock.RLock()
	defer m.shmLock.RUnlock()

	module, ok := m.configs[name]
	if !ok {
		return &routepb.ShowFIBResponse{}, nil
	}

	entries, err := module.DumpFIB()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to dump FIB: %v", err)
	}

	response := &routepb.ShowFIBResponse{
		Entries: make([]*routepb.FIBEntry, 0, len(entries)),
	}
	for _, e := range entries {
		if req.GetIpv4Only() && e.AddressFamily != croute.AddressFamilyIPv4 {
			continue
		}
		if req.GetIpv6Only() && e.AddressFamily != croute.AddressFamilyIPv6 {
			continue
		}

		nexthops := make([]*routepb.FIBNexthop, len(e.Nexthops))
		for idx, nh := range e.Nexthops {
			nexthops[idx] = &routepb.FIBNexthop{
				DstMac: commonpb.NewMACAddressEUI48([6]byte(nh.DstMAC)),
				SrcMac: commonpb.NewMACAddressEUI48([6]byte(nh.SrcMAC)),
				Device: nh.Device,
			}
		}

		response.Entries = append(response.Entries, &routepb.FIBEntry{
			Prefix:   formatPrefixRange(e.PrefixFrom, e.PrefixTo),
			Nexthops: nexthops,
		})
	}
	return response, nil
}

// DeleteConfig deletes a route module configuration.
func (m *RouteService) DeleteConfig(
	ctx context.Context,
	req *routepb.DeleteConfigRequest,
) (*routepb.DeleteConfigResponse, error) {
	name := req.GetName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module config name is required")
	}

	m.shmLock.Lock()
	defer m.shmLock.Unlock()

	module, ok := m.configs[name]
	if !ok {
		return &routepb.DeleteConfigResponse{}, nil
	}

	if err := m.backend.DeleteModule(name); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to delete module config %q: %v", name, err)
	}
	module.Free()
	delete(m.configs, name)

	return &routepb.DeleteConfigResponse{}, nil
}

// UpdateFIB applies a freshly-built FIB to the dataplane atomically.
func (m *RouteService) UpdateFIB(
	ctx context.Context,
	req *routepb.UpdateFIBRequest,
) (*routepb.UpdateFIBResponse, error) {
	name := req.GetModuleName()
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "module_name is required")
	}

	m.shmLock.Lock()
	defer m.shmLock.Unlock()

	module, err := m.backend.UpdateModule(name, req.GetEntries())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to apply FIB for %q: %v", name, err)
	}

	if old, ok := m.configs[name]; ok {
		old.Free()
	}
	m.configs[name] = module

	return &routepb.UpdateFIBResponse{}, nil
}

// formatPrefixRange converts an address range to a human-readable
// string. If the range corresponds to a single CIDR prefix, it returns
// CIDR notation; otherwise "from-to" range notation.
func formatPrefixRange(from, to netip.Addr) string {
	if prefix, ok := xnetip.RangeToCIDR(from, to); ok {
		return prefix.String()
	}
	return from.String() + "-" + to.String()
}
