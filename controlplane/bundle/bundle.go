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
	fwstate "github.com/yanet-platform/yanet2/modules/fwstate/controlplane"
	mirror "github.com/yanet-platform/yanet2/modules/mirror/controlplane"
	nat64 "github.com/yanet-platform/yanet2/modules/nat64/controlplane"
	pdump "github.com/yanet-platform/yanet2/modules/pdump/controlplane"
	route_mpls "github.com/yanet-platform/yanet2/modules/route-mpls/controlplane"
	route "github.com/yanet-platform/yanet2/modules/route/controlplane"
	_ "github.com/yanet-platform/yanet2/operators/route/operatorpb/v1"
)

type serviceConstructor func() (gateway.Service, error)

type serviceFactory struct {
	Name string
	// Configured reports whether the corresponding config block was
	// present in the loaded document. A factory with Configured == false
	// is skipped: no service is constructed and no agent is attached.
	Configured bool
	New        serviceConstructor
}

// Bundle is the standard YANET distribution bundle of built-in modules and
// devices.
//
// It instantiates each module/device from config and exposes them uniformly
// as a slice of gateway.Service.
type Bundle struct {
	services []gateway.Service
}

// Option configures the NewBundle constructor.
type Option func(*bundleOptions)

type bundleOptions struct {
	Log *zap.Logger
}

func newBundleOptions() *bundleOptions {
	return &bundleOptions{
		Log: zap.NewNop(),
	}
}

// WithLog sets the logger for the bundle.
func WithLog(log *zap.Logger) Option {
	return func(o *bundleOptions) {
		o.Log = log
	}
}

// NewBundle constructs every bundled module and device from the given config.
func NewBundle(
	modulesCfg ModulesConfig,
	devicesCfg DevicesConfig,
	options ...Option,
) (*Bundle, error) {
	opts := newBundleOptions()
	for _, o := range options {
		o(opts)
	}

	services, err := buildServices(modulesCfg, devicesCfg, opts.Log)
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
			Name:       "route module",
			Configured: modulesCfg.Route.Unwrap() != nil,
			New: func() (gateway.Service, error) {
				return route.NewRouteModule(modulesCfg.Route.Unwrap(), route.WithLog(log))
			},
		},
		{
			Name:       "route mpls module",
			Configured: modulesCfg.RouteMPLS.Unwrap() != nil,
			New: func() (gateway.Service, error) {
				return route_mpls.NewRouteMPLSModule(modulesCfg.RouteMPLS.Unwrap(), route_mpls.WithLog(log))
			},
		},
		{
			Name:       "decap module",
			Configured: modulesCfg.Decap.Unwrap() != nil,
			New: func() (gateway.Service, error) {
				return decap.NewDecapModule(modulesCfg.Decap.Unwrap(), decap.WithLog(log))
			},
		},
		{
			Name:       "dscp module",
			Configured: modulesCfg.DSCP.Unwrap() != nil,
			New: func() (gateway.Service, error) {
				return dscp.NewDSCPModule(modulesCfg.DSCP.Unwrap(), dscp.WithLog(log))
			},
		},
		{
			Name:       "forward module",
			Configured: modulesCfg.Forward.Unwrap() != nil,
			New: func() (gateway.Service, error) {
				return forward.NewForwardModule(modulesCfg.Forward.Unwrap(), forward.WithLog(log))
			},
		},
		{
			Name:       "mirror module",
			Configured: modulesCfg.Mirror.Unwrap() != nil,
			New: func() (gateway.Service, error) {
				return mirror.NewMirrorModule(modulesCfg.Mirror.Unwrap(), mirror.WithLog(log))
			},
		},
		{
			Name:       "nat64 module",
			Configured: modulesCfg.NAT64.Unwrap() != nil,
			New: func() (gateway.Service, error) {
				return nat64.NewNAT64Module(modulesCfg.NAT64.Unwrap(), nat64.WithLog(log))
			},
		},
		{
			Name:       "pdump module",
			Configured: modulesCfg.Pdump.Unwrap() != nil,
			New: func() (gateway.Service, error) {
				return pdump.NewPdumpModule(modulesCfg.Pdump.Unwrap(), pdump.WithLog(log))
			},
		},
		{
			Name:       "acl module",
			Configured: modulesCfg.ACL.Unwrap() != nil,
			New: func() (gateway.Service, error) {
				return acl.NewACLModule(modulesCfg.ACL.Unwrap(), acl.WithModuleLog(log))
			},
		},
		{
			Name:       "fwstate module",
			Configured: modulesCfg.FWState.Unwrap() != nil,
			New: func() (gateway.Service, error) {
				return fwstate.NewFWStateModule(modulesCfg.FWState.Unwrap(), fwstate.WithLog(log))
			},
		},
		{
			Name:       "blackhole module",
			Configured: modulesCfg.Blackhole.Unwrap() != nil,
			New: func() (gateway.Service, error) {
				return blackhole.NewBlackholeModule(modulesCfg.Blackhole.Unwrap(), blackhole.WithLog(log))
			},
		},
		{
			Name:       "plain device",
			Configured: devicesCfg.Plain.Unwrap() != nil,
			New: func() (gateway.Service, error) {
				return plain.NewDevicePlainDevice(devicesCfg.Plain.Unwrap(), plain.WithLog(log))
			},
		},
		{
			Name:       "vlan device",
			Configured: devicesCfg.Vlan.Unwrap() != nil,
			New: func() (gateway.Service, error) {
				return vlan.NewDeviceVlanDevice(devicesCfg.Vlan.Unwrap(), vlan.WithLog(log))
			},
		},
		{
			Name:       "trafgen device",
			Configured: devicesCfg.Trafgen.Unwrap() != nil,
			New: func() (gateway.Service, error) {
				return trafgen.NewTrafgenDevice(devicesCfg.Trafgen.Unwrap(), trafgen.WithLog(log))
			},
		},
	}

	services := make([]gateway.Service, 0, len(factories))

	for _, factory := range factories {
		if !factory.Configured {
			log.Info("skipping service with no config", zap.String("service", factory.Name))
			continue
		}

		service, err := factory.New()
		if err != nil {
			return nil, fmt.Errorf("failed to initialize %s: %w", factory.Name, err)
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
