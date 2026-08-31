package main

// The blank imports link every gateway-facing proto package into the
// binary, so a target may spell any public module or device method.
//
// The private tree builds its own binary with its own bundle. The aclpb,
// fwstatepb, and fwstatemappb packages are absent on purpose: their
// handwritten helpers import CGO bindings, which this pure-Go binary
// does not link.
import (
	_ "github.com/yanet-platform/yanet2/devices/plain/controlplane/plainpb/v1"
	_ "github.com/yanet-platform/yanet2/devices/trafgen/controlplane/trafgenpb/v1"
	_ "github.com/yanet-platform/yanet2/devices/vlan/controlplane/vlanpb/v1"
	_ "github.com/yanet-platform/yanet2/modules/balancer2/controlplane/balancerpb/v1"
	_ "github.com/yanet-platform/yanet2/modules/blackhole/controlplane/blackholepb/v1"
	_ "github.com/yanet-platform/yanet2/modules/decap/controlplane/decappb/v1"
	_ "github.com/yanet-platform/yanet2/modules/dscp/controlplane/dscppb/v1"
	_ "github.com/yanet-platform/yanet2/modules/forward/controlplane/forwardpb/v1"
	_ "github.com/yanet-platform/yanet2/modules/mirror/controlplane/mirrorpb/v1"
	_ "github.com/yanet-platform/yanet2/modules/nat64/controlplane/nat64pb/v1"
	_ "github.com/yanet-platform/yanet2/modules/pdump/controlplane/pdumppb/v1"
	_ "github.com/yanet-platform/yanet2/modules/route-mpls/controlplane/routemplspb/v1"
	_ "github.com/yanet-platform/yanet2/modules/route/controlplane/routepb/v1"
	_ "github.com/yanet-platform/yanet2/modules/unrdup/controlplane/unrduppb/v1"
)
