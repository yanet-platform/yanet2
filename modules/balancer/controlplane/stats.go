package balancer

import (
	"strconv"
	"strings"
	"unsafe"

	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

func aggregateCounter(counter [][]uint64) {
	for idx := 1; idx < len(counter); idx++ {
		for i := range counter[idx] {
			counter[0][i] += counter[idx][i]
		}
	}
}

func commonStats(counter [][]uint64) *balancerpb.CommonStats {
	aggregateCounter(counter)
	commonStats := (*CommonStats)(unsafe.Pointer(&counter[0][0]))
	return &balancerpb.CommonStats{
		IncomingPackets:        commonStats.Incoming_packets,
		IncomingBytes:          commonStats.Incoming_bytes,
		UnexpectedNetworkProto: commonStats.Unexpected_network_proto,
		DecapSuccessful:        commonStats.Decap_successful,
		DecapFailed:            commonStats.Decap_failed,
		OutgoingPackets:        commonStats.Outgoing_packets,
		OutgoingBytes:          commonStats.Outgoing_bytes,
	}
}

func icmpStats(counter [][]uint64) *balancerpb.IcmpStats {
	aggregateCounter(counter)
	icmpStats := (*IcmpStats)(unsafe.Pointer(&counter[0][0]))
	return &balancerpb.IcmpStats{
		IncomingPackets:           icmpStats.Incoming_packets,
		SrcNotAllowed:             icmpStats.Src_not_allowed,
		EchoResponses:             icmpStats.Echo_responses,
		PayloadTooShortIp:         icmpStats.Payload_too_short_ip,
		UnmatchingSrcFromOriginal: icmpStats.Unmatching_src_from_original,
		PayloadTooShortPort:       icmpStats.Payload_too_short_port,
		UnexpectedTransport:       icmpStats.Unexpected_transport,
		UnrecognizedVs:            icmpStats.Unrecognized_vs,
		ForwardedPackets:          icmpStats.Forwarded_packets,
		BroadcastedPackets:        icmpStats.Broadcasted_packets,
		PacketClonesSent:          icmpStats.Packet_clones_sent,
		PacketClonesReceived:      icmpStats.Packet_clones_received,
		PacketCloneFailures:       icmpStats.Packet_clone_failures,
	}
}

func l4Stats(counter [][]uint64) *balancerpb.L4Stats {
	aggregateCounter(counter)
	l4Stats := (*L4Stats)(unsafe.Pointer(&counter[0][0]))
	return &balancerpb.L4Stats{
		IncomingPackets:  l4Stats.Incoming_packets,
		SelectVsFailed:   l4Stats.Select_vs_failed,
		InvalidPackets:   l4Stats.Invalid_packets,
		SelectRealFailed: l4Stats.Select_real_failed,
		OutgoingPackets:  l4Stats.Outgoing_packets,
	}
}

func vsStats(counter [][]uint64) *balancerpb.VsStats {
	aggregateCounter(counter)
	vsStats := (*VsStats)(unsafe.Pointer(&counter[0][0]))
	return &balancerpb.VsStats{
		IncomingPackets:        vsStats.Incoming_packets,
		IncomingBytes:          vsStats.Incoming_bytes,
		PacketSrcNotAllowed:    vsStats.Packet_src_not_allowed,
		NoReals:                vsStats.No_reals,
		SessionTableOverflow:   vsStats.Session_table_overflow,
		EchoIcmpPackets:        vsStats.Echo_icmp_packets,
		ErrorIcmpPackets:       vsStats.Error_icmp_packets,
		RealIsDisabled:         vsStats.Real_is_disabled,
		RealIsRemoved:          vsStats.Real_is_removed,
		NotRescheduledPackets:  vsStats.Not_rescheduled_packets,
		BroadcastedIcmpPackets: vsStats.Broadcasted_icmp_packets,
		CreatedSessions:        vsStats.Created_sessions,
		OutgoingPackets:        vsStats.Outgoing_packets,
		OutgoingBytes:          vsStats.Outgoing_bytes,
	}
}

func realStats(counter [][]uint64) *balancerpb.RealStats {
	aggregateCounter(counter)
	realStats := (*RealStats)(unsafe.Pointer(&counter[0][0]))
	return &balancerpb.RealStats{
		CreatedSessions:     realStats.Created_sessions,
		Packets:             realStats.Packets,
		Bytes:               realStats.Bytes,
		PacketsRealDisabled: realStats.Packets_real_disabled,
		ErrorIcmpPackets:    realStats.Error_icmp_packets,
	}
}

func aggregateACLPasses(counter [][]uint64) uint64 {
	aggregateCounter(counter)
	return counter[0][0]
}

func vsIndexFromCounterName(name string) (uint32, bool) {
	stableIndex, err := strconv.ParseUint(strings.TrimPrefix(name, "vs_"), 10, 64)
	if err != nil {
		return 0, false
	}
	return configIndexOf(stableIndex), true
}

func realIndexFromCounterName(name string) (uint32, uint32, bool) {
	parts := strings.SplitN(strings.TrimPrefix(name, "rl_"), "_", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	vsStableIdx, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	realStableIdx, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return configIndexOf(vsStableIdx), configIndexOf(realStableIdx), true
}

func aclTagFromCounterName(name string) (uint32, string, bool) {
	parts := strings.SplitN(strings.TrimPrefix(name, "acl_"), "_", 2)
	if len(parts) != 2 {
		return 0, "", false
	}
	vsStableIdx, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, "", false
	}
	return configIndexOf(vsStableIdx), parts[1], true
}

func applyCounter(state *balancerpb.BalancerState, counter yanet.CounterInfo) {
	name := counter.Name

	switch {
	case strings.HasPrefix(name, "vs_"):
		vsIndex, ok := vsIndexFromCounterName(name)
		if !ok {
			return
		}
		vsState := state.VirtualServices[vsIndex]
		if vsState == nil {
			return
		}
		vsState.Stats = vsStats(counter.Values)
	case strings.HasPrefix(name, "rl_"):
		vsIndex, realIndex, ok := realIndexFromCounterName(name)
		if !ok {
			return
		}
		vsState := state.VirtualServices[vsIndex]
		if vsState == nil {
			return
		}
		realState := vsState.Reals[realIndex]
		if realState == nil {
			return
		}
		realState.RealStats = realStats(counter.Values)
	case name == "cmn":
		state.CommonStats = commonStats(counter.Values)
	case name == "iv4":
		state.IcmpIpv4Stats = icmpStats(counter.Values)
	case name == "iv6":
		state.IcmpIpv6Stats = icmpStats(counter.Values)
	case name == "l4":
		state.L4Stats = l4Stats(counter.Values)
	case strings.HasPrefix(name, "acl_"):
		vsIndex, tag, ok := aclTagFromCounterName(name)
		if !ok {
			return
		}
		vsState := state.VirtualServices[vsIndex]
		if vsState == nil {
			return
		}
		vsState.AllowedSources = append(vsState.AllowedSources, &balancerpb.AllowedSourcesStats{
			Tag:    tag,
			Passes: aggregateACLPasses(counter.Values),
		})
	}
}

func compactVsState(state *balancerpb.VsState) {
	next := 0
	for idx := range state.Reals {
		if state.Reals[idx] != nil {
			state.Reals[next] = state.Reals[idx]
			next++
		}
	}
	state.Reals = state.Reals[:next]
}

func compactBalancerState(state *balancerpb.BalancerState) {
	next := 0
	for idx := range state.VirtualServices {
		if state.VirtualServices[idx] != nil {
			state.VirtualServices[next] = state.VirtualServices[idx]
			compactVsState(state.VirtualServices[next])
			next++
		}
	}
	state.VirtualServices = state.VirtualServices[:next]
}
