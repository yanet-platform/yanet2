package balancer

import (
	"strconv"
	"strings"
	"unsafe"

	"github.com/yanet-platform/yanet2/common/go/relptr"
	yanet "github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/balancerpb"
)

// Aggregate the worker local counters into the first counter.
func aggregateCounter(counter [][]uint64) {
	for idx := 1; idx < len(counter); idx++ {
		for i := range counter[idx] {
			counter[0][i] += counter[idx][i]
		}
	}
}

func commonStats(counter [][]uint64) *CommonStats {
	aggregateCounter(counter)
	return (*CommonStats)(unsafe.Pointer(&counter[0][0]))
}

func (common *CommonStats) proto() *balancerpb.CommonStats {
	return &balancerpb.CommonStats{
		IncomingPackets:        common.Incoming_packets,
		IncomingBytes:          common.Incoming_bytes,
		UnexpectedNetworkProto: common.Unexpected_network_proto,
		DecapSuccessful:        common.Decap_successful,
		DecapFailed:            common.Decap_failed,
		OutgoingPackets:        common.Outgoing_packets,
		OutgoingBytes:          common.Outgoing_bytes,
	}
}

func icmpStats(counter [][]uint64) *IcmpStats {
	aggregateCounter(counter)
	return (*IcmpStats)(unsafe.Pointer(&counter[0][0]))
}

func (i *IcmpStats) proto() *balancerpb.IcmpStats {
	return &balancerpb.IcmpStats{
		IncomingPackets:           i.Incoming_packets,
		SrcNotAllowed:             i.Src_not_allowed,
		EchoResponses:             i.Echo_responses,
		PayloadTooShortIp:         i.Payload_too_short_ip,
		UnmatchingSrcFromOriginal: i.Unmatching_src_from_original,
		PayloadTooShortPort:       i.Payload_too_short_port,
		UnexpectedTransport:       i.Unexpected_transport,
		UnrecognizedVs:            i.Unrecognized_vs,
		ForwardedPackets:          i.Forwarded_packets,
		BroadcastedPackets:        i.Broadcasted_packets,
		PacketClonesSent:          i.Packet_clones_sent,
		PacketClonesReceived:      i.Packet_clones_received,
		PacketCloneFailures:       i.Packet_clone_failures,
	}
}

func l4Stats(counter [][]uint64) *L4Stats {
	aggregateCounter(counter)
	return (*L4Stats)(unsafe.Pointer(&counter[0][0]))
}

func (l4 *L4Stats) proto() *balancerpb.L4Stats {
	return &balancerpb.L4Stats{
		IncomingPackets:  l4.Incoming_packets,
		SelectVsFailed:   l4.Select_vs_failed,
		InvalidPackets:   l4.Invalid_packets,
		SelectRealFailed: l4.Select_real_failed,
		OutgoingPackets:  l4.Outgoing_packets,
	}
}

func vsStats(counter [][]uint64) *VsStats {
	aggregateCounter(counter)
	return (*VsStats)(unsafe.Pointer(&counter[0][0]))
}

func (vs *VsStats) proto() *balancerpb.VsStats {
	return &balancerpb.VsStats{
		IncomingPackets:        vs.Incoming_packets,
		IncomingBytes:          vs.Incoming_bytes,
		PacketSrcNotAllowed:    vs.Packet_src_not_allowed,
		NoReals:                vs.No_reals,
		SessionTableOverflow:   vs.Session_table_overflow,
		EchoIcmpPackets:        vs.Echo_icmp_packets,
		ErrorIcmpPackets:       vs.Error_icmp_packets,
		RealIsDisabled:         vs.Real_is_disabled,
		RealIsRemoved:          vs.Real_is_removed,
		NotRescheduledPackets:  vs.Not_rescheduled_packets,
		BroadcastedIcmpPackets: vs.Broadcasted_icmp_packets,
		CreatedSessions:        vs.Created_sessions,
		OutgoingPackets:        vs.Outgoing_packets,
		OutgoingBytes:          vs.Outgoing_bytes,
	}
}

func realStats(counter [][]uint64) *RealStats {
	aggregateCounter(counter)
	return (*RealStats)(unsafe.Pointer(&counter[0][0]))
}

func (rs *RealStats) proto() *balancerpb.RealStats {
	return &balancerpb.RealStats{
		CreatedSessions:     rs.Created_sessions,
		Packets:             rs.Packets,
		Bytes:               rs.Bytes,
		PacketsRealDisabled: rs.Packets_real_disabled,
		ErrorIcmpPackets:    rs.Error_icmp_packets,
	}
}

func aggregateACLPasses(counter [][]uint64) uint64 {
	aggregateCounter(counter)
	return counter[0][0]
}

func vsIndexFromCounterName(name string) (uint64, bool) {
	stableIndex, err := strconv.ParseUint(strings.TrimPrefix(name, "vs_"), 10, 64)
	if err != nil {
		return 0, false
	}
	return stableIndex, true
}

func realIndexFromCounterName(name string) (uint64, uint64, bool) {
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
	return vsStableIdx, realStableIdx, true
}

func aclTagFromCounterName(name string) (uint64, string, bool) {
	parts := strings.SplitN(strings.TrimPrefix(name, "acl_"), "_", 2)
	if len(parts) != 2 {
		return 0, "", false
	}
	vsStableIdx, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return 0, "", false
	}
	return vsStableIdx, parts[1], true
}

func applyCounter(
	handler *PacketHandler,
	state *balancerpb.BalancerState,
	counter yanet.CounterInfo,
) {
	name := counter.Name

	services := relptr.Slice(&handler.Vs, handler.Vs_count)

	switch {
	case strings.HasPrefix(name, "vs_"):
		vsStableIndex, ok := vsIndexFromCounterName(name)
		if !ok {
			return
		}
		vsConfigIndex := configIndexOf(vsStableIndex)
		if services[vsConfigIndex].isRemoved() ||
			services[vsConfigIndex].Stable_idx != vsStableIndex {
			return
		}

		vsState := state.VirtualServices[vsConfigIndex]
		if vsState == nil {
			return
		}
		vsState.Stats = vsStats(counter.Values).proto()
	case strings.HasPrefix(name, "rl_"):
		vsStableIndex, realStableIndex, ok := realIndexFromCounterName(name)
		if !ok {
			return
		}
		vsConfigIndex := configIndexOf(vsStableIndex)
		if services[vsConfigIndex].isRemoved() ||
			services[vsConfigIndex].Stable_idx != vsStableIndex {
			return
		}
		realConfigIndex := configIndexOf(realStableIndex)
		vs := &services[vsConfigIndex]
		reals := relptr.Slice(&vs.Reals, vs.Reals_count)
		if reals[realConfigIndex].isRemoved() ||
			reals[realConfigIndex].Stable_idx != realStableIndex {
			return
		}
		vsState := state.VirtualServices[vsConfigIndex]
		if vsState == nil {
			return
		}
		realState := vsState.Reals[realConfigIndex]
		if realState == nil {
			return
		}
		realState.RealStats = realStats(counter.Values).proto()
	case name == "cmn":
		state.CommonStats = commonStats(counter.Values).proto()
	case name == "iv4":
		state.IcmpIpv4Stats = icmpStats(counter.Values).proto()
	case name == "iv6":
		state.IcmpIpv6Stats = icmpStats(counter.Values).proto()
	case name == "l4":
		state.L4Stats = l4Stats(counter.Values).proto()
	case strings.HasPrefix(name, "acl_"):
		vsStableIndex, tag, ok := aclTagFromCounterName(name)
		if !ok {
			return
		}
		vsConfigIndex := configIndexOf(vsStableIndex)
		if services[vsConfigIndex].isRemoved() ||
			services[vsConfigIndex].Stable_idx != vsStableIndex {
			return
		}
		vsState := state.VirtualServices[vsStableIndex]
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
