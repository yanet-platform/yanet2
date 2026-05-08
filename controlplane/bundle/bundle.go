package bundle

import (
	"fmt"

	"go.uber.org/zap"

	// Blank import registers operator proto descriptors in the global
	// protobuf registry so the gateway HTTP/gRPC proxy can resolve
	// operatorpb services.
	"github.com/yanet-platform/yanet2/controlplane/gateway"
	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
	vlan "github.com/yanet-platform/yanet2/devices/vlan/controlplane"
	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/agent/go"
	decap "github.com/yanet-platform/yanet2/modules/decap/controlplane"
	dscp "github.com/yanet-platform/yanet2/modules/dscp/controlplane"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
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
				return route_mpls.NewRouteMPLSModule(modulesCfg.RouteMPLS, log)
			},
		},
		{
			name: "decap module",
			new: func() (gateway.Service, error) {
				return decap.NewDecapModule(modulesCfg.Decap, log)
			},
		},
		{
			name: "dscp module",
			new: func() (gateway.Service, error) {
				return dscp.NewDSCPModule(modulesCfg.DSCP, log)
			},
		},
		{
			name: "forward module",
			new: func() (gateway.Service, error) {
				return forward.NewForwardModule(modulesCfg.Forward, log)
			},
		},
		{
			name: "nat64 module",
			new: func() (gateway.Service, error) {
				return nat64.NewNAT64Module(modulesCfg.NAT64, log)
			},
		},
		{
			name: "pdump module",
			new: func() (gateway.Service, error) {
				return pdump.NewPdumpModule(modulesCfg.Pdump, log)
			},
		},
		{
			name: "acl module",
			new: func() (gateway.Service, error) {
				return acl.NewACLModule(modulesCfg.ACL, log)
			},
		},
		{
			name: "balancer module",
			new: func() (gateway.Service, error) {
				return balancer.NewBalancerModule(modulesCfg.Balancer, log)
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
				return vlan.NewDeviceVlanDevice(devicesCfg.Vlan, log)
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
