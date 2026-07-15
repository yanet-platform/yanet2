package bundle

import (
	"fmt"

	"go.uber.org/zap"

	// Blank import registers operator proto descriptors in the global
	// protobuf registry so the gateway HTTP/gRPC proxy can resolve
	// operatorpb services.
	"github.com/yanet-platform/yanet2/controlplane/gateway"
	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
	trafgen "github.com/yanet-platform/yanet2/devices/trafgen/controlplane"
	vlan "github.com/yanet-platform/yanet2/devices/vlan/controlplane"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
	blackhole "github.com/yanet-platform/yanet2/modules/blackhole/controlplane"
	decap "github.com/yanet-platform/yanet2/modules/decap/controlplane"
	dscp "github.com/yanet-platform/yanet2/modules/dscp/controlplane"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
	mirror "github.com/yanet-platform/yanet2/modules/mirror/controlplane"
	nat64 "github.com/yanet-platform/yanet2/modules/nat64/controlplane"
	pdump "github.com/yanet-platform/yanet2/modules/pdump/controlplane"
	route_mpls "github.com/yanet-platform/yanet2/modules/route-mpls/controlplane"
	route "github.com/yanet-platform/yanet2/modules/route/controlplane"
	_ "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

type serviceConstructor func() (gateway.Service, error)

type serviceFactory struct {
	name string
	new  serviceConstructor
}

// Bundle is the standard YANET distribution bundle of built-in modules and
// devices.
//
// It instantiates each module/device from config and exposes them uniformly
// as a slice of gateway.Service.
type Bundle struct {
	services []gateway.Service
}

// NewBundle constructs every bundled module and device from the given config.
func NewBundle(
	modulesCfg ModulesConfig,
	devicesCfg DevicesConfig,
	log *zap.Logger,
) (*Bundle, error) {
	services, err := buildServices(modulesCfg, devicesCfg, log)
	if err != nil {
		return nil, err
	}

	return &Bundle{services: services}, nil
}

func buildServices(
	modulesCfg ModulesConfig,
	devicesCfg DevicesConfig,
	log *zap.Logger,
) ([]gateway.Service, error) {
	factories := []serviceFactory{
		{
			name: "route module",
			new: func() (gateway.Service, error) {
				return route.NewRouteModule(modulesCfg.Route, route.WithLog(log))
			},
		},
		{
			name: "route mpls module",
			new: func() (gateway.Service, error) {
				return route_mpls.NewRouteMPLSModule(modulesCfg.RouteMPLS, route_mpls.WithLog(log))
			},
		},
		{
			name: "decap module",
			new: func() (gateway.Service, error) {
				return decap.NewDecapModule(modulesCfg.Decap, decap.WithLog(log))
			},
		},
		{
			name: "dscp module",
			new: func() (gateway.Service, error) {
				return dscp.NewDSCPModule(modulesCfg.DSCP, dscp.WithLog(log))
			},
		},
		{
			name: "forward module",
			new: func() (gateway.Service, error) {
				return forward.NewForwardModule(modulesCfg.Forward, forward.WithLog(log))
			},
		},
		{
			name: "mirror module",
			new: func() (gateway.Service, error) {
				return mirror.NewMirrorModule(modulesCfg.Mirror, mirror.WithLog(log))
			},
		},
		{
			name: "nat64 module",
			new: func() (gateway.Service, error) {
				return nat64.NewNAT64Module(modulesCfg.NAT64, nat64.WithLog(log))
			},
		},
		{
			name: "pdump module",
			new: func() (gateway.Service, error) {
				return pdump.NewPdumpModule(modulesCfg.Pdump, pdump.WithLog(log))
			},
		},
		{
			name: "acl module",
			new: func() (gateway.Service, error) {
				return acl.NewACLModule(modulesCfg.ACL, acl.WithModuleLog(log))
			},
		},
		{
			name: "blackhole module",
			new: func() (gateway.Service, error) {
				return blackhole.NewBlackholeModule(modulesCfg.Blackhole, blackhole.WithLog(log))
			},
		},
		{
			name: "plain device",
			new: func() (gateway.Service, error) {
				return plain.NewDevicePlainDevice(devicesCfg.Plain, log)
			},
		},
		{
			name: "vlan device",
			new: func() (gateway.Service, error) {
				return vlan.NewDeviceVlanDevice(devicesCfg.Vlan, vlan.WithLog(log))
			},
		},
		{
			name: "trafgen device",
			new: func() (gateway.Service, error) {
				return trafgen.NewTrafgenDevice(devicesCfg.Trafgen, trafgen.WithLog(log))
			},
		},
	}

	services := make([]gateway.Service, 0, len(factories))

	for _, factory := range factories {
		service, err := factory.new()
		if err != nil {
			return nil, fmt.Errorf("failed to initialize %s: %w", factory.name, err)
		}

		services = append(services, service)
	}

	return services, nil
}

// Services returns all services from the bundle, ready to be registered with
// the gateway via gateway.WithService.
func (m *Bundle) Services() []gateway.Service {
	return m.services
}
