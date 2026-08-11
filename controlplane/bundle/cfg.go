package bundle

import (
	"github.com/yanet-platform/yanet2/common/go/xcfg"
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
)

// ModulesConfig describes built-in modules in the standard YANET bundle.
type ModulesConfig struct {
	// Route is the configuration for the route module.
	Route xcfg.Optional[route.Config] `yaml:"route"`
	// RouteMPLS is the configuration for the route mpls module.
	RouteMPLS xcfg.Optional[route_mpls.Config] `yaml:"route-mpls"`
	// Decap is the configuration for the decap module.
	Decap xcfg.Optional[decap.Config] `yaml:"decap"`
	// DSCP is the configuration for the dscp module.
	DSCP xcfg.Optional[dscp.Config] `yaml:"dscp"`
	// Forward is the configuration for the forward module.
	Forward xcfg.Optional[forward.Config] `yaml:"forward"`
	// Mirror is the configuration for the mirror module.
	Mirror xcfg.Optional[mirror.Config] `yaml:"mirror"`
	// NAT64 is the configuration for the NAT64 module.
	NAT64 xcfg.Optional[nat64.Config] `yaml:"nat64"`
	// Pdump is the configuration for the packet dump module.
	Pdump xcfg.Optional[pdump.Config] `yaml:"pdump"`
	// ACL is the configuration for the acl module.
	ACL xcfg.Optional[acl.Config] `yaml:"acl"`
	// Blackhole is the configuration for the blackhole module.
	Blackhole xcfg.Optional[blackhole.Config] `yaml:"blackhole"`
}

// DevicesConfig describes built-in devices in the standard YANET bundle.
type DevicesConfig struct {
	// Plain is the configuration for the plain device.
	Plain xcfg.Optional[plain.Config] `yaml:"plain"`
	// Vlan is the configuration for the vlan device.
	Vlan xcfg.Optional[vlan.Config] `yaml:"vlan"`
	// Trafgen is the configuration for the traffic generator device.
	Trafgen xcfg.Optional[trafgen.Config] `yaml:"trafgen"`
}
