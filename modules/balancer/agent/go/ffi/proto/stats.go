package proto

/*
#cgo CFLAGS: -I../../../ -I../../../../../../
#cgo LDFLAGS: -L../../../../../../build/modules/balancer/agent -lbalancer_agent
#cgo LDFLAGS: -L../../../../../../build/modules/balancer/controlplane/api -lbalancer_cp
#cgo LDFLAGS: -L../../../../../build/modules/balancer/controlplane/handler -lbalancer_packet_handler
#cgo LDFLAGS: -L../../../../../build/modules/balancer/controlplane/state -lbalancer_state
#include "agent.h"
#include "manager.h"
#include "modules/balancer/controlplane/api/graph.h"
#include "modules/balancer/controlplane/api/vs.h"
#include "modules/balancer/controlplane/api/real.h"
#include "modules/balancer/controlplane/api/inspect.h"
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"unsafe"

	"github.com/yanet-platform/yanet2/modules/balancer/agent/balancerpb"
)

// ============================================================
// L4 Stats
// ============================================================

func convertL4Stats(c *C.struct_balancer_l4_stats) *balancerpb.L4Stats {
	if c == nil {
		return nil
	}
	return &balancerpb.L4Stats{
		IncomingPackets:  uint64(c.incoming_packets),
		SelectVsFailed:   uint64(c.select_vs_failed),
		InvalidPackets:   uint64(c.invalid_packets),
		SelectRealFailed: uint64(c.select_real_failed),
		OutgoingPackets:  uint64(c.outgoing_packets),
	}
}

// ============================================================
// Common Stats
// ============================================================

func convertCommonStats(c *C.struct_balancer_common_stats) *balancerpb.CommonStats {
	if c == nil {
		return nil
	}
	return &balancerpb.CommonStats{
		IncomingPackets:        uint64(c.incoming_packets),
		IncomingBytes:          uint64(c.incoming_bytes),
		UnexpectedNetworkProto: uint64(c.unexpected_network_proto),
		DecapSuccessful:        uint64(c.decap_successful),
		DecapFailed:            uint64(c.decap_failed),
		OutgoingPackets:        uint64(c.outgoing_packets),
		OutgoingBytes:          uint64(c.outgoing_bytes),
	}
}

// ============================================================
// ICMP Stats
// ============================================================

func convertIcmpStats(c *C.struct_balancer_icmp_stats) *balancerpb.IcmpStats {
	if c == nil {
		return nil
	}
	return &balancerpb.IcmpStats{
		IncomingPackets:           uint64(c.incoming_packets),
		SrcNotAllowed:             uint64(c.src_not_allowed),
		EchoResponses:             uint64(c.echo_responses),
		PayloadTooShortIp:         uint64(c.payload_too_short_ip),
		UnmatchingSrcFromOriginal: uint64(c.unmatching_src_from_original),
		PayloadTooShortPort:       uint64(c.payload_too_short_port),
		UnexpectedTransport:       uint64(c.unexpected_transport),
		UnrecognizedVs:            uint64(c.unrecognized_vs),
		ForwardedPackets:          uint64(c.forwarded_packets),
		BroadcastedPackets:        uint64(c.broadcasted_packets),
		PacketClonesSent:          uint64(c.packet_clones_sent),
		PacketClonesReceived:      uint64(c.packet_clones_received),
		PacketCloneFailures:       uint64(c.packet_clone_failures),
	}
}

// ============================================================
// VS Stats
// ============================================================

func convertVsStats(c *C.struct_vs_stats) *balancerpb.VsStats {
	if c == nil {
		return nil
	}
	return &balancerpb.VsStats{
		IncomingPackets:        uint64(c.incoming_packets),
		IncomingBytes:          uint64(c.incoming_bytes),
		PacketSrcNotAllowed:    uint64(c.packet_src_not_allowed),
		NoReals:                uint64(c.no_reals),
		OpsPackets:             uint64(c.ops_packets),
		SessionTableOverflow:   uint64(c.session_table_overflow),
		EchoIcmpPackets:        uint64(c.echo_icmp_packets),
		ErrorIcmpPackets:       uint64(c.error_icmp_packets),
		RealIsDisabled:         uint64(c.real_is_disabled),
		RealIsRemoved:          uint64(c.real_is_removed),
		NotRescheduledPackets:  uint64(c.not_rescheduled_packets),
		BroadcastedIcmpPackets: uint64(c.broadcasted_icmp_packets),
		CreatedSessions:        uint64(c.created_sessions),
		OutgoingPackets:        uint64(c.outgoing_packets),
		OutgoingBytes:          uint64(c.outgoing_bytes),
	}
}

// ============================================================
// Allowed Sources Stats
//
// struct allowed_sources_stats {
//     char     tag[MAX_TAG_LEN];   // MAX_TAG_LEN = 16, may NOT be null-terminated
//     uint64_t passes;
// }
// ============================================================

func convertAllowedSourcesStats(
	ptr *C.struct_allowed_sources_stats,
	count C.size_t,
) []*balancerpb.AllowedSourcesStats {
	n := int(count)
	if n == 0 || ptr == nil {
		return nil
	}

	// Zero-copy view into C array
	cSlice := unsafe.Slice(ptr, n)

	result := make([]*balancerpb.AllowedSourcesStats, n)
	for i := range cSlice {
		result[i] = convertAllowedSourcesStat(&cSlice[i])
	}
	return result
}

func convertAllowedSourcesStat(c *C.struct_allowed_sources_stats) *balancerpb.AllowedSourcesStats {
	if c == nil {
		return nil
	}
	return &balancerpb.AllowedSourcesStats{
		// tag[16] may not be null-terminated, use the [16]C.char → string
		// helper that safely handles both null-terminated and full-width cases.
		Tag:    convertFixedCharArray((*[16]C.char)(unsafe.Pointer(&c.tag[0]))),
		Passes: uint64(c.passes),
	}
}

// convertFixedCharArray safely converts a fixed-size C char array that may
// or may not be null-terminated into a Go string without any C calls.
//
//	[16]C.char → Go string
//	- If null byte found: string up to null byte
//	- If no null byte:    full 16-byte string
func convertFixedCharArray(arr *[16]C.char) string {
	b := (*[16]byte)(unsafe.Pointer(arr))
	for i, v := range b {
		if v == 0 {
			return string(b[:i])
		}
	}
	return string(b[:])
}

// ============================================================
// Real Stats
// ============================================================

func convertRealStats(c *C.struct_real_stats) *balancerpb.RealStats {
	if c == nil {
		return nil
	}
	return &balancerpb.RealStats{
		PacketsRealDisabled: uint64(c.packets_real_disabled),
		OpsPackets:          uint64(c.ops_packets),
		ErrorIcmpPackets:    uint64(c.error_icmp_packets),
		CreatedSessions:     uint64(c.created_sessions),
		Packets:             uint64(c.packets),
		Bytes:               uint64(c.bytes),
	}
}
