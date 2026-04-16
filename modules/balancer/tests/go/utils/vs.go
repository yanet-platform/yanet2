package utils

import (
	"bytes"
	"cmp"
	"fmt"
	"net/netip"

	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

type VsID struct {
	addrLen int
	addr    [16]byte
	port    uint16
	proto   balancerpb.TransportProto
}

func (vs *VsID) Compare(other *VsID) int {
	if vs.addrLen != other.addrLen {
		return cmp.Compare(vs.addrLen, other.addrLen)
	}
	if cmp := bytes.Compare(vs.addr[:vs.addrLen], other.addr[:other.addrLen]); cmp != 0 {
		return cmp
	}
	if cmp := cmp.Compare(vs.port, other.port); cmp != 0 {
		return cmp
	}
	return cmp.Compare(vs.proto, other.proto)
}

func (vs *VsID) String() string {
	ip, _ := netip.AddrFromSlice(vs.addr[:vs.addrLen])
	proto := "TCP"
	if vs.proto == balancerpb.TransportProto_UDP {
		proto = "UDP"
	}
	ips := ip.String()
	if ip.Is6() {
		ips = fmt.Sprintf("[%s]", ips)
	}
	return fmt.Sprintf("%s:%d/%s", ips, vs.port, proto)
}

func VsIDFromPb(vs *balancerpb.VsIdentifier) VsID {
	if len(vs.Addr) != 4 && len(vs.Addr) != 16 {
		panic(fmt.Sprintf("invalid address length: %d", len(vs.Addr)))
	}
	if vs.Port > 65535 {
		panic(fmt.Sprintf("invalid port: %d", vs.Port))
	}
	if vs.Proto != balancerpb.TransportProto_TCP && vs.Proto != balancerpb.TransportProto_UDP {
		panic(fmt.Sprintf("invalid protocol: %s", vs.Proto))
	}
	addr := [16]byte{}
	copy(addr[:], vs.Addr)
	return VsID{
		addrLen: len(vs.Addr),
		addr:    addr,
		port:    uint16(vs.Port),
		proto:   vs.Proto,
	}
}
