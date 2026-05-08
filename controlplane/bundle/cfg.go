package bundle

import (
	"fmt"

	acl "github.com/yanet-platform/yanet2/modules/acl/controlplane"
	balancer "github.com/yanet-platform/yanet2/modules/balancer/agent/go"
	decap "github.com/yanet-platform/yanet2/modules/decap/controlplane"
	dscp "github.com/yanet-platform/yanet2/modules/dscp/controlplane"
	forward "github.com/yanet-platform/yanet2/modules/forward/controlplane"
	nat64 "github.com/yanet-platform/yanet2/modules/nat64/controlplane"
	pdump "github.com/yanet-platform/yanet2/modules/pdump/controlplane"
	route_mpls "github.com/yanet-platform/yanet2/modules/route-mpls/controlplane"
	route "github.com/yanet-platform/yanet2/modules/route/controlplane"

	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
	vlan "github.com/yanet-platform/yanet2/devices/vlan/controlplane"
)

// ModulesConfig describes built-in modules in the standard YANET bundle.
type ModulesConfig struct {
	// Route is the configuration for the route module.
	Route *route.Config `yaml:"route"`
	// RouteMPLS is the configuration for the route mpls module.
	RouteMPLS *route_mpls.Config `yaml:"route-mpls"`
	// Decap is the configuration for the decap module.
	Decap *decap.Config `yaml:"decap"`
	// DSCP is the configuration for the dscp module.
	DSCP *dscp.Config `yaml:"dscp"`
	// Forward is the configuration for the forward module.
	Forward *forward.Config `yaml:"forward"`
	// NAT64 is the configuration for the NAT64 module.
	NAT64 *nat64.Config `yaml:"nat64"`
	// Pdump is the configuration for the packet dump module.
	Pdump *pdump.Config `yaml:"pdump"`
	// Balancer is the configuration for the balancer module.
	Balancer *balancer.Config `yaml:"balancer"`
	// ACL is the configuration for the acl module.
	ACL *acl.Config `yaml:"acl"`
}

// DevicesConfig describes built-in devices in the standard YANET bundle.
type DevicesConfig struct {
	// Plain is the configuration for the plain device.
	Plain *plain.Config `yaml:"plain"`
	// Vlan is the configuration for the vlan device.
	Vlan *vlan.Config `yaml:"vlan"`
}

// DefaultModulesConfig returns the default config for the bundled modules.
func DefaultModulesConfig() ModulesConfig {
	return ModulesConfig{
		Route:     route.DefaultConfig(),
		RouteMPLS: route_mpls.DefaultConfig(),
		Decap:     decap.DefaultConfig(),
		DSCP:      dscp.DefaultConfig(),
		Forward:   forward.DefaultConfig(),
		NAT64:     nat64.DefaultConfig(),
		Pdump:     pdump.DefaultConfig(),
		Balancer:  balancer.DefaultConfig(),
		ACL:       acl.DefaultConfig(),
	}
}

// DefaultDevicesConfig returns the default config for the bundled devices.
func DefaultDevicesConfig() DevicesConfig {
	return DevicesConfig{
		Plain: plain.DefaultConfig(),
		Vlan:  vlan.DefaultConfig(),
	}
}

// Validate validates the modules config.
func (m *ModulesConfig) Validate() error {
	if m.Route == nil {
		return fmt.Errorf("route module is not configured")
	}
	if m.Decap == nil {
		return fmt.Errorf("decap module is not configured")
	}
	if m.DSCP == nil {
		return fmt.Errorf("dscp module is not configured")
	}
	if m.Forward == nil {
		return fmt.Errorf("forward module is not configured")
	}
	if m.NAT64 == nil {
		return fmt.Errorf("nat64 module is not configured")
	}
	if m.Balancer == nil {
		return fmt.Errorf("balancer module is not configured")
	}
	if m.ACL == nil {
		return fmt.Errorf("acl module is not configured")
	}
	return nil
}

// Validate validates the devices config.
func (m *DevicesConfig) Validate() error {
	if m.Plain == nil {
		return fmt.Errorf("plain device is not configured")
	}
	if m.Vlan == nil {
		return fmt.Errorf("vlan device is not configured")
	}
	return nil
}
