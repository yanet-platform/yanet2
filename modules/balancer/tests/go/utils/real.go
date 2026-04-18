package utils

import (
	"bytes"
	"cmp"
	"net/netip"

	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

type RealID struct {
	addrLen int
	addr    [16]byte
}

func (r *RealID) Compare(other *RealID) int {
	if r.addrLen != other.addrLen {
		return cmp.Compare(r.addrLen, other.addrLen)
	}
	return bytes.Compare(r.addr[:r.addrLen], other.addr[:other.addrLen])
}

func (r *RealID) String() string {
	ip, _ := netip.AddrFromSlice(r.addr[:r.addrLen])
	return ip.String()
}

func RealIDFromPb(r *balancerpb.RelativeRealIdentifier) RealID {
	addr := [16]byte{}
	copy(addr[:], r.Ip)
	return RealID{
		addrLen: len(r.Ip),
		addr:    addr,
	}
}

func RealStatsEqual(a, b *balancerpb.RealStats) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.PacketsRealDisabled == b.PacketsRealDisabled &&
		a.OpsPackets == b.OpsPackets &&
		a.ErrorIcmpPackets == b.ErrorIcmpPackets &&
		a.CreatedSessions == b.CreatedSessions &&
		a.Packets == b.Packets &&
		a.Bytes == b.Bytes
}
