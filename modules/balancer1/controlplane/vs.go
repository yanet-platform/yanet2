package balancer

import "net/netip"

type VsFlags struct {
	// Use GRE for incapsulation
	GRE bool

	// One packet scheduler
	OPS bool

	// Use pure L3 scheduling, which means
	// service listens to all ports (but transport protocol is fixed)
	PureL3 bool

	// Fix MSS tcp option
	FixMSS bool
}

type VsProto string

const (
	VsProtoUdp VsProto = "UDP"
	VsProtoTcp VsProto = "TCP"
)

type VirtualService struct {
	Address    netip.Addr
	Port       uint16
	Proto      VsProto
	AllowedSrc []netip.Prefix
	Reals      []Real
	Flags      VsFlags
}
