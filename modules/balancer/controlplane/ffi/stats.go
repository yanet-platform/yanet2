package ffi

// #cgo CFLAGS: -I../../../../ -I../../../../lib
// #cgo LDFLAGS: -L../../../../build/lib/controlplane/agent -lagent
// #cgo LDFLAGS: -L../../../../build/lib/controlplane/config -lconfig_cp
// #cgo LDFLAGS: -L../../../../build/lib/counters -lcounters
// #cgo LDFLAGS: -L../../../../build/lib/dataplane/config -lconfig_dp
// #cgo LDFLAGS: -L../../../../build/lib/logging -llogging
// #cgo LDFLAGS: -L../../../../build/modules/balancer/api -lbalancer_cp
// #cgo LDFLAGS: -L../../../../build/modules/balancer/state -lbalancer_state
// #cgo LDFLAGS: -L../../../../build/filter -lfilter
//
// #include "modules/balancer/api/stats.h"
// #include "lib/controlplane/agent/agent.h"
// #include <stdlib.h>
import "C"
import (
	"fmt"
	"unsafe"

	"github.com/yanet-platform/yanet2/controlplane/ffi"
	"github.com/yanet-platform/yanet2/modules/balancer/controlplane/module"
)

// statsInfoC is a handle to C-allocated struct balancer_stats_info and its agent context
type statsInfoC struct {
	ptr   *C.struct_balancer_stats_info
	agent ffi.Agent
}

// free releases C-allocated members and the stats struct itself.
// It is safe to call multiple times.
func (s *statsInfoC) free() {
	if s == nil {
		return
	}
	if s.ptr != nil {
		// Free nested arrays via API (uses agent allocator)
		C.balancer_stats_info_free(s.ptr, (*C.struct_agent)(s.agent.AsRawPtr()))
		// Free the struct itself (we allocated it)
		C.free(unsafe.Pointer(s.ptr))
		s.ptr = nil
	}
}

// FillBalancerStatsC calls balancer_stats_info_fill and returns a C-backed stats handle.
// Caller must call (*statsInfoC).free() to avoid leaks.
func FillBalancerStatsC(
	agent ffi.Agent,
	device, pipeline, function, chain, moduleName string,
) (*statsInfoC, error) {
	if agent.AsRawPtr() == nil {
		return nil, fmt.Errorf("agent pointer is nil")
	}

	// Allocate zeroed C struct
	stats := (*C.struct_balancer_stats_info)(
		C.calloc(
			1,
			C.size_t(unsafe.Sizeof(*(*C.struct_balancer_stats_info)(nil))),
		),
	)
	if stats == nil {
		return nil, fmt.Errorf("failed to allocate balancer_stats_info")
	}

	cDevice := C.CString(device)
	defer C.free(unsafe.Pointer(cDevice))
	cPipeline := C.CString(pipeline)
	defer C.free(unsafe.Pointer(cPipeline))
	cFunction := C.CString(function)
	defer C.free(unsafe.Pointer(cFunction))
	cChain := C.CString(chain)
	defer C.free(unsafe.Pointer(cChain))
	cModule := C.CString(moduleName)
	defer C.free(unsafe.Pointer(cModule))

	rc, err := C.balancer_stats_info_fill(
		stats,
		(*C.struct_agent)(agent.AsRawPtr()),
		cDevice,
		cPipeline,
		cFunction,
		cChain,
		cModule,
	)
	if err != nil {
		C.free(unsafe.Pointer(stats))
		return nil, fmt.Errorf("balancer_stats_info_fill failed: %w", err)
	}
	if int(rc) != 0 {
		C.free(unsafe.Pointer(stats))
		return nil, fmt.Errorf(
			"balancer_stats_info_fill returned non-zero: %d",
			int(rc),
		)
	}

	return &statsInfoC{ptr: stats, agent: agent}, nil
}

// BalancerConfigStats is a convenience wrapper that returns a Go-copied Stats and frees C memory.
func BalancerConfigStats(
	agent ffi.Agent,
	state ModuleConfigStatePtr,
	device, pipeline, function, chain, moduleName string,
) (*module.BalancerStats, error) {
	handle, err := FillBalancerStatsC(
		agent,
		device,
		pipeline,
		function,
		chain,
		moduleName,
	)
	if err != nil {
		return nil, err
	}
	defer handle.free()

	return copyFromC(state, handle.ptr), nil
}

func copyFromC(
	state ModuleConfigStatePtr,
	cstats *C.struct_balancer_stats_info,
) *module.BalancerStats {
	var out module.BalancerStats

	// L4
	out.Module.L4 = module.L4Stats{
		IncomingPackets:  uint64(cstats.stats.l4.incoming_packets),
		SelectVSFailed:   uint64(cstats.stats.l4.select_vs_failed),
		InvalidPackets:   uint64(cstats.stats.l4.invalid_packets),
		SelectRealFailed: uint64(cstats.stats.l4.select_real_failed),
		OutgoingPackets:  uint64(cstats.stats.l4.outgoing_packets),
	}

	// ICMP IPv4
	out.Module.ICMPv4 = module.ICMPStats{
		IncomingPackets: uint64(
			cstats.stats.icmp_ipv4.incoming_packets,
		),
		EchoResponses: uint64(
			cstats.stats.icmp_ipv4.echo_responses,
		),
		PayloadTooShortIP: uint64(
			cstats.stats.icmp_ipv4.payload_too_short_ip,
		),
		UnmatchingSrcFromOriginal: uint64(
			cstats.stats.icmp_ipv4.unmatching_src_from_original,
		),
		PayloadTooShortPort: uint64(
			cstats.stats.icmp_ipv4.payload_too_short_port,
		),
		UnexpectedTransport: uint64(
			cstats.stats.icmp_ipv4.unexpected_transport,
		),
		UnrecognizedVS: uint64(
			cstats.stats.icmp_ipv4.unrecognized_vs,
		),
		ForwardedPackets: uint64(
			cstats.stats.icmp_ipv4.forwarded_packets,
		),
		BroadcastedPackets: uint64(
			cstats.stats.icmp_ipv4.broadcasted_packets,
		),
		PacketClonesSent:     uint64(cstats.stats.icmp_ipv4.packet_clones_sent),
		PacketClonesReceived: uint64(cstats.stats.icmp_ipv4.packet_clones_received),
		PacketCloneFailures: uint64(
			cstats.stats.icmp_ipv4.packet_clone_failures,
		),
	}

	// ICMP IPv6
	out.Module.ICMPv6 = module.ICMPStats{
		IncomingPackets: uint64(
			cstats.stats.icmp_ipv6.incoming_packets,
		),
		EchoResponses: uint64(
			cstats.stats.icmp_ipv6.echo_responses,
		),
		PayloadTooShortIP: uint64(
			cstats.stats.icmp_ipv6.payload_too_short_ip,
		),
		UnmatchingSrcFromOriginal: uint64(
			cstats.stats.icmp_ipv6.unmatching_src_from_original,
		),
		PayloadTooShortPort: uint64(
			cstats.stats.icmp_ipv6.payload_too_short_port,
		),
		UnexpectedTransport: uint64(
			cstats.stats.icmp_ipv6.unexpected_transport,
		),
		UnrecognizedVS: uint64(
			cstats.stats.icmp_ipv6.unrecognized_vs,
		),
		ForwardedPackets: uint64(
			cstats.stats.icmp_ipv6.forwarded_packets,
		),
		BroadcastedPackets: uint64(
			cstats.stats.icmp_ipv6.broadcasted_packets,
		),
		PacketClonesSent:     uint64(cstats.stats.icmp_ipv6.packet_clones_sent),
		PacketClonesReceived: uint64(cstats.stats.icmp_ipv6.packet_clones_received),
		PacketCloneFailures: uint64(
			cstats.stats.icmp_ipv6.packet_clone_failures,
		),
	}

	// Common
	out.Module.Common = module.CommonStats{
		IncomingPackets: uint64(cstats.stats.common.incoming_packets),
		IncomingBytes:   uint64(cstats.stats.common.incoming_bytes),
		UnexpectedNetworkProto: uint64(
			cstats.stats.common.unexpected_network_proto,
		),
		DecapSuccessful: uint64(cstats.stats.common.decap_successful),
		DecapFailed:     uint64(cstats.stats.common.decap_failed),
		OutgoingPackets: uint64(cstats.stats.common.outgoing_packets),
		OutgoingBytes:   uint64(cstats.stats.common.outgoing_bytes),
	}

	// VS slice
	vsCount := int(cstats.vs_count)
	if vsCount > 0 && cstats.vs_info != nil {
		cArr := unsafe.Slice(
			(*C.struct_balancer_vs_stats_info)(cstats.vs_info),
			vsCount,
		)
		out.Vs = make([]module.VsStatsInfo, 0, vsCount)
		for i := range vsCount {
			entry := cArr[i]
			vsIdx := uint(entry.vs_registry_idx)
			vsId := state.VirtualServiceInfo(vsIdx).VsIdentifier
			stats := module.VsStats{
				IncomingPackets: uint64(entry.stats.incoming_packets),
				IncomingBytes:   uint64(entry.stats.incoming_bytes),
				PacketSrcNotAllowed: uint64(
					entry.stats.packet_src_not_allowed,
				),
				NoReals:    uint64(entry.stats.no_reals),
				OpsPackets: uint64(entry.stats.ops_packets),
				SessionTableOverflow: uint64(
					entry.stats.session_table_overflow,
				),
				EchoIcmpPackets:  uint64(entry.stats.echo_icmp_packets),
				ErrorIcmpPackets: uint64(entry.stats.error_icmp_packets),
				RealIsDisabled:   uint64(entry.stats.real_is_disabled),
				RealIsRemoved:    uint64(entry.stats.real_is_removed),
				NotRescheduledPackets: uint64(
					entry.stats.not_rescheduled_packets,
				),
				BroadcastedIcmpPackets: uint64(
					entry.stats.broadcasted_icmp_packets,
				),
				CreatedSessions: uint64(entry.stats.created_sessions),
				OutgoingPackets: uint64(entry.stats.outgoing_packets),
				OutgoingBytes:   uint64(entry.stats.outgoing_bytes),
			}

			out.Vs = append(out.Vs, module.VsStatsInfo{
				VsRegistryIdx: vsIdx,
				VsIdentifier:  vsId,
				Stats:         stats,
			})
		}
	}

	// Real slice
	realCount := int(cstats.real_count)
	if realCount > 0 && cstats.real_info != nil {
		cArr := unsafe.Slice(
			(*C.struct_balancer_real_stats_info)(cstats.real_info),
			realCount,
		)
		out.Reals = make([]module.RealStatsInfo, 0, realCount)
		for i := range realCount {
			entry := cArr[i]
			realIdx := uint(entry.real_registry_idx)
			realId := state.RealInfo(realIdx).RealIdentifier
			stats := module.RealStats{
				PacketsRealDisabled: uint64(
					entry.stats.packets_real_disabled,
				),
				PacketsRealNotPresent: uint64(
					entry.stats.packets_real_not_present,
				),
				OpsPackets:       uint64(entry.stats.ops_packets),
				ErrorIcmpPackets: uint64(entry.stats.error_icmp_packets),
				CreatedSessions:  uint64(entry.stats.created_sessions),
				Packets:          uint64(entry.stats.packets),
				Bytes:            uint64(entry.stats.bytes),
			}

			out.Reals = append(out.Reals, module.RealStatsInfo{
				RealRegistryIdx: realIdx,
				RealIdentifier:  realId,
				Stats:           stats,
			})
		}
	}

	return &out
}
