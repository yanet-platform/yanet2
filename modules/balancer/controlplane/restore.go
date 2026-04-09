package balancer

import (
	"time"

	"github.com/yanet-platform/yanet2/common/filterpb"
	"github.com/yanet-platform/yanet2/common/go/relptr"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
	"google.golang.org/protobuf/types/known/durationpb"
)

// restoreBalancerFromPacketHandler reconstructs a Balancer Go object from an
// existing PacketHandler in shared memory. Called on control-plane restart to
// re-attach to already-running packet handlers without re-allocating resources.
//
// The returned Balancer shares the same packet handler memory; no new shared
// memory is allocated.
func restoreBalancerFromPacketHandler(agent *BalancerAgent, ph *PacketHandler) *Balancer {
	b := &Balancer{
		handler: ph,
		agent:   agent,
		config:  restoreConfigFromPacketHandler(ph),
	}
	b.buildIndexes()
	return b
}

func restoreConfigFromPacketHandler(ph *PacketHandler) *balancerpb.BalancerConfig {
	return &balancerpb.BalancerConfig{
		PacketHandler: restorePacketHandlerConfig(ph),
		State:         restoreStateConfig(ph),
	}
}

func restorePacketHandlerConfig(ph *PacketHandler) *balancerpb.PacketHandlerConfig {
	return &balancerpb.PacketHandlerConfig{
		SourceAddressV4:  append([]byte(nil), ph.Source_v4.Bytes[:]...),
		SourceAddressV6:  append([]byte(nil), ph.Source_v6.Bytes[:]...),
		DecapAddresses:   restoreDecapAddrs(ph),
		SessionsTimeouts: restoreSessionTimeouts(&ph.Session_timeouts),
		Vs:               restoreVirtualServices(ph),
	}
}

func restoreSessionTimeouts(t *SessionTimeouts) *balancerpb.SessionsTimeouts {
	return &balancerpb.SessionsTimeouts{
		TcpSynAck: uint32(t.Syn_ack),
		TcpSyn:    uint32(t.Syn),
		TcpFin:    uint32(t.Fin),
		Tcp:       uint32(t.Tcp),
		Udp:       uint32(t.Udp),
	}
}

func restoreDecapAddrs(ph *PacketHandler) [][]byte {
	total := int(ph.Decap_v4_count) + int(ph.Decap_v6_count)
	addrs := make([][]byte, 0, total)
	for _, a := range relptr.Slice(&ph.Decap_v4, ph.Decap_v4_count) {
		addr := make([]byte, 4)
		copy(addr, a.Bytes[:])
		addrs = append(addrs, addr)
	}
	for _, a := range relptr.Slice(&ph.Decap_v6, ph.Decap_v6_count) {
		addr := make([]byte, 16)
		copy(addr, a.Bytes[:])
		addrs = append(addrs, addr)
	}
	return addrs
}

func restoreVirtualServices(ph *PacketHandler) []*balancerpb.VirtualService {
	vsSlice := relptr.Slice(&ph.Vs, ph.Vs_count)
	result := make([]*balancerpb.VirtualService, len(vsSlice))
	for i := range vsSlice {
		result[i] = restoreVS(&vsSlice[i])
	}
	return result
}

func restoreVS(vs *VS) *balancerpb.VirtualService {
	isV6 := vs.Ip_proto == ipprotoIPv6

	addr := append([]byte(nil), vs.Addr.Bytes(int(vs.Ip_proto))...)

	proto := balancerpb.TransportProto_UDP
	if vs.Transport_proto == ipprotoTCP {
		proto = balancerpb.TransportProto_TCP
	}

	return &balancerpb.VirtualService{
		Id: &balancerpb.VsIdentifier{
			Addr:  addr,
			Port:  uint32(vs.Port),
			Proto: proto,
		},
		Flags:       vsFlags(vs.Flags),
		Reals:       restoreReals(vs),
		AllowedSrcs: restoreAllowedSources(vs, isV6),
		Peers:       restorePeers(vs),
		Scheduler:   vs.scheduler(),
	}
}

func restoreReals(vs *VS) []*balancerpb.Real {
	reals := relptr.Slice(&vs.Reals, vs.Reals_count)
	result := make([]*balancerpb.Real, len(reals))
	for i := range reals {
		result[i] = restoreReal(&reals[i])
	}
	return result
}

func restoreReal(r *Real) *balancerpb.Real {
	isV6 := r.Flags&RealFlagIPv6 != 0
	ipProto := ipprotoIP
	if isV6 {
		ipProto = ipprotoIPv6
	}

	ip := append([]byte(nil), r.Addr.Bytes(ipProto)...)

	return &balancerpb.Real{
		Id:     &balancerpb.RelativeRealIdentifier{Ip: ip},
		Weight: r.Weight,
		Src:    restoreIPNet(&r.Src, isV6),
	}
}

// restoreIPNet reads a Net union back to a filterpb.IPNet.
func restoreIPNet(net *Net, isV6 bool) *filterpb.IPNet {
	proto := ipprotoIP
	size := 4
	if isV6 {
		proto = ipprotoIPv6
		size = 16
	}
	addr := make([]byte, size)
	mask := make([]byte, size)
	copy(addr, net.AddrBytes(proto))
	copy(mask, net.MaskBytes(proto))
	return &filterpb.IPNet{Addr: addr, Mask: mask}
}

func restoreAllowedSources(vs *VS, isV6 bool) []*balancerpb.AllowedSources {
	srcs := relptr.Slice(&vs.Allowed_sources, vs.Allowed_sources_count)
	result := make([]*balancerpb.AllowedSources, len(srcs))
	for i := range srcs {
		result[i] = restoreAllowedSource(&srcs[i], isV6)
	}
	return result
}

func restoreAllowedSource(src *AllowedSource, isV6 bool) *balancerpb.AllowedSources {
	rawNets := relptr.Slice(&src.Nets, src.Nets_count)
	nets := make([]*filterpb.IPNet, len(rawNets))
	for i := range rawNets {
		nets[i] = restoreIPNet(&rawNets[i], isV6)
	}

	rawPr := relptr.Slice(&src.Port_ranges, src.Port_ranges_count)
	pr := make([]*filterpb.PortRange, len(rawPr))
	for i, p := range rawPr {
		pr[i] = &filterpb.PortRange{
			From: uint32(p.From),
			To:   uint32(p.To),
		}
	}

	result := &balancerpb.AllowedSources{
		Nets:  nets,
		Ports: pr,
	}

	tag := cStringToGo(src.Tag[:])
	if len(tag) > 0 {
		result.Tag = &tag
	}

	return result
}

func cStringToGo(b []int8) string {
	s := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		s = append(s, byte(c))
	}
	return string(s)
}

func restorePeers(vs *VS) [][]byte {
	total := int(vs.Peers_v4_count) + int(vs.Peers_v6_count)
	peers := make([][]byte, 0, total)
	for _, p := range relptr.Slice(&vs.Peers_v4, vs.Peers_v4_count) {
		addr := make([]byte, 4)
		copy(addr, p.Bytes[:])
		peers = append(peers, addr)
	}
	for _, p := range relptr.Slice(&vs.Peers_v6, vs.Peers_v6_count) {
		addr := make([]byte, 16)
		copy(addr, p.Bytes[:])
		peers = append(peers, addr)
	}
	return peers
}

func restoreStateConfig(ph *PacketHandler) *balancerpb.StateConfig {
	capacity := uint64(relptr.Deref(&ph.Session_table).capacity())
	maxLoadFactor := ph.Session_table_max_load_factor
	wlcPower := uint64(ph.Wlc_power)
	wlcMaxWeight := uint32(ph.Wlc_max_weight)
	refreshPeriod := durationpb.New(
		time.Duration(ph.Refresh_period_ms) * time.Millisecond,
	)

	return &balancerpb.StateConfig{
		SessionTableCapacity:      &capacity,
		SessionTableMaxLoadFactor: &maxLoadFactor,
		Wlc: &balancerpb.WlcConfig{
			Power:     &wlcPower,
			MaxWeight: &wlcMaxWeight,
		},
		RefreshPeriod: refreshPeriod,
	}
}
