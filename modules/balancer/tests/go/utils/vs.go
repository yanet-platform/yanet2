package utils

import (
	"bytes"
	"cmp"
	"fmt"
	"math/rand/v2"
	"net/netip"

	balancer "github.com/yanet-platform/yanet2/modules/balancer/controlplane"
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

func VsStatsEquals(a *balancerpb.VsStats, b *balancerpb.VsStats) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.IncomingPackets == b.IncomingPackets &&
		a.IncomingBytes == b.IncomingBytes &&
		a.PacketSrcNotAllowed == b.PacketSrcNotAllowed &&
		a.NoReals == b.NoReals &&
		a.SessionTableOverflow == b.SessionTableOverflow &&
		a.EchoIcmpPackets == b.EchoIcmpPackets &&
		a.ErrorIcmpPackets == b.ErrorIcmpPackets &&
		a.RealIsDisabled == b.RealIsDisabled &&
		a.RealIsRemoved == b.RealIsRemoved &&
		a.NotRescheduledPackets == b.NotRescheduledPackets &&
		a.BroadcastedIcmpPackets == b.BroadcastedIcmpPackets &&
		a.CreatedSessions == b.CreatedSessions &&
		a.OutgoingPackets == b.OutgoingPackets &&
		a.OutgoingBytes == b.OutgoingBytes
}

func VsCount(b *balancer.Balancer) int {
	return len(b.Config().PacketHandler.Vs)
}

func SelectVS(
	cnt int,
	vs []*balancerpb.VirtualService,
	rng *rand.Rand,
) []*balancerpb.VirtualService {
	if cnt >= len(vs) {
		panic(fmt.Sprintf("selectVS: cnt >= len(vs): %d >= %d", cnt, len(vs)))
	}
	indices := make([]int, len(vs))
	for i := range indices {
		indices[i] = i
	}
	for i := len(indices) - 1; i > 0; i-- {
		j := rng.IntN(i + 1)
		indices[i], indices[j] = indices[j], indices[i]
	}
	result := make([]*balancerpb.VirtualService, cnt)
	for i := range cnt {
		result[i] = vs[indices[i]]
	}
	return result
}

func VSUpdateSomeReals(
	vs *balancerpb.VirtualService,
	rng *rand.Rand,
) *balancerpb.VirtualService {
	reals := make([]*balancerpb.Real, len(vs.Reals))
	copy(reals, vs.Reals)
	for i := len(reals) - 1; i > 0; i-- {
		j := rng.IntN(i + 1)
		reals[i], reals[j] = reals[j], reals[i]
	}
	delta := 2 - rng.IntN(5) // -2 to 2
	replaceCnt := max(0, min(len(reals), len(reals)/2+delta))
	for i := range replaceCnt {
		reals[i] = GenerateReal(rng)
	}
	newCnt := rng.IntN(3)
	for range newCnt {
		reals = append(reals, GenerateReal(rng))
	}
	return &balancerpb.VirtualService{
		Id:          vs.Id,
		AllowedSrcs: vs.AllowedSrcs,
		Flags:       vs.Flags,
		Scheduler:   vs.Scheduler,
		Reals:       reals,
	}
}

func GenerateRealUpdates(
	services []*balancerpb.VirtualService,
	rng *rand.Rand,
) []*balancerpb.RealUpdate {
	updates := make([]*balancerpb.RealUpdate, 0)
	for _, vs := range services {
		upd := VSGenerateRealUpdates(vs, rng)
		updates = append(updates, upd...)
	}
	return updates
}
