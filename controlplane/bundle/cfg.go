package bundle

import (
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

	plain "github.com/yanet-platform/yanet2/devices/plain/controlplane"
	trafgen "github.com/yanet-platform/yanet2/devices/trafgen/controlplane"
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
	// Mirror is the configuration for the mirror module.
	Mirror *mirror.Config `yaml:"mirror"`
	// NAT64 is the configuration for the NAT64 module.
	NAT64 *nat64.Config `yaml:"nat64"`
	// Pdump is the configuration for the packet dump module.
	Pdump *pdump.Config `yaml:"pdump"`
	// ACL is the configuration for the acl module.
	ACL *acl.Config `yaml:"acl"`
	// Blackhole is the configuration for the blackhole module.
	Blackhole *blackhole.Config `yaml:"blackhole"`
}

// DevicesConfig describes built-in devices in the standard YANET bundle.
type DevicesConfig struct {
	// Plain is the configuration for the plain device.
	Plain *plain.Config `yaml:"plain"`
	// Vlan is the configuration for the vlan device.
	Vlan *vlan.Config `yaml:"vlan"`
	// Trafgen is the configuration for the traffic generator device.
	Trafgen *trafgen.Config `yaml:"trafgen"`
}
