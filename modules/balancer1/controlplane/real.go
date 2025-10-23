package balancer

import "net/netip"

type Real struct {
	Weight uint16

	DstAddr netip.Addr

	SrcAddr netip.Addr
	SrcMask netip.Addr

	Enabled bool
}
